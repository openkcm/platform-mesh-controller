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

const azureRootKeyFinalizer = "operations.openkcm.io/azurerootkey-cleanup"

// AzureRootKeyReconciler reconciles an AzureRootKey (L1) via the mock API.
// Real Azure Key Vault / federated identity wiring is a follow-up.
type AzureRootKeyReconciler struct {
	APIClient        *openkcmapi.Client
	Manager          mcmanager.Manager
	AccountNamespace string
}

// +kubebuilder:rbac:groups=operations.openkcm.io,resources=azurerootkeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=azurerootkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=azurerootkeys/finalizers,verbs=update

func (r *AzureRootKeyReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("cluster", req.ClusterName)

	cl, err := r.clusterClient(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	rk := &operationsv1alpha1.AzureRootKey{}
	if err := cl.Get(ctx, req.NamespacedName, rk); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !rk.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, cl, rk)
	}

	if controllerutil.AddFinalizer(rk, azureRootKeyFinalizer) {
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
		accountName, err := resolveAccountName(ctx, cl)
		if err != nil {
			r.setFailed(ctx, cl, rk, "TenantResolutionFailed", err.Error())
			return ctrl.Result{}, err
		}
		if rk.Spec.TenantNameRef != "" && rk.Spec.TenantNameRef != accountName {
			logger.Info("ignoring spec.tenantNameRef; using path-derived account",
				"specName", rk.Spec.TenantNameRef, "accountName", accountName)
		}
		tenantResp, err := r.APIClient.CreateTenant(ctx, openkcmapi.CreateTenantRequest{Name: accountName})
		if err != nil {
			r.setFailed(ctx, cl, rk, "TenantCreateFailed", err.Error())
			return ctrl.Result{}, err
		}

		resp, err := r.APIClient.CreateRootKey(ctx, openkcmapi.CreateRootKeyRequest{
			TenantID: tenantResp.ID,
			Provider: "azure",
			Name:     rk.Name,
			Config: map[string]string{
				"vaultUrl":   rk.Spec.VaultURL,
				"keyName":    rk.Spec.KeyName,
				"keyVersion": rk.Spec.KeyVersion,
				"tenantId":   rk.Spec.FederatedIdentity.TenantID,
				"clientId":   rk.Spec.FederatedIdentity.ClientID,
			},
		})
		if err != nil {
			r.setFailed(ctx, cl, rk, "RootKeyCreateFailed", err.Error())
			return ctrl.Result{}, err
		}
		logger.Info("AzureRootKey registered", "rootKeyID", resp.ID)

		now := metav1.Now()
		rk.Status.CryptoState = &shared.CryptoState{
			ID:             resp.ID,
			LifecycleState: shared.LifecyclePreActive,
		}
		rk.Status.OperationID = resp.ID
		rk.Status.ReconciliationStatus = &shared.ReconciliationStatus{
			Success:            true,
			Message:            "Azure root key registered; awaiting activation.",
			InternalKeyID:      resp.ID,
			LastTransitionTime: &now,
			IdentityInfo: &shared.IdentityInfo{
				Subject: "CN=" + accountName + " OU=Krypton, O=OpenKCM",
				CertificateSecretRef: &shared.SecretKeyReference{
					Name:      "azure-kms-ca",
					Namespace: "openkcm-system",
					Key:       "ca.crt",
				},
			},
		}
		meta.SetStatusCondition(&rk.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "Processing",
			Message:            "Root key registered, awaiting activation",
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

	if rk.Status.CryptoState.LifecycleState == shared.LifecyclePreActive {
		activateResp, err := r.APIClient.ActivateKey(ctx, rk.Status.CryptoState.ID)
		if err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("AzureRootKey activated", "rootKeyID", rk.Status.CryptoState.ID)
		now := metav1.Now()
		rk.Status.CryptoState.LifecycleState = shared.LifecycleState(activateResp.LifecycleState)
		rk.Status.CryptoState.Version = activateResp.Version
		rk.Status.CryptoState.LastRotatedAt = &now
		rk.Status.ReconciliationStatus.LastTransitionTime = &now
		rk.Status.ReconciliationStatus.Message = "Azure Key Vault bound and authenticated."
		meta.SetStatusCondition(&rk.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "UpstreamAuthenticated",
			Message:            "Successfully bound to Azure Key Vault",
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
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "Deactivated",
				Message:            "Azure root key deactivated per spec.lifecycle.",
				ObservedGeneration: rk.Generation,
			})
			if err := cascadeDeactivateRootKey(ctx, cl, "AzureRootKey", rk.Namespace, rk.Name); err != nil {
				return ctrl.Result{}, err
			}
		} else {
			meta.SetStatusCondition(&rk.Status.Conditions, metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				Reason:             "UpstreamAuthenticated",
				Message:            "Azure root key re-activated.",
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

func (r *AzureRootKeyReconciler) handleDeletion(ctx context.Context, cl client.Client, rk *operationsv1alpha1.AzureRootKey) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(rk, azureRootKeyFinalizer) {
		return ctrl.Result{}, nil
	}
	if rk.Status.CryptoState != nil && rk.Status.CryptoState.ID != "" {
		if err := r.APIClient.DeleteRootKey(ctx, rk.Status.CryptoState.ID); err != nil {
			logger.Error(err, "Failed to delete AzureRootKey in OpenKCM; will retry", "rootKeyID", rk.Status.CryptoState.ID)
			return ctrl.Result{}, err
		}
	}
	controllerutil.RemoveFinalizer(rk, azureRootKeyFinalizer)
	if err := cl.Update(ctx, rk); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *AzureRootKeyReconciler) setFailed(ctx context.Context, cl client.Client, rk *operationsv1alpha1.AzureRootKey, reason, message string) {
	now := metav1.Now()
	rk.Status.ReconciliationStatus = &shared.ReconciliationStatus{
		Success:            false,
		Message:            message,
		LastTransitionTime: &now,
		Errors:             []string{reason + ": " + message},
	}
	meta.SetStatusCondition(&rk.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: rk.Generation,
	})
	rk.Status.ObservedGeneration = rk.Generation
	_ = cl.Status().Update(ctx, rk)
}

func (r *AzureRootKeyReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		Named("operations-azurerootkey").
		For(&operationsv1alpha1.AzureRootKey{}).
		Complete(r)
}

func (r *AzureRootKeyReconciler) clusterClient(ctx context.Context, clusterName string) (client.Client, error) {
	cluster, err := r.Manager.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	return cluster.GetClient(), nil
}
