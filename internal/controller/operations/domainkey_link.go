/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package operations

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	operationsv1alpha1 "github.com/openkcm/platform-mesh-controller/api/operations/v1alpha1"
	"github.com/openkcm/platform-mesh-controller/api/shared"
	"github.com/openkcm/platform-mesh-controller/internal/openkcmapi"
)

const (
	rootKeyLinkedType         = "RootKeyLinked"
	reasonLinked              = "Linked"
	reasonUnlinked            = "Unlinked"
	reasonLinkFailed          = "LinkFailed"
	reasonProviderUnsupported = "ProviderUnsupported"
)

type l1ProviderBackend interface {
	CreateProvider(ctx context.Context, rootKeyID string, req openkcmapi.CreateProviderRequest) (*openkcmapi.GetRootKeyResponse, error)
	RemoveProvider(ctx context.Context, rootKeyID string) error
	ActivateKey(ctx context.Context, id string) (*openkcmapi.ActivateKeyResponse, error)
	DeleteRootKey(ctx context.Context, id string) error
}

var _ l1ProviderBackend = (*openkcmapi.Client)(nil)

type linkError struct {
	reason string
	err    error
}

func (e *linkError) Error() string { return e.err.Error() }
func (e *linkError) Unwrap() error { return e.err }

func (r *DomainKeyReconciler) syncRootKeyLink(
	ctx context.Context, cl client.Client, dk *operationsv1alpha1.DomainKey,
) (ctrl.Result, bool, error) {
	if dk.Status.L1KeyID == "" {
		return ctrl.Result{}, false, nil
	}
	logger := log.FromContext(ctx).WithValues("l1KeyID", dk.Status.L1KeyID)

	l1, err := r.L1Keys.GetRootKey(ctx, dk.Status.L1KeyID)
	if err != nil {
		return ctrl.Result{}, true, err
	}

	ref := dk.Spec.PrimaryRootKeyRef
	wantLinked := ref != nil && ref.Name != ""
	if !wantLinked && !l1.Linked {
		setRootKeyLinked(dk, metav1.ConditionFalse, reasonUnlinked, "No root key is linked.")
		return ctrl.Result{}, false, nil
	}

	linker, ok := r.L1Keys.(l1ProviderBackend)
	if !ok {
		logger.V(1).Info("The backend cannot link root keys; the DomainKey stays unlinked")
		return ctrl.Result{}, false, nil
	}
	if l1.ProcessingState != processingStateReady {
		return ctrl.Result{RequeueAfter: pollInterval}, true, nil
	}

	if !wantLinked {
		if err := linker.RemoveProvider(ctx, dk.Status.L1KeyID); err != nil {
			return ctrl.Result{}, true, err
		}
		logger.Info("DomainKey unlinked from its root key")
		setRootKeyLinked(dk, metav1.ConditionFalse, reasonUnlinked, "No root key is linked.")
		return ctrl.Result{}, false, nil
	}

	provider, err := r.rootKeyProvider(ctx, cl, dk)
	if err != nil {
		reason := reasonLinkFailed
		if le, ok := errors.AsType[*linkError](err); ok {
			reason = le.reason
		}
		r.setFailedCondition(ctx, cl, dk, reason, err.Error())
		return ctrl.Result{RequeueAfter: pollInterval}, true, nil
	}

	lifecycleState := l1.LifecycleState
	if !l1.Linked || l1.Provider != provider.Provider || !maps.Equal(l1.Config, provider.Config) {
		if l1.Linked {
			if err := linker.RemoveProvider(ctx, dk.Status.L1KeyID); err != nil {
				return ctrl.Result{}, true, err
			}
		}
		linked, err := linker.CreateProvider(ctx, dk.Status.L1KeyID, provider)
		if err != nil {
			if openkcmapi.IsRetryable(err) {
				return ctrl.Result{}, true, err
			}
			r.setFailedCondition(ctx, cl, dk, reasonLinkFailed, err.Error())
			return ctrl.Result{RequeueAfter: pollInterval}, true, nil
		}
		lifecycleState = linked.LifecycleState
		logger.Info("DomainKey linked to its root key", "rootKey", ref.Kind+"/"+ref.Name)
	}

	if lifecycleState == string(shared.LifecyclePreActive) {
		if _, err := linker.ActivateKey(ctx, dk.Status.L1KeyID); err != nil {
			return ctrl.Result{}, true, err
		}
	}

	setRootKeyLinked(dk, metav1.ConditionTrue, reasonLinked,
		fmt.Sprintf("Linked to %s %s/%s.", ref.Kind, rootKeyReferenceNamespace(ref, dk.Namespace), ref.Name))
	return ctrl.Result{}, false, nil
}

func (r *DomainKeyReconciler) rootKeyProvider(
	ctx context.Context, cl client.Client, dk *operationsv1alpha1.DomainKey,
) (openkcmapi.CreateProviderRequest, error) {
	ref := dk.Spec.PrimaryRootKeyRef
	if ref.Kind != awsRootKeyKind {
		return openkcmapi.CreateProviderRequest{}, &linkError{
			reason: reasonProviderUnsupported,
			err:    fmt.Errorf("linking a %s is not supported yet; only AWSRootKey", ref.Kind),
		}
	}
	namespace, _ := allowedRootKeyReferenceNamespace(ref, dk.Namespace, r.AccountNamespace)
	rk := &operationsv1alpha1.AWSRootKey{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, rk); err != nil {
		return openkcmapi.CreateProviderRequest{}, &linkError{reason: "RootKeyResolutionFailed", err: err}
	}

	accessKey, err := readAWSAccessKey(ctx, cl, rk)
	if err != nil {
		reason := awsCredentialsFailureReason(err)
		if reason == "" {
			reason = reasonLinkFailed
		}
		return openkcmapi.CreateProviderRequest{}, &linkError{reason: reason, err: err}
	}

	config := map[string]string{
		"region": rk.Spec.Region,
		"keyArn": rk.Spec.KeyURI,
	}
	if rk.Spec.Auth != nil && rk.Spec.Auth.AccessKey != nil && rk.Spec.Auth.AccessKey.RoleARN != "" {
		config["roleArn"] = rk.Spec.Auth.AccessKey.RoleARN
	}
	return openkcmapi.CreateProviderRequest{
		Provider: "aws",
		Config:   config,
		Auth: map[string]string{
			"accessKeyId":     accessKey.ID,
			"secretAccessKey": accessKey.Secret,
		},
	}, nil
}

func (r *DomainKeyReconciler) deleteL1Key(ctx context.Context, dk *operationsv1alpha1.DomainKey) error {
	if dk.Status.L1KeyID == "" {
		return nil
	}
	linker, ok := r.L1Keys.(l1ProviderBackend)
	if !ok {
		return nil
	}
	if err := linker.RemoveProvider(ctx, dk.Status.L1KeyID); err != nil && !openkcmapi.IsNotFound(err) {
		return err
	}
	if err := linker.DeleteRootKey(ctx, dk.Status.L1KeyID); err != nil && !openkcmapi.IsNotFound(err) {
		return err
	}
	log.FromContext(ctx).Info("DomainKey L1 deleted from OpenKCM", "l1KeyID", dk.Status.L1KeyID)
	return nil
}

func setRootKeyLinked(dk *operationsv1alpha1.DomainKey, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&dk.Status.Conditions, metav1.Condition{
		Type:               rootKeyLinkedType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: dk.Generation,
	})
}
