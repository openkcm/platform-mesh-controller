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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	"github.com/openkcm/openkcm-controller/api/shared"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

const awsRootKeyFinalizer = "operations.openkcm.io/awsrootkey-cleanup"

// AWSRootKeyReconciler reconciles an AWSRootKey (L1) via the mock API.
// Real AWS KMS / Roles Anywhere wiring is a follow-up; v0.7.0 stands in
// the mock API for upstream calls per #216 acceptance criteria.
type AWSRootKeyReconciler struct {
	APIClient        Backend
	Manager          mcmanager.Manager
	AccountNamespace string
}

// +kubebuilder:rbac:groups=operations.openkcm.io,resources=awsrootkeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=awsrootkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=awsrootkeys/finalizers,verbs=update
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=tenants,verbs=get

func (r *AWSRootKeyReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("cluster", req.ClusterName)

	cl, err := r.clusterClient(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	rk := &operationsv1alpha1.AWSRootKey{}
	if err := cl.Get(ctx, req.NamespacedName, rk); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !rk.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, cl, rk)
	}

	if controllerutil.AddFinalizer(rk, awsRootKeyFinalizer) {
		if err := cl.Update(ctx, rk); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	desired := desiredLifecycleState(rk.Spec.Lifecycle)
	if rk.Status.CryptoState != nil &&
		rk.Status.CryptoState.LifecycleState == desired &&
		rk.Status.ObservedGeneration == rk.Generation {
		return ctrl.Result{}, ensureAutoDomainKeysForActiveAccountRoot(ctx, cl, rk.Namespace, r.AccountNamespace, rk.Status.CryptoState)
	}

	if rk.Status.CryptoState == nil || rk.Status.CryptoState.ID == "" {
		account, err := resolveAccount(ctx, cl, r.AccountNamespace)
		if err != nil {
			r.setFailed(ctx, cl, rk, "TenantResolutionFailed", err.Error())
			return ctrl.Result{}, err
		}
		if rk.Spec.TenantNameRef != "" && rk.Spec.TenantNameRef != account.Name {
			logger.Info("ignoring spec.tenantNameRef; using path-derived account",
				"specName", rk.Spec.TenantNameRef, "accountName", account.Name)
		}
		tenantID, err := resolveTenantID(ctx, cl, account)
		if err != nil {
			r.setFailed(ctx, cl, rk, "TenantResolutionFailed", err.Error())
			return ctrl.Result{}, err
		}
		if tenantID == "" {
			r.setFailed(ctx, cl, rk, "AwaitingTenant", "Tenant is not registered in the backend yet; waiting.")
			return ctrl.Result{RequeueAfter: pollInterval}, nil
		}

		resp, err := r.APIClient.CreateRootKey(ctx, openkcmapi.CreateRootKeyRequest{
			TenantID: tenantID,
			Provider: "aws",
			Name:     rk.Name,
			Config: map[string]string{
				"region":         rk.Spec.Region,
				"keyUri":         rk.Spec.KeyURI,
				"endpointUrl":    rk.Spec.EndpointURL,
				"trustAnchorArn": rk.Spec.RolesAnywhere.TrustAnchorARN,
				"profileArn":     rk.Spec.RolesAnywhere.ProfileARN,
				"roleArn":        rk.Spec.RolesAnywhere.RoleARN,
			},
		})
		if err != nil {
			r.setFailed(ctx, cl, rk, "RootKeyCreateFailed", err.Error())
			return ctrl.Result{}, err
		}
		logger.Info("AWSRootKey registered", "rootKeyID", resp.ID)

		now := metav1.Now()
		rk.Status.CryptoState = &shared.CryptoState{
			ID:             resp.ID,
			LifecycleState: shared.LifecyclePreActive,
		}
		rk.Status.OperationID = resp.ID
		rk.Status.ReconciliationStatus = &shared.ReconciliationStatus{
			Success:            true,
			Message:            "AWS root key registered; awaiting activation.",
			InternalKeyID:      resp.ID,
			LastTransitionTime: &now,
			IdentityInfo: &shared.IdentityInfo{
				Subject: "CN=" + account.Name + " OU=Krypton, O=OpenKCM",
				CertificateSecretRef: &shared.SecretKeyReference{
					Name:      "aws-kms-ca",
					Namespace: openkcmSystemNamespace,
					Key:       caCertKey,
				},
			},
		}
		meta.SetStatusCondition(&rk.Status.Conditions, metav1.Condition{
			Type:               readyType,
			Status:             metav1.ConditionFalse,
			Reason:             reasonProcess,
			Message:            rootKeyRegisteredMessage,
			ObservedGeneration: rk.Generation,
		})
		rk.Status.ObservedGeneration = rk.Generation
		if err := cl.Status().Update(ctx, rk); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	resp, err := r.APIClient.GetRootKey(ctx, rk.Status.CryptoState.ID)
	if err != nil {
		return ctrl.Result{}, err
	}
	if resp.ProcessingState != processingStateReady {
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	// PreActive → Active happens once. After that, Active⇄Deactivated is
	// driven by spec.lifecycle in the reconcileLifecycle step below.
	if rk.Status.CryptoState.LifecycleState == shared.LifecyclePreActive {
		activateResp, err := r.APIClient.ActivateKey(ctx, rk.Status.CryptoState.ID)
		if err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("AWSRootKey activated", "rootKeyID", rk.Status.CryptoState.ID)
		now := metav1.Now()
		rk.Status.CryptoState.LifecycleState = shared.LifecycleState(activateResp.LifecycleState)
		rk.Status.CryptoState.Version = activateResp.Version
		rk.Status.CryptoState.LastRotatedAt = &now
		rk.Status.ReconciliationStatus.LastTransitionTime = &now
		rk.Status.ReconciliationStatus.Message = "AWS KMS bound and authenticated."
		meta.SetStatusCondition(&rk.Status.Conditions, metav1.Condition{
			Type:               readyType,
			Status:             metav1.ConditionTrue,
			Reason:             reasonUpstreamAuthenticated,
			Message:            "Successfully bound to AWS KMS",
			ObservedGeneration: rk.Generation,
		})
	}

	newState, transitioned, err := reconcileLifecycle(
		ctx, r.APIClient, rk.Status.CryptoState.ID,
		rk.Status.CryptoState.LifecycleState, rk.Spec.Lifecycle,
	)
	if err != nil {
		r.setFailed(ctx, cl, rk, "LifecycleTransitionFailed", err.Error())
		return ctrl.Result{}, err
	}
	if transitioned {
		now := metav1.Now()
		rk.Status.CryptoState.LifecycleState = newState
		rk.Status.CryptoState.LastRotatedAt = &now
		if newState == shared.LifecycleDeactivated {
			meta.SetStatusCondition(&rk.Status.Conditions, metav1.Condition{
				Type:               readyType,
				Status:             metav1.ConditionFalse,
				Reason:             reasonDeactivated,
				Message:            "AWS root key deactivated per spec.lifecycle.",
				ObservedGeneration: rk.Generation,
			})
			if err := r.cascadeDeactivate(ctx, cl, rk); err != nil {
				return ctrl.Result{}, err
			}
		} else {
			meta.SetStatusCondition(&rk.Status.Conditions, metav1.Condition{
				Type:               readyType,
				Status:             metav1.ConditionTrue,
				Reason:             reasonUpstreamAuthenticated,
				Message:            "AWS root key re-activated.",
				ObservedGeneration: rk.Generation,
			})
		}
	}
	rk.Status.ObservedGeneration = rk.Generation
	if err := cl.Status().Update(ctx, rk); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, ensureAutoDomainKeysForActiveAccountRoot(ctx, cl, rk.Namespace, r.AccountNamespace, rk.Status.CryptoState)
}

// cascadeDeactivate finds all DomainKeys in the same namespace that reference
// this AWSRootKey via primary or fallback ref, and patches their
// spec.lifecycle to Deactivated. Each DomainKey controller will then deactivate
// itself and cascade further to ServiceKeys and DEKs.
func (r *AWSRootKeyReconciler) cascadeDeactivate(ctx context.Context, cl client.Client, rk *operationsv1alpha1.AWSRootKey) error {
	return cascadeDeactivateRootKey(ctx, cl, "AWSRootKey", rk.Namespace, rk.Name)
}

func (r *AWSRootKeyReconciler) handleDeletion(ctx context.Context, cl client.Client, rk *operationsv1alpha1.AWSRootKey) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(rk, awsRootKeyFinalizer) {
		return ctrl.Result{}, nil
	}
	if rk.Status.CryptoState != nil && rk.Status.CryptoState.ID != "" {
		if err := r.APIClient.DeleteRootKey(ctx, rk.Status.CryptoState.ID); err != nil {
			logger.Error(err, "Failed to delete AWSRootKey in OpenKCM; will retry", "rootKeyID", rk.Status.CryptoState.ID)
			return ctrl.Result{}, err
		}
	}
	controllerutil.RemoveFinalizer(rk, awsRootKeyFinalizer)
	if err := cl.Update(ctx, rk); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *AWSRootKeyReconciler) setFailed(ctx context.Context, cl client.Client, rk *operationsv1alpha1.AWSRootKey, reason, message string) {
	now := metav1.Now()
	rk.Status.ReconciliationStatus = &shared.ReconciliationStatus{
		Success:            false,
		Message:            message,
		LastTransitionTime: &now,
		Errors:             []string{reason + ": " + message},
	}
	meta.SetStatusCondition(&rk.Status.Conditions, metav1.Condition{
		Type:               readyType,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: rk.Generation,
	})
	rk.Status.ObservedGeneration = rk.Generation
	_ = cl.Status().Update(ctx, rk)
}

func (r *AWSRootKeyReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		Named("operations-awsrootkey").
		For(&operationsv1alpha1.AWSRootKey{}).
		Complete(r)
}

func (r *AWSRootKeyReconciler) clusterClient(ctx context.Context, clusterName string) (client.Client, error) {
	cluster, err := r.Manager.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	return cluster.GetClient(), nil
}
