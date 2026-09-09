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

const (
	tenantFinalizer = "operations.openkcm.io/tenant-cleanup"
	// tenantIDAnnotation persists the OpenKCM-assigned tenant ID across
	// status-subresource resets and operator restarts.
	tenantIDAnnotation = "operations.openkcm.io/openkcm-tenant-id"

	readyType     = "Ready"
	reasonProcess = "Processing"
	reasonCreated = "TenantCreated"
	reasonFailed  = "CreateFailed"
)

// TenantReconciler reconciles a Tenant object across KCP account workspaces.
//
// In v0.7.0 Tenant moved into the operations API group and lives at account
// scope (one per account, metadata.name == account name).
// The reconciler registers the tenant with the OpenKCM API and surfaces the
// new common status surface (operationId, reconciliationStatus).
type TenantReconciler struct {
	APIClient TenantBackend
	Manager   mcmanager.Manager
}

// +kubebuilder:rbac:groups=operations.openkcm.io,resources=tenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=tenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=tenants/finalizers,verbs=update

// Reconcile handles Tenant create/update/delete events from KCP workspaces.
func (r *TenantReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("cluster", req.ClusterName)

	cl, err := r.clusterClient(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	tenant := &operationsv1alpha1.Tenant{}
	if err := cl.Get(ctx, req.NamespacedName, tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !tenant.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, cl, tenant)
	}

	// Only promise cleanup the backend can actually deliver. Krypton has no
	// delete RPC, so holding a finalizer there would wedge the object in
	// Terminating forever and block deletion of its namespace behind it.
	if r.APIClient.SupportsTenantDeletion() {
		if controllerutil.AddFinalizer(tenant, tenantFinalizer) {
			if err := cl.Update(ctx, tenant); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	} else if controllerutil.RemoveFinalizer(tenant, tenantFinalizer) {
		// The backend lost the capability, most likely a switch from the mock
		// to Krypton. Release the object rather than stranding it.
		logger.Info("backend cannot delete tenants; releasing finalizer",
			"finalizer", tenantFinalizer)
		if err := cl.Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if meta.IsStatusConditionTrue(tenant.Status.Conditions, readyType) &&
		tenant.Status.ObservedGeneration == tenant.Generation {
		return ctrl.Result{}, nil
	}

	readyCond := meta.FindStatusCondition(tenant.Status.Conditions, readyType)

	// A stored tenant ID means the backend already registered this account, so
	// never call CreateTenant again: Krypton mints a fresh UUID on every call
	// and has no unique constraint on the name, so a retry silently produces a
	// second tenant and orphans the first.
	//
	// The annotation is written before the status, so it is the earlier and
	// therefore the authoritative record of "the backend has seen us".
	alreadyRegistered := tenant.Annotations[tenantIDAnnotation] != ""

	// Step 1: create path.
	if !alreadyRegistered && (readyCond == nil || readyCond.Reason != reasonProcess) {
		accountName, err := resolveAccountName(ctx, cl)
		if err != nil {
			r.setFailed(ctx, cl, tenant, "TenantResolutionFailed", err.Error())
			return ctrl.Result{}, err
		}
		if tenant.Name != accountName {
			logger.Info("ignoring Tenant metadata.name; using path-derived account",
				"objectName", tenant.Name, "accountName", accountName)
		}
		resp, err := r.APIClient.CreateTenant(ctx, openkcmapi.CreateTenantRequest{Name: accountName})
		if err != nil {
			r.setFailed(ctx, cl, tenant, reasonFailed, err.Error())
			return ctrl.Result{}, err
		}

		logger.Info("Tenant created in OpenKCM", "tenantID", resp.ID)
		if tenant.Annotations == nil {
			tenant.Annotations = map[string]string{}
		}
		tenant.Annotations[tenantIDAnnotation] = resp.ID
		if err := cl.Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}

		r.setProcessing(tenant, resp.ID, "Tenant provisioning in progress")
		if err := cl.Status().Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	// Step 2: poll readiness.
	tenantID := tenant.Annotations[tenantIDAnnotation]
	if tenantID == "" {
		r.setFailed(ctx, cl, tenant, reasonFailed, "missing tenant ID annotation, restarting")
		return ctrl.Result{Requeue: true}, nil
	}

	resp, err := r.APIClient.GetTenant(ctx, tenantID)
	if err != nil {
		return ctrl.Result{}, err
	}
	if resp.ProcessingState != processingStateReady {
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	logger.Info("Tenant ready in OpenKCM", "tenantID", tenantID)
	r.setReady(tenant, tenantID)
	if err := cl.Status().Update(ctx, tenant); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *TenantReconciler) handleDeletion(ctx context.Context, cl client.Client, tenant *operationsv1alpha1.Tenant) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(tenant, tenantFinalizer) {
		return ctrl.Result{}, nil
	}

	if tenantID := tenant.Annotations[tenantIDAnnotation]; tenantID != "" {
		if err := r.APIClient.DeleteTenant(ctx, tenantID); err != nil {
			logger.Error(err, "Failed to delete tenant in OpenKCM; will retry", "tenantID", tenantID)
			return ctrl.Result{}, err
		}
		logger.Info("Tenant deleted from OpenKCM", "tenantID", tenantID)
	}

	controllerutil.RemoveFinalizer(tenant, tenantFinalizer)
	if err := cl.Update(ctx, tenant); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *TenantReconciler) setProcessing(tenant *operationsv1alpha1.Tenant, tenantID, message string) {
	now := metav1.Now()
	tenant.Status.OperationID = tenantID
	tenant.Status.ReconciliationStatus = &shared.ReconciliationStatus{
		Success:            true,
		Message:            message,
		InternalKeyID:      tenantID,
		LastTransitionTime: &now,
	}
	meta.SetStatusCondition(&tenant.Status.Conditions, metav1.Condition{
		Type:               readyType,
		Status:             metav1.ConditionFalse,
		Reason:             reasonProcess,
		Message:            message,
		ObservedGeneration: tenant.Generation,
	})
	tenant.Status.ObservedGeneration = tenant.Generation
}

func (r *TenantReconciler) setReady(tenant *operationsv1alpha1.Tenant, tenantID string) {
	now := metav1.Now()
	tenant.Status.OperationID = tenantID
	tenant.Status.ReconciliationStatus = &shared.ReconciliationStatus{
		Success:            true,
		Message:            "Tenant registered in OpenKCM.",
		InternalKeyID:      tenantID,
		LastTransitionTime: &now,
	}
	meta.SetStatusCondition(&tenant.Status.Conditions, metav1.Condition{
		Type:               readyType,
		Status:             metav1.ConditionTrue,
		Reason:             reasonCreated,
		Message:            "Tenant registered in OpenKCM. ID: " + tenantID,
		ObservedGeneration: tenant.Generation,
	})
	tenant.Status.ObservedGeneration = tenant.Generation
}

func (r *TenantReconciler) setFailed(ctx context.Context, cl client.Client, tenant *operationsv1alpha1.Tenant, reason, message string) {
	now := metav1.Now()
	tenant.Status.ReconciliationStatus = &shared.ReconciliationStatus{
		Success:            false,
		Message:            message,
		LastTransitionTime: &now,
		Errors:             []string{reason + ": " + message},
	}
	meta.SetStatusCondition(&tenant.Status.Conditions, metav1.Condition{
		Type:               readyType,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: tenant.Generation,
	})
	tenant.Status.ObservedGeneration = tenant.Generation
	_ = cl.Status().Update(ctx, tenant)
}

func (r *TenantReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		Named("operations-tenant").
		For(&operationsv1alpha1.Tenant{}).
		Complete(r)
}

func (r *TenantReconciler) clusterClient(ctx context.Context, clusterName string) (client.Client, error) {
	cluster, err := r.Manager.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	return cluster.GetClient(), nil
}
