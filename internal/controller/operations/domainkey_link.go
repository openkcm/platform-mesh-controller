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
	rootKeyLinkedType             = "RootKeyLinked"
	rootKeyChangeType             = "RootKeyChangeSupported"
	reasonLinked                  = "Linked"
	reasonUnlinked                = "Unlinked"
	reasonLinkFailed              = "LinkFailed"
	reasonProviderUnsupported     = "ProviderUnsupported"
	reasonChangeUnsupported       = "Unsupported"
	reasonRootKeyProcessingFailed = "RootKeyProcessingFailed"

	reasonAwaitingRootKeyActivation = "AwaitingRootKeyActivation"
	reasonActivating                = "Activating"
	processingStateFailed           = "failed"
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
	recorded := recordedLink(dk)
	if !wantLinked && !l1.Linked && recorded == nil {
		setRootKeyLinked(dk, metav1.ConditionFalse, reasonUnlinked, "No root key is linked.")
		return ctrl.Result{}, false, nil
	}

	linker, ok := r.L1Keys.(l1ProviderBackend)
	if !ok {
		logger.V(1).Info("The backend cannot link root keys; the DomainKey stays unlinked")
		return ctrl.Result{}, false, nil
	}
	switch l1.ProcessingState {
	case processingStateReady:
	case processingStateFailed:
		r.setFailedCondition(ctx, cl, dk, reasonRootKeyProcessingFailed,
			"The backend failed to process the root key; see its log.")
		return ctrl.Result{RequeueAfter: pollInterval}, true, nil
	default:
		logger.Info("Waiting for the backend to finish processing the L1",
			"processing", l1.ProcessingState, "lifecycle", l1.LifecycleState)
		return ctrl.Result{RequeueAfter: pollInterval}, true, nil
	}

	if !wantLinked {
		if err := linker.RemoveProvider(ctx, dk.Status.L1KeyID); err != nil {
			if errors.Is(err, errors.ErrUnsupported) {
				r.recordRootKeyChange(ctx, cl, dk,
					"Removing the root key is not supported by this backend yet; the DomainKey stays linked.")
				return ctrl.Result{}, false, nil
			}
			return ctrl.Result{}, true, err
		}
		logger.Info("DomainKey unlinked from its root key")
		clearRootKeyChange(dk)
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

	linkedMessage := rootKeyLinkedMessage(dk, ref)
	create, replace := true, false
	switch {
	case l1.Linked:
		replace = l1.Provider != provider.Provider || !maps.Equal(l1.Config, provider.Config)
		create = replace
	case recorded != nil:
		replace = recorded.Message != linkedMessage
		create = replace
	}

	lifecycleState := l1.LifecycleState
	if create {
		if replace {
			if err := linker.RemoveProvider(ctx, dk.Status.L1KeyID); err != nil {
				if errors.Is(err, errors.ErrUnsupported) {
					r.recordRootKeyChange(ctx, cl, dk,
						"Changing the root key of a linked DomainKey is not supported by this backend yet; it stays linked as before.")
					return ctrl.Result{}, false, nil
				}
				return ctrl.Result{}, true, err
			}
		}
		logger.Info("Binding the root key provider in the backend (CreateProvider)",
			"rootKey", ref.Kind+"/"+ref.Name,
			"provider", provider.Provider,
			"region", provider.Config[openkcmapi.AWSConfigRegion],
			"keyArn", provider.Config[openkcmapi.AWSConfigKeyARN],
			"roleArn", provider.Config[openkcmapi.AWSConfigRoleARN])
		linked, err := linker.CreateProvider(ctx, dk.Status.L1KeyID, provider)
		if err != nil {
			if openkcmapi.IsRetryable(err) {
				return ctrl.Result{}, true, err
			}
			r.setFailedCondition(ctx, cl, dk, reasonLinkFailed, err.Error())
			return ctrl.Result{RequeueAfter: pollInterval}, true, nil
		}
		logger.Info("The backend accepted the root key provider; the L1 is linked",
			"rootKey", ref.Kind+"/"+ref.Name, "lifecycle", linked.LifecycleState)
		lifecycleState = linked.LifecycleState
		setRootKeyLinked(dk, metav1.ConditionTrue, reasonLinked, linkedMessage)
		clearRootKeyChange(dk)
		if err := cl.Status().Update(ctx, dk); err != nil {
			return ctrl.Result{}, true, err
		}
	}

	if lifecycleState == string(shared.LifecyclePreActive) {
		logger.Info("Asking the backend to activate the L1 (an asynchronous job)")
		if _, err := linker.ActivateKey(ctx, dk.Status.L1KeyID); err != nil {
			return ctrl.Result{}, true, err
		}
		logger.Info("The backend accepted the L1 activation job")
	}

	if recorded != nil && recorded.Message == linkedMessage {
		clearRootKeyChange(dk)
	}
	setRootKeyLinked(dk, metav1.ConditionTrue, reasonLinked, linkedMessage)
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
		openkcmapi.AWSConfigRegion: rk.Spec.Region,
		openkcmapi.AWSConfigKeyARN: rk.Spec.KeyURI,
	}
	if rk.Spec.Auth != nil && rk.Spec.Auth.AccessKey != nil && rk.Spec.Auth.AccessKey.RoleARN != "" {
		config[openkcmapi.AWSConfigRoleARN] = rk.Spec.Auth.AccessKey.RoleARN
	}
	return openkcmapi.CreateProviderRequest{
		Provider: openkcmapi.ProviderAWS,
		Config:   config,
		Auth: map[string]string{
			openkcmapi.AWSAuthAccessKeyID:     accessKey.ID,
			openkcmapi.AWSAuthSecretAccessKey: accessKey.Secret,
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
	if err := linker.RemoveProvider(ctx, dk.Status.L1KeyID); err != nil &&
		!openkcmapi.IsNotFound(err) && !errors.Is(err, errors.ErrUnsupported) {
		return err
	}
	if err := linker.DeleteRootKey(ctx, dk.Status.L1KeyID); err != nil &&
		!openkcmapi.IsNotFound(err) && !errors.Is(err, errors.ErrUnsupported) {
		return err
	}
	log.FromContext(ctx).Info("DomainKey L1 deleted from OpenKCM", "l1KeyID", dk.Status.L1KeyID)
	return nil
}

func rootKeyLinkedMessage(dk *operationsv1alpha1.DomainKey, ref *shared.TypedReference) string {
	return fmt.Sprintf("Linked to %s %s/%s.", ref.Kind, rootKeyReferenceNamespace(ref, dk.Namespace), ref.Name)
}

func recordedLink(dk *operationsv1alpha1.DomainKey) *metav1.Condition {
	c := meta.FindStatusCondition(dk.Status.Conditions, rootKeyLinkedType)
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != reasonLinked {
		return nil
	}
	return c
}

func clearRootKeyChange(dk *operationsv1alpha1.DomainKey) {
	meta.RemoveStatusCondition(&dk.Status.Conditions, rootKeyChangeType)
}

func (r *DomainKeyReconciler) recordRootKeyChange(
	ctx context.Context, cl client.Client, dk *operationsv1alpha1.DomainKey, message string,
) {
	changed := meta.SetStatusCondition(&dk.Status.Conditions, metav1.Condition{
		Type:               rootKeyChangeType,
		Status:             metav1.ConditionFalse,
		Reason:             reasonChangeUnsupported,
		Message:            message,
		ObservedGeneration: dk.Generation,
	})
	if changed {
		log.FromContext(ctx).Info(message)
		_ = cl.Status().Update(ctx, dk)
	}
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
