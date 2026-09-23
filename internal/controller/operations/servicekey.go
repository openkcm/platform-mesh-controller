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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

const (
	serviceKeyFinalizer = "operations.openkcm.io/servicekey-cleanup"
	// A service key sits one tier below the domain key it hangs off.
	serviceKeyKind       = "L3"
	serviceKeyNamePrefix = "servicekey:"
)

// ServiceKeyReconciler reconciles a ServiceKey object across KCP workspaces.
//
// In v0.7.0 the spec dropped the `activate` boolean. ServiceKey lifecycle
// now mirrors whatever OpenKCM reports — the reconciler creates the key,
// activates it once material is ready, and reflects the six-value lifecycle
// (PreActive|Active|Suspended|Deactivated|Compromised|Destroyed) back into
// status as OpenKCM transitions it.
type ServiceKeyReconciler struct {
	APIClient        KeyBackend
	Manager          mcmanager.Manager
	AccountNamespace string
}

// +kubebuilder:rbac:groups=operations.openkcm.io,resources=servicekeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=servicekeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=servicekeys/finalizers,verbs=update
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=domainkeys,verbs=get;list;create
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=tenants,verbs=get

// Reconcile handles ServiceKey create/update/delete events from KCP workspaces.
func (r *ServiceKeyReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("cluster", req.ClusterName)

	cl, err := r.clusterClient(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	sk := &operationsv1alpha1.ServiceKey{}
	if err := cl.Get(ctx, req.NamespacedName, sk); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !sk.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, cl, sk)
	}

	if controllerutil.AddFinalizer(sk, serviceKeyFinalizer) {
		if err := cl.Update(ctx, sk); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if handled, res, err := r.ensureParentDomainKeyLink(ctx, cl, sk); handled {
		return res, err
	}

	// Resolve the parent DomainKey by name (sibling in the same workspace).
	// Fetched up-front because the child≤parent invariant clamps the
	// effective desired lifecycle by the parent's actual state.
	dk := &operationsv1alpha1.DomainKey{}
	dkName := types.NamespacedName{Namespace: req.Namespace, Name: sk.Spec.DomainKeyRef}
	if err := cl.Get(ctx, dkName, dk); err != nil {
		logger.Info("Referenced DomainKey not found, will retry", "domainKey", sk.Spec.DomainKeyRef)
		r.setFailedCondition(ctx, cl, sk, "DomainKeyNotFound",
			fmt.Sprintf("Referenced DomainKey %q not found: %v", sk.Spec.DomainKeyRef, err))
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}
	domainKeyState := shared.LifecycleState("")
	if dk.Status.CryptoState != nil {
		domainKeyState = dk.Status.CryptoState.LifecycleState
	}

	// Child cannot be more active than parent: if DK is not Active, SK
	// effective desired collapses to Deactivated regardless of spec.
	effectiveDesired := effectiveDesiredLifecycle(sk.Spec.Lifecycle, domainKeyState)
	desired := desiredLifecycleState(effectiveDesired)
	if sk.Status.CryptoState != nil &&
		sk.Status.CryptoState.LifecycleState == desired &&
		sk.Status.ObservedGeneration == sk.Generation {
		return ctrl.Result{}, nil
	}

	// If we already have an existing ServiceKey (PreActive or Active), allow
	// the lifecycle reconcile path even when the parent DomainKey is not
	// Active — a deactivated parent should still let us deactivate this SK.
	if domainKeyState != shared.LifecycleActive && (sk.Status.CryptoState == nil || sk.Status.CryptoState.ID == "") {
		logger.Info("DomainKey not yet active, requeuing", "domainKey", dk.Name)
		r.setFailedCondition(ctx, cl, sk, "DomainKeyNotActive",
			fmt.Sprintf("DomainKey %q is not yet active", dk.Name))
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	// Step 1: new ServiceKey — register tenant + create L3 key.
	if sk.Status.CryptoState == nil || sk.Status.CryptoState.ID == "" {
		return r.createServiceKey(ctx, cl, sk, dk)
	}

	// Step 2: key exists — drive to Active. Lifecycle is owned by OpenKCM;
	// reflect whatever the backend reports and trigger activation while
	// the state is PreActive.
	keyID := sk.Status.CryptoState.ID
	keyResp, err := r.APIClient.GetKey(ctx, keyID)
	if err != nil {
		return ctrl.Result{}, err
	}
	if keyResp.ProcessingState != processingStateReady {
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	if keyResp.LifecycleState != "" {
		sk.Status.CryptoState.LifecycleState = shared.LifecycleState(keyResp.LifecycleState)
	}

	if sk.Status.CryptoState.LifecycleState == shared.LifecyclePreActive && desired == shared.LifecycleActive {
		activateResp, err := r.APIClient.ActivateKey(ctx, keyID)
		if err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("ServiceKey activated in OpenKCM", "keyID", keyID, "version", activateResp.Version)
		now := metav1.Now()
		sk.Status.CryptoState.LifecycleState = shared.LifecycleState(activateResp.LifecycleState)
		sk.Status.CryptoState.Version = activateResp.Version
		sk.Status.CryptoState.LastRotatedAt = &now
		sk.Status.OperationID = keyID
		sk.Status.ReconciliationStatus = &shared.ReconciliationStatus{
			Success:            true,
			Message:            "ServiceKey activated in OpenKCM.",
			InternalKeyID:      keyID,
			LastTransitionTime: &now,
		}
	}
	if sk.Status.CryptoState.LifecycleState == shared.LifecycleActive {
		r.setReady(sk, reasonKeyMaterialBound, "ServiceKey is at lifecycle "+string(sk.Status.CryptoState.LifecycleState))
	}

	// Lifecycle reconcile (Active ⇄ Deactivated). Effective desired is
	// clamped by the parent DomainKey above.
	newState, transitioned, err := reconcileLifecycle(
		ctx, r.APIClient, sk.Status.CryptoState.ID,
		sk.Status.CryptoState.LifecycleState, effectiveDesired,
	)
	if err != nil {
		r.setFailedCondition(ctx, cl, sk, "LifecycleTransitionFailed", err.Error())
		return ctrl.Result{}, err
	}
	if transitioned {
		now := metav1.Now()
		sk.Status.CryptoState.LifecycleState = newState
		sk.Status.CryptoState.LastRotatedAt = &now
		if newState == shared.LifecycleDeactivated {
			meta.SetStatusCondition(&sk.Status.Conditions, metav1.Condition{
				Type:               readyType,
				Status:             metav1.ConditionFalse,
				Reason:             reasonDeactivated,
				Message:            "ServiceKey deactivated per spec.lifecycle.",
				ObservedGeneration: sk.Generation,
			})
			if err := cascadeDeactivateServiceKey(ctx, cl, sk.Namespace, sk.Name); err != nil {
				return ctrl.Result{}, err
			}
		} else {
			r.setReady(sk, reasonKeyMaterialBound, "ServiceKey re-activated.")
		}
	}
	sk.Status.ObservedGeneration = sk.Generation
	if err := cl.Status().Update(ctx, sk); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *ServiceKeyReconciler) createServiceKey(ctx context.Context, cl client.Client, sk *operationsv1alpha1.ServiceKey, dk *operationsv1alpha1.DomainKey) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	account, err := resolveAccount(ctx, cl, r.AccountNamespace)
	if err != nil {
		r.setFailedCondition(ctx, cl, sk, "TenantResolutionFailed", err.Error())
		return ctrl.Result{}, err
	}
	tenantID, err := resolveTenantID(ctx, cl, account)
	if err != nil {
		r.setFailedCondition(ctx, cl, sk, "TenantResolutionFailed", err.Error())
		return ctrl.Result{}, err
	}
	if tenantID == "" {
		r.setFailedCondition(ctx, cl, sk, "AwaitingTenant", "Tenant is not registered in the backend yet; waiting.")
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	keyResp, err := r.APIClient.CreateKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: tenantID,
		Kind:     serviceKeyKind,
		Name:     serviceKeyOpenKCMName(sk),
		ParentID: dk.Status.CryptoState.ID,
	})
	if err != nil {
		r.setFailedCondition(ctx, cl, sk, "KeyCreateFailed", err.Error())
		return ctrl.Result{}, err
	}

	logger.Info("ServiceKey created in OpenKCM", "keyID", keyResp.ID, "parentKeyID", dk.Status.CryptoState.ID)
	sk.Status.CryptoState = &shared.CryptoState{
		ID:             keyResp.ID,
		LifecycleState: shared.LifecyclePreActive,
	}
	sk.Status.OperationID = keyResp.ID
	now := metav1.Now()
	sk.Status.ReconciliationStatus = &shared.ReconciliationStatus{
		Success:            true,
		Message:            "ServiceKey created in OpenKCM; awaiting activation.",
		InternalKeyID:      keyResp.ID,
		LastTransitionTime: &now,
	}
	meta.SetStatusCondition(&sk.Status.Conditions, metav1.Condition{
		Type:               readyType,
		Status:             metav1.ConditionFalse,
		Reason:             reasonProcess,
		Message:            "ServiceKey created, waiting for processing to complete",
		ObservedGeneration: sk.Generation,
	})
	sk.Status.ObservedGeneration = sk.Generation
	if err := cl.Status().Update(ctx, sk); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: pollInterval}, nil
}

func serviceKeyOpenKCMName(sk *operationsv1alpha1.ServiceKey) string {
	return serviceKeyNamePrefix + sk.Namespace + "." + sk.Name
}

func (r *ServiceKeyReconciler) ensureParentDomainKeyLink(ctx context.Context, cl client.Client, sk *operationsv1alpha1.ServiceKey) (bool, ctrl.Result, error) {
	if sk.Spec.DomainKeyRef != "" {
		dk := &operationsv1alpha1.DomainKey{}
		err := cl.Get(ctx, types.NamespacedName{Namespace: sk.Namespace, Name: sk.Spec.DomainKeyRef}, dk)
		if err == nil {
			return false, ctrl.Result{}, nil
		}
		if !apierrors.IsNotFound(err) {
			return true, ctrl.Result{}, err
		}
	}
	account, err := resolveAccount(ctx, cl, r.AccountNamespace)
	if err != nil {
		r.setFailedCondition(ctx, cl, sk, "TenantResolutionFailed", err.Error())
		return true, ctrl.Result{}, err
	}
	fallbackName := sk.Spec.DomainKeyRef
	if fallbackName == "" {
		fallbackName = account.Name
	}
	domainKeyName, err := ensureParentDomainKey(ctx, cl, sk.Namespace, fallbackName, account.Name)
	if err != nil {
		r.setFailedCondition(ctx, cl, sk, "DomainKeyCreateFailed", err.Error())
		return true, ctrl.Result{}, err
	}
	if sk.Spec.DomainKeyRef != domainKeyName {
		sk.Spec.DomainKeyRef = domainKeyName
		if err := cl.Update(ctx, sk); err != nil {
			return true, ctrl.Result{}, err
		}
		return true, ctrl.Result{}, nil
	}
	return true, ctrl.Result{RequeueAfter: pollInterval}, nil
}

func (r *ServiceKeyReconciler) setReady(sk *operationsv1alpha1.ServiceKey, reason, message string) {
	meta.SetStatusCondition(&sk.Status.Conditions, metav1.Condition{
		Type:               readyType,
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: sk.Generation,
	})
	meta.SetStatusCondition(&sk.Status.Conditions, metav1.Condition{
		Type:               providerSyncedType,
		Status:             metav1.ConditionTrue,
		Reason:             reasonSyncSuccessful,
		Message:            "ServiceKey synced to OpenKCM",
		ObservedGeneration: sk.Generation,
	})
}

func (r *ServiceKeyReconciler) handleDeletion(ctx context.Context, cl client.Client, sk *operationsv1alpha1.ServiceKey) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(sk, serviceKeyFinalizer) {
		return ctrl.Result{}, nil
	}

	if sk.Status.CryptoState != nil && sk.Status.CryptoState.ID != "" {
		err := r.APIClient.DeleteKey(ctx, sk.Status.CryptoState.ID)
		switch {
		case errors.Is(err, errors.ErrUnsupported):
			// TODO: Krypton has no DeleteKey RPC yet, so we skip upstream cleanup and just drop the finalizer.
			logger.Info("Backend cannot delete keys; skipping upstream cleanup", "keyID", sk.Status.CryptoState.ID)
		case err != nil:
			logger.Error(err, "Failed to delete ServiceKey in OpenKCM; will retry", "keyID", sk.Status.CryptoState.ID)
			return ctrl.Result{}, err
		default:
			logger.Info("ServiceKey deleted from OpenKCM", "keyID", sk.Status.CryptoState.ID)
		}
	}

	controllerutil.RemoveFinalizer(sk, serviceKeyFinalizer)
	if err := cl.Update(ctx, sk); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *ServiceKeyReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		Named("operations-servicekey").
		For(&operationsv1alpha1.ServiceKey{}).
		Complete(r)
}

func (r *ServiceKeyReconciler) clusterClient(ctx context.Context, clusterName string) (client.Client, error) {
	cluster, err := r.Manager.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	return cluster.GetClient(), nil
}

func (r *ServiceKeyReconciler) setFailedCondition(ctx context.Context, cl client.Client, sk *operationsv1alpha1.ServiceKey, reason, message string) {
	now := metav1.Now()
	sk.Status.ReconciliationStatus = &shared.ReconciliationStatus{
		Success:            false,
		Message:            message,
		LastTransitionTime: &now,
		Errors:             []string{reason + ": " + message},
	}
	meta.SetStatusCondition(&sk.Status.Conditions, metav1.Condition{
		Type:               readyType,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: sk.Generation,
	})
	sk.Status.ObservedGeneration = sk.Generation
	_ = cl.Status().Update(ctx, sk)
}
