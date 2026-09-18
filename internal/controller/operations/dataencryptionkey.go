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

const dataEncryptionKeyFinalizer = "operations.openkcm.io/dataencryptionkey-cleanup"

// DataEncryptionKeyReconciler reconciles a DataEncryptionKey (L4). Requires
// the parent ServiceKey to be at lifecycleState=Active before provisioning.
type DataEncryptionKeyReconciler struct {
	APIClient Backend
	Manager   mcmanager.Manager
}

// +kubebuilder:rbac:groups=operations.openkcm.io,resources=dataencryptionkeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=dataencryptionkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=dataencryptionkeys/finalizers,verbs=update
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=servicekeys,verbs=get;create

func (r *DataEncryptionKeyReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("cluster", req.ClusterName)

	cl, err := r.clusterClient(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	dek := &operationsv1alpha1.DataEncryptionKey{}
	if err := cl.Get(ctx, req.NamespacedName, dek); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !dek.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, cl, dek)
	}

	if controllerutil.AddFinalizer(dek, dataEncryptionKeyFinalizer) {
		if err := cl.Update(ctx, dek); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if handled, res, err := r.ensureParentServiceKeyLink(ctx, cl, dek); handled {
		return res, err
	}

	// Resolve the parent ServiceKey by name (sibling in same workspace).
	// Fetched up-front because the child≤parent invariant clamps the
	// effective desired lifecycle by the parent's actual state.
	sk := &operationsv1alpha1.ServiceKey{}
	skName := types.NamespacedName{Namespace: req.Namespace, Name: dek.Spec.ServiceKeyRef}
	if err := cl.Get(ctx, skName, sk); err != nil {
		logger.Info("Referenced ServiceKey not found, will retry", "serviceKey", dek.Spec.ServiceKeyRef)
		r.setFailed(ctx, cl, dek, "ServiceKeyNotFound",
			fmt.Sprintf("Referenced ServiceKey %q not found: %v", dek.Spec.ServiceKeyRef, err))
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	serviceKeyState := shared.LifecycleState("")
	if sk.Status.CryptoState != nil {
		serviceKeyState = sk.Status.CryptoState.LifecycleState
	}

	// Child cannot be more active than parent: if SK is not Active, DEK
	// effective desired collapses to Deactivated regardless of spec.
	effectiveDesired := effectiveDesiredLifecycle(dek.Spec.Lifecycle, serviceKeyState)
	desired := desiredLifecycleState(effectiveDesired)
	if dek.Status.CryptoState != nil &&
		dek.Status.CryptoState.LifecycleState == desired &&
		dek.Status.ObservedGeneration == dek.Generation {
		return ctrl.Result{}, nil
	}

	// Initial provisioning still requires an Active parent.
	if serviceKeyState != shared.LifecycleActive &&
		(dek.Status.CryptoState == nil || dek.Status.CryptoState.ID == "") {
		logger.Info("Parent ServiceKey not yet active, requeuing", "serviceKey", sk.Name)
		r.setFailed(ctx, cl, dek, "ServiceKeyNotActive",
			fmt.Sprintf("Parent ServiceKey %q is not Active (state=%q)", sk.Name, serviceKeyState))
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	if dek.Status.CryptoState == nil || dek.Status.CryptoState.ID == "" {
		accountName, err := resolveAccountName(ctx, cl)
		if err != nil {
			r.setFailed(ctx, cl, dek, "TenantResolutionFailed", err.Error())
			return ctrl.Result{}, err
		}
		tenantResp, err := r.APIClient.CreateTenant(ctx, openkcmapi.CreateTenantRequest{Name: accountName})
		if err != nil {
			r.setFailed(ctx, cl, dek, "TenantCreateFailed", err.Error())
			return ctrl.Result{}, err
		}

		var attrs map[string]string
		if dek.Spec.KMIP != nil {
			attrs = dek.Spec.KMIP.Attributes
		}
		resp, err := r.APIClient.CreateDEK(ctx, openkcmapi.CreateDEKRequest{
			TenantID:       tenantResp.ID,
			ServiceKeyID:   sk.Status.CryptoState.ID,
			Name:           dek.Name,
			KMIPAttributes: attrs,
		})
		if err != nil {
			r.setFailed(ctx, cl, dek, "DEKCreateFailed", err.Error())
			return ctrl.Result{}, err
		}
		logger.Info("DataEncryptionKey created", "dekID", resp.ID, "serviceKeyID", sk.Status.CryptoState.ID)

		now := metav1.Now()
		dek.Status.CryptoState = &shared.CryptoState{
			ID:             resp.ID,
			LifecycleState: shared.LifecyclePreActive,
		}
		dek.Status.OperationID = resp.ID
		dek.Status.ReconciliationStatus = &shared.ReconciliationStatus{
			Success:            true,
			Message:            "DEK created in OpenKCM; awaiting activation.",
			InternalKeyID:      resp.ID,
			LastTransitionTime: &now,
		}
		meta.SetStatusCondition(&dek.Status.Conditions, metav1.Condition{
			Type:               readyType,
			Status:             metav1.ConditionFalse,
			Reason:             reasonProcess,
			Message:            "DEK created, awaiting activation",
			ObservedGeneration: dek.Generation,
		})
		dek.Status.ObservedGeneration = dek.Generation
		if err := cl.Status().Update(ctx, dek); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	resp, err := r.APIClient.GetDEK(ctx, dek.Status.CryptoState.ID)
	if err != nil {
		return ctrl.Result{}, err
	}
	if resp.ProcessingState != processingStateReady {
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	if dek.Status.CryptoState.LifecycleState == shared.LifecyclePreActive {
		activateResp, err := r.APIClient.ActivateKey(ctx, dek.Status.CryptoState.ID)
		if err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("DataEncryptionKey activated", "dekID", dek.Status.CryptoState.ID)
		now := metav1.Now()
		dek.Status.CryptoState.LifecycleState = shared.LifecycleState(activateResp.LifecycleState)
		dek.Status.CryptoState.Version = activateResp.Version
		dek.Status.CryptoState.LastRotatedAt = &now
		dek.Status.ReconciliationStatus.LastTransitionTime = &now
		dek.Status.ReconciliationStatus.Message = "Data Encryption Key generated natively."

		meta.SetStatusCondition(&dek.Status.Conditions, metav1.Condition{
			Type:               readyType,
			Status:             metav1.ConditionTrue,
			Reason:             reasonKeyMaterialBound,
			Message:            "DataEncryptionKey activated",
			ObservedGeneration: dek.Generation,
		})
		meta.SetStatusCondition(&dek.Status.Conditions, metav1.Condition{
			Type:               providerSyncedType,
			Status:             metav1.ConditionTrue,
			Reason:             reasonSyncSuccessful,
			Message:            "Metadata fully replicated.",
			ObservedGeneration: dek.Generation,
		})
	}

	// Lifecycle reconcile (Active ⇄ Deactivated). DEK has no downstream
	// dependents so there is nothing to cascade further. Effective desired
	// is clamped by parent SK above.
	newState, transitioned, err := reconcileLifecycle(
		ctx, r.APIClient, dek.Status.CryptoState.ID,
		dek.Status.CryptoState.LifecycleState, effectiveDesired,
	)
	if err != nil {
		r.setFailed(ctx, cl, dek, "LifecycleTransitionFailed", err.Error())
		return ctrl.Result{}, err
	}
	if transitioned {
		now := metav1.Now()
		dek.Status.CryptoState.LifecycleState = newState
		dek.Status.CryptoState.LastRotatedAt = &now
		if newState == shared.LifecycleDeactivated {
			meta.SetStatusCondition(&dek.Status.Conditions, metav1.Condition{
				Type:               readyType,
				Status:             metav1.ConditionFalse,
				Reason:             reasonDeactivated,
				Message:            "DEK deactivated per spec.lifecycle.",
				ObservedGeneration: dek.Generation,
			})
		} else {
			meta.SetStatusCondition(&dek.Status.Conditions, metav1.Condition{
				Type:               readyType,
				Status:             metav1.ConditionTrue,
				Reason:             reasonKeyMaterialBound,
				Message:            "DEK re-activated.",
				ObservedGeneration: dek.Generation,
			})
		}
	}
	dek.Status.ObservedGeneration = dek.Generation
	if err := cl.Status().Update(ctx, dek); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *DataEncryptionKeyReconciler) ensureParentServiceKeyLink(ctx context.Context, cl client.Client, dek *operationsv1alpha1.DataEncryptionKey) (bool, ctrl.Result, error) {
	name := dek.Spec.ServiceKeyRef
	if name != "" {
		sk := &operationsv1alpha1.ServiceKey{}
		err := cl.Get(ctx, types.NamespacedName{Namespace: dek.Namespace, Name: name}, sk)
		if err == nil {
			return false, ctrl.Result{}, nil
		}
		if !apierrors.IsNotFound(err) {
			return true, ctrl.Result{}, err
		}
	}
	accountName, err := resolveAccountName(ctx, cl)
	if err != nil {
		r.setFailed(ctx, cl, dek, "TenantResolutionFailed", err.Error())
		return true, ctrl.Result{}, err
	}
	if name == "" {
		name = dek.Name
	}
	if err := ensureServiceKey(ctx, cl, dek.Namespace, name, accountName); err != nil {
		r.setFailed(ctx, cl, dek, "ServiceKeyCreateFailed", err.Error())
		return true, ctrl.Result{}, err
	}
	if dek.Spec.ServiceKeyRef != name {
		dek.Spec.ServiceKeyRef = name
		if err := cl.Update(ctx, dek); err != nil {
			return true, ctrl.Result{}, err
		}
		return true, ctrl.Result{}, nil
	}
	return true, ctrl.Result{RequeueAfter: pollInterval}, nil
}

func (r *DataEncryptionKeyReconciler) handleDeletion(ctx context.Context, cl client.Client, dek *operationsv1alpha1.DataEncryptionKey) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(dek, dataEncryptionKeyFinalizer) {
		return ctrl.Result{}, nil
	}
	if dek.Status.CryptoState != nil && dek.Status.CryptoState.ID != "" {
		if err := r.APIClient.DeleteDEK(ctx, dek.Status.CryptoState.ID); err != nil {
			logger.Error(err, "Failed to delete DEK in OpenKCM; will retry", "dekID", dek.Status.CryptoState.ID)
			return ctrl.Result{}, err
		}
	}
	controllerutil.RemoveFinalizer(dek, dataEncryptionKeyFinalizer)
	if err := cl.Update(ctx, dek); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *DataEncryptionKeyReconciler) setFailed(ctx context.Context, cl client.Client, dek *operationsv1alpha1.DataEncryptionKey, reason, message string) {
	now := metav1.Now()
	dek.Status.ReconciliationStatus = &shared.ReconciliationStatus{
		Success:            false,
		Message:            message,
		LastTransitionTime: &now,
		Errors:             []string{reason + ": " + message},
	}
	meta.SetStatusCondition(&dek.Status.Conditions, metav1.Condition{
		Type:               readyType,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: dek.Generation,
	})
	dek.Status.ObservedGeneration = dek.Generation
	_ = cl.Status().Update(ctx, dek)
}

func (r *DataEncryptionKeyReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		Named("operations-dataencryptionkey").
		For(&operationsv1alpha1.DataEncryptionKey{}).
		Complete(r)
}

func (r *DataEncryptionKeyReconciler) clusterClient(ctx context.Context, clusterName string) (client.Client, error) {
	cluster, err := r.Manager.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	return cluster.GetClient(), nil
}
