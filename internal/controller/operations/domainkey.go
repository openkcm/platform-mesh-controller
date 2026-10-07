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
	"time"

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
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	operationsv1alpha1 "github.com/openkcm/platform-mesh-controller/api/operations/v1alpha1"
	"github.com/openkcm/platform-mesh-controller/api/shared"
	"github.com/openkcm/platform-mesh-controller/internal/openkcmapi"
)

const (
	domainKeyFinalizer = "operations.openkcm.io/domainkey-cleanup"
	pollInterval       = 5 * time.Second
	domainKeyTypeTeam  = "Team"
	domainKeyKind      = "L2"
)

// DomainKeyReconciler reconciles a DomainKey object across KCP workspaces.
type DomainKeyReconciler struct {
	APIClient        KeyBackend
	Manager          mcmanager.Manager
	AccountNamespace string
}

// +kubebuilder:rbac:groups=operations.openkcm.io,resources=domainkeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=domainkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=domainkeys/finalizers,verbs=update
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=tenants,verbs=get

// Reconcile handles DomainKey create/update/delete events from KCP workspaces.
func (r *DomainKeyReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.clusterClient(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	dk := &operationsv1alpha1.DomainKey{}
	if err := cl.Get(ctx, req.NamespacedName, dk); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !dk.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, cl, dk)
	}

	// Singleton constraint: only one Namespace DomainKey per namespace. If a
	// Namespace DomainKey with an earlier creationTimestamp (or, on tie, a name
	// that sorts earlier) already exists, this one is rejected without
	// progressing. The earlier resource keeps reconciling normally.
	if earlier, err := r.findEarlierDomainKey(ctx, cl, dk); err != nil {
		return ctrl.Result{}, err
	} else if earlier != nil {
		r.setFailedCondition(ctx, cl, dk, "DomainKeyLimitExceeded",
			fmt.Sprintf("Only one Namespace DomainKey is allowed per namespace; %q already exists.", earlier.Name))
		return ctrl.Result{}, nil
	}

	if controllerutil.AddFinalizer(dk, domainKeyFinalizer) {
		if err := cl.Update(ctx, dk); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Child≤parent invariant: effective desired is clamped by primary L1
	// state. If L1 is not Active, DK must collapse to Deactivated even if
	// spec.lifecycle says otherwise. Structurally invalid references are
	// rejected before this clamp so bad edits cannot trigger deactivation.
	primaryL1State, err := r.primaryRootKeyLifecycle(ctx, cl, dk)
	if err != nil {
		r.setFailedCondition(ctx, cl, dk, "RootKeyResolutionFailed", err.Error())
		return ctrl.Result{}, err
	}
	effectiveDesired := effectiveDesiredLifecycle(dk.Spec.Lifecycle, primaryL1State)
	desired := desiredLifecycleState(effectiveDesired)
	if dk.Status.CryptoState != nil &&
		dk.Status.CryptoState.LifecycleState == desired &&
		dk.Status.ObservedGeneration == dk.Generation {
		return ctrl.Result{}, nil
	}

	// If the DomainKey already exists (has a Krypton key id) and is
	// transitioning to Deactivated (either by user or by L1 cascade), skip
	// the Active-L1 validation that resolvePrimaryRootKey enforces — we
	// don't need an Active parent to deactivate ourselves.
	skipResolveCheck := dk.Status.CryptoState != nil && dk.Status.CryptoState.ID != "" &&
		effectiveDesired == shared.DesiredLifecycleDeactivated
	if !skipResolveCheck {
		if err := r.resolvePrimaryRootKey(ctx, cl, dk); err != nil {
			r.setFailedCondition(ctx, cl, dk, "RootKeyResolutionFailed", err.Error())
			if result, ok := rootKeyResolutionFailureResult(err); ok {
				return result, nil
			}
			return ctrl.Result{}, err
		}
	}

	// Step 1: new DomainKey — register tenant + create L2 key.
	if dk.Status.CryptoState == nil || dk.Status.CryptoState.ID == "" {
		return r.createDomainKey(ctx, cl, dk)
	}

	// Step 2: PreActive → Active (one-time). May early-return if Krypton is
	// still processing; otherwise updates status in-place and falls through
	// to Step 3.
	if dk.Status.CryptoState.LifecycleState == shared.LifecyclePreActive {
		if result, done, err := r.activatePreActiveDomainKey(ctx, dk); err != nil || done {
			return result, err
		}
	}

	// Step 3: lifecycle reconcile (Active ⇄ Deactivated). Effective desired
	// is clamped by the primary L1 root key state above.
	newState, transitioned, err := reconcileLifecycle(
		ctx, r.APIClient, dk.Status.CryptoState.ID,
		dk.Status.CryptoState.LifecycleState, effectiveDesired,
	)
	if err != nil {
		r.setFailedCondition(ctx, cl, dk, "LifecycleTransitionFailed", err.Error())
		return ctrl.Result{}, err
	}
	if transitioned {
		now := metav1.Now()
		dk.Status.CryptoState.LifecycleState = newState
		dk.Status.CryptoState.LastRotatedAt = &now
		if newState == shared.LifecycleDeactivated {
			meta.SetStatusCondition(&dk.Status.Conditions, metav1.Condition{
				Type:               readyType,
				Status:             metav1.ConditionFalse,
				Reason:             reasonDeactivated,
				Message:            "DomainKey deactivated per spec.lifecycle.",
				ObservedGeneration: dk.Generation,
			})
			if err := cascadeDeactivateDomainKey(ctx, cl, dk.Namespace, dk.Name); err != nil {
				return ctrl.Result{}, err
			}
		} else {
			meta.SetStatusCondition(&dk.Status.Conditions, metav1.Condition{
				Type:               readyType,
				Status:             metav1.ConditionTrue,
				Reason:             reasonKeyMaterialBound,
				Message:            "DomainKey re-activated.",
				ObservedGeneration: dk.Generation,
			})
		}
	}
	dk.Status.ObservedGeneration = dk.Generation
	if err := cl.Status().Update(ctx, dk); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// resolveTenantID reads the backend tenant id recorded on the account Tenant.
// An empty result means the tenant is not registered yet.
func resolveTenantID(ctx context.Context, cl client.Client, account accountIdentity) (string, error) {
	tenant := &operationsv1alpha1.Tenant{}
	key := types.NamespacedName{Namespace: account.Namespace, Name: account.Name}
	if err := cl.Get(ctx, key, tenant); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	return tenant.Annotations[tenantIDAnnotation], nil
}

// createDomainKey is Step 1 of Reconcile: register the tenant and create
// the L2 key in OpenKCM. Returns the result to bubble up to Reconcile.
func (r *DomainKeyReconciler) createDomainKey(ctx context.Context, cl client.Client, dk *operationsv1alpha1.DomainKey) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	account, err := resolveAccount(ctx, cl, r.AccountNamespace)
	if err != nil {
		r.setFailedCondition(ctx, cl, dk, "TenantResolutionFailed", err.Error())
		return ctrl.Result{}, err
	}
	if dk.Spec.TenantNameRef != "" && dk.Spec.TenantNameRef != account.Name {
		logger.Info("ignoring spec.tenantNameRef; using path-derived account",
			"specName", dk.Spec.TenantNameRef, "accountName", account.Name)
	}

	tenantID, err := resolveTenantID(ctx, cl, account)
	if err != nil {
		r.setFailedCondition(ctx, cl, dk, "TenantResolutionFailed", err.Error())
		return ctrl.Result{}, err
	}
	if tenantID == "" {
		r.setFailedCondition(ctx, cl, dk, "AwaitingTenant",
			"Tenant is not registered in the backend yet; waiting.")
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	keyResp, err := r.APIClient.CreateKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: tenantID,
		Kind:     domainKeyKind,
		Name:     domainKeyOpenKCMName(dk),
	})
	if err != nil {
		r.setFailedCondition(ctx, cl, dk, "KeyCreateFailed", err.Error())
		return ctrl.Result{}, err
	}

	logger.Info("DomainKey created in OpenKCM", "keyID", keyResp.ID, "tenantID", tenantID)
	now := metav1.Now()
	dk.Status.CryptoState = &shared.CryptoState{
		ID:             keyResp.ID,
		LifecycleState: shared.LifecyclePreActive,
	}
	dk.Status.OperationID = keyResp.ID
	dk.Status.ReconciliationStatus = &shared.ReconciliationStatus{
		Success:            true,
		Message:            "DomainKey created in OpenKCM; awaiting activation.",
		InternalKeyID:      keyResp.ID,
		LastTransitionTime: &now,
	}
	meta.SetStatusCondition(&dk.Status.Conditions, metav1.Condition{
		Type:               readyType,
		Status:             metav1.ConditionFalse,
		Reason:             reasonProcess,
		Message:            "Key created, waiting for processing to complete",
		ObservedGeneration: dk.Generation,
	})
	dk.Status.ObservedGeneration = dk.Generation
	if err := cl.Status().Update(ctx, dk); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: pollInterval}, nil
}

func domainKeyOpenKCMName(dk *operationsv1alpha1.DomainKey) string {
	if dk.Namespace == "" {
		return dk.Name
	}
	return dk.Namespace + "." + dk.Name
}

// activatePreActiveDomainKey is Step 2: drive PreActive → Active once
// Krypton reports the key is ready. Returns (result, done, err) — when
// done=true the caller should return immediately; otherwise the function
// has updated the in-memory status and the caller should continue with
// Step 3 (lifecycle reconcile).
func (r *DomainKeyReconciler) activatePreActiveDomainKey(ctx context.Context, dk *operationsv1alpha1.DomainKey) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)
	keyID := dk.Status.CryptoState.ID

	keyResp, err := r.APIClient.GetKey(ctx, keyID)
	if err != nil {
		return ctrl.Result{}, true, err
	}
	if keyResp.ProcessingState != processingStateReady {
		return ctrl.Result{RequeueAfter: pollInterval}, true, nil
	}

	activateResp, err := r.APIClient.ActivateKey(ctx, keyID)
	if err != nil {
		return ctrl.Result{}, true, err
	}

	logger.Info("DomainKey activated in OpenKCM", "keyID", keyID)
	now := metav1.Now()
	dk.Status.CryptoState.LifecycleState = shared.LifecycleState(activateResp.LifecycleState)
	dk.Status.CryptoState.Version = activateResp.Version
	dk.Status.CryptoState.LastRotatedAt = &now
	dk.Status.OperationID = keyID
	dk.Status.ReconciliationStatus = &shared.ReconciliationStatus{
		Success:            true,
		Message:            "DomainKey activated in OpenKCM.",
		InternalKeyID:      keyID,
		LastTransitionTime: &now,
	}
	meta.SetStatusCondition(&dk.Status.Conditions, metav1.Condition{
		Type:               readyType,
		Status:             metav1.ConditionTrue,
		Reason:             reasonKeyMaterialBound,
		Message:            "DomainKey activated in OpenKCM. ID: " + keyID,
		ObservedGeneration: dk.Generation,
	})
	meta.SetStatusCondition(&dk.Status.Conditions, metav1.Condition{
		Type:               providerSyncedType,
		Status:             metav1.ConditionTrue,
		Reason:             reasonSyncSuccessful,
		Message:            "DomainKey synced to OpenKCM",
		ObservedGeneration: dk.Generation,
	})
	return ctrl.Result{}, false, nil
}

type rootKeyPendingError struct {
	err error
}

func (e *rootKeyPendingError) Error() string {
	return e.err.Error()
}

func (e *rootKeyPendingError) Unwrap() error {
	return e.err
}

func rootKeyResolutionFailureResult(err error) (ctrl.Result, bool) {
	if _, ok := errors.AsType[*rootKeyPendingError](err); !ok {
		return ctrl.Result{}, false
	}
	return ctrl.Result{RequeueAfter: pollInterval}, true
}

// primaryRootKeyLifecycle reads the lifecycle state of the L1 referenced by
// dk.spec.primaryRootKeyRef without erroring when the L1 is non-Active.
// Used to clamp the DomainKey's effective desired lifecycle by the parent's
// state (child≤parent invariant). Returns empty string when the reference is
// empty or points at a missing root key; callers should treat empty as
// non-Active. Invalid reference shape is returned as an error so bad edits do
// not look like a non-Active parent and accidentally deactivate the DomainKey.
func (r *DomainKeyReconciler) primaryRootKeyLifecycle(ctx context.Context, cl client.Client, dk *operationsv1alpha1.DomainKey) (shared.LifecycleState, error) {
	ref := dk.Spec.PrimaryRootKeyRef
	if ref == nil || ref.Name == "" {
		// No linked root: the domain key hangs off Krypton's default root,
		// which is always active, so nothing clamps its lifecycle.
		return shared.LifecycleActive, nil
	}
	gvr := rootKeyGVK(ref.Kind)
	if gvr == nil {
		return "", fmt.Errorf("unsupported primaryRootKeyRef.kind %q", ref.Kind)
	}
	obj := gvr.newEmpty()
	refNamespace, ok := allowedRootKeyReferenceNamespace(ref, dk.Namespace, r.AccountNamespace)
	if !ok {
		accountNamespace := defaultAccountNamespace(r.AccountNamespace)
		return "", fmt.Errorf("primaryRootKeyRef namespace %q is not allowed; use same namespace %q or account namespace %q",
			refNamespace, dk.Namespace, accountNamespace)
	}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: refNamespace, Name: ref.Name}, obj); err != nil {
		return "", nil
	}
	cs := rootKeyCryptoState(obj)
	if cs == nil {
		return "", nil
	}
	return cs.LifecycleState, nil
}

// resolvePrimaryRootKey loads the L1 referent named by spec.primaryRootKeyRef
// and fails reconciliation if it is invalid. Missing or inactive root keys are
// pending dependencies and should keep the DomainKey on the poll loop.
func (r *DomainKeyReconciler) resolvePrimaryRootKey(ctx context.Context, cl client.Client, dk *operationsv1alpha1.DomainKey) error {
	ref := dk.Spec.PrimaryRootKeyRef
	if ref == nil || ref.Name == "" {
		// No linked root: the domain key uses Krypton's default root, which
		// the backend seeds. There is nothing to resolve here.
		return nil
	}

	gvr := rootKeyGVK(ref.Kind)
	if gvr == nil {
		return fmt.Errorf("unsupported primaryRootKeyRef.kind %q", ref.Kind)
	}

	obj := gvr.newEmpty()
	refNamespace, ok := allowedRootKeyReferenceNamespace(ref, dk.Namespace, r.AccountNamespace)
	if !ok {
		accountNamespace := defaultAccountNamespace(r.AccountNamespace)
		return fmt.Errorf("primaryRootKeyRef namespace %q is not allowed; use same namespace %q or account namespace %q",
			refNamespace, dk.Namespace, accountNamespace)
	}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: refNamespace, Name: ref.Name}, obj); err != nil {
		err = fmt.Errorf("resolving primaryRootKeyRef %s/%s/%s: %w", ref.Kind, refNamespace, ref.Name, err)
		if apierrors.IsNotFound(err) {
			return &rootKeyPendingError{err: err}
		}
		return err
	}
	cryptoState := rootKeyCryptoState(obj)
	if cryptoState == nil || cryptoState.LifecycleState != shared.LifecycleActive {
		return &rootKeyPendingError{
			err: fmt.Errorf("primaryRootKeyRef %s/%s/%s is not Active", ref.Kind, refNamespace, ref.Name),
		}
	}
	return nil
}

func (r *DomainKeyReconciler) handleDeletion(ctx context.Context, cl client.Client, dk *operationsv1alpha1.DomainKey) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(dk, domainKeyFinalizer) {
		return ctrl.Result{}, nil
	}

	if dk.Status.CryptoState != nil && dk.Status.CryptoState.ID != "" {
		err := r.APIClient.DeleteKey(ctx, dk.Status.CryptoState.ID)
		switch {
		case errors.Is(err, errors.ErrUnsupported):
			// TODO: Krypton has no DeleteKey RPC yet, so we skip upstream cleanup and just drop the finalizer.
			logger.Info("Backend cannot delete keys; skipping upstream cleanup", "keyID", dk.Status.CryptoState.ID)
		case err != nil:
			logger.Error(err, "Failed to delete DomainKey in OpenKCM; will retry", "keyID", dk.Status.CryptoState.ID)
			return ctrl.Result{}, err
		default:
			logger.Info("DomainKey deleted from OpenKCM", "keyID", dk.Status.CryptoState.ID)
		}
	}

	controllerutil.RemoveFinalizer(dk, domainKeyFinalizer)
	if err := cl.Update(ctx, dk); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *DomainKeyReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		Named("operations-domainkey").
		For(&operationsv1alpha1.DomainKey{}).
		Complete(r)
}

func (r *DomainKeyReconciler) clusterClient(ctx context.Context, clusterName multicluster.ClusterName) (client.Client, error) {
	cluster, err := r.Manager.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	return cluster.GetClient(), nil
}

// findEarlierDomainKey returns the Namespace DomainKey in the same namespace that
// should "win" the singleton slot — the one created earliest, with the
// lexicographically-smaller name as a deterministic tiebreaker. Returns
// (nil, nil) if dk itself is the winner or is not a Namespace DomainKey.
// DomainKeys marked for deletion are ignored.
func (r *DomainKeyReconciler) findEarlierDomainKey(
	ctx context.Context,
	cl client.Client,
	dk *operationsv1alpha1.DomainKey,
) (*operationsv1alpha1.DomainKey, error) {
	if !isNamespaceDomainKey(dk) {
		return nil, nil
	}
	others := &operationsv1alpha1.DomainKeyList{}
	if err := cl.List(ctx, others, client.InNamespace(dk.Namespace)); err != nil {
		return nil, err
	}
	for i := range others.Items {
		other := &others.Items[i]
		if other.Name == dk.Name || !isNamespaceDomainKey(other) {
			continue
		}
		if !other.DeletionTimestamp.IsZero() {
			continue
		}
		switch {
		case other.CreationTimestamp.Before(&dk.CreationTimestamp):
			return other, nil
		case dk.CreationTimestamp.Before(&other.CreationTimestamp):
			continue
		default:
			// Same creationTimestamp — deterministic tiebreaker by name.
			if other.Name < dk.Name {
				return other, nil
			}
		}
	}
	return nil, nil
}

func isNamespaceDomainKey(dk *operationsv1alpha1.DomainKey) bool {
	// Scope is empty when the served schema predates the field.
	return dk.Spec.Scope == "" || dk.Spec.Scope == operationsv1alpha1.DomainKeyScopeNamespace
}

func (r *DomainKeyReconciler) setFailedCondition(ctx context.Context, cl client.Client, dk *operationsv1alpha1.DomainKey, reason, message string) {
	now := metav1.Now()
	dk.Status.ReconciliationStatus = &shared.ReconciliationStatus{
		Success:            false,
		Message:            message,
		LastTransitionTime: &now,
		Errors:             []string{reason + ": " + message},
	}
	meta.SetStatusCondition(&dk.Status.Conditions, metav1.Condition{
		Type:               readyType,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: dk.Generation,
	})
	dk.Status.ObservedGeneration = dk.Generation
	_ = cl.Status().Update(ctx, dk)
}
