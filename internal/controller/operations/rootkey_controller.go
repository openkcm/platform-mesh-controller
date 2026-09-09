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
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/openkcm/openkcm-controller/api/shared"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

const (
	providerSyncedType = "ProviderSynced"

	reasonUpstreamAuthenticated = "UpstreamAuthenticated"
	reasonDeactivated           = "Deactivated"
	reasonSyncSuccessful        = "SyncSuccessful"

	// reasonTenantPending and reasonProviderRejected keep the two failure
	// modes apart on the object. An operator has to be able to tell "the
	// tenant has not reached the backend yet, wait" from "the backend looked
	// at this key and refused it", because only the second needs a human.
	reasonTenantPending    = "TenantPending"
	reasonProviderRejected = "ProviderRejected"

	rootKeyFinalizerPrefix = "operations.openkcm.io/"
	rootKeyFinalizerSuffix = "-cleanup"

	// identitySecretSuffix names the CA bundle the upstream identity is
	// presented with, e.g. aws-kms-ca.
	identitySecretSuffix    = "-kms-ca"
	identitySecretNamespace = "openkcm-system"
	identitySecretKey       = "ca.crt"
)

// RootKeyReconciler drives one L1 kind. All six providers differ only in the
// spec block naming the external key store, which RootKey
// hides behind ProviderName and ProviderConfig, so one loop serves them all.
//
// Before this there were three near-identical controllers for six kinds, and
// they had already drifted: only the OpenBao one set ProviderSynced, so
// anything gating on that condition saw AWS and Azure roots as never synced.
type RootKeyReconciler struct {
	apiClient        Backend
	manager          mcmanager.Manager
	accountNamespace string

	newRootKey func() RootKey
	kind       string
}

// NewRootKeyReconciler builds the reconciler for one L1 kind. The kind name is
// read off the type rather than passed in: it decides the finalizer name and
// the DomainKey reference match, and a value that disagreed with the object
// being watched would strand finalizers and break the deactivation cascade.
func NewRootKeyReconciler(
	apiClient Backend,
	accountNamespace string,
	newRootKey func() RootKey,
) *RootKeyReconciler {
	return &RootKeyReconciler{
		apiClient:        apiClient,
		accountNamespace: accountNamespace,
		newRootKey:       newRootKey,
		kind:             reflect.TypeOf(newRootKey()).Elem().Name(),
	}
}

// RootKeyReconcilers builds one reconciler per L1 kind.
func RootKeyReconcilers(apiClient Backend, accountNamespace string) []*RootKeyReconciler {
	reconcilers := make([]*RootKeyReconciler, 0, len(rootKeyKinds))
	for _, rk := range rootKeyKinds {
		reconcilers = append(reconcilers, NewRootKeyReconciler(apiClient, accountNamespace, rk.New))
	}
	return reconcilers
}

// +kubebuilder:rbac:groups=operations.openkcm.io,resources=awsrootkeys;azurerootkeys;openbaorootkeys;gcprootkeys;vaultrootkeys;hsmrootkeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=awsrootkeys/status;azurerootkeys/status;openbaorootkeys/status;gcprootkeys/status;vaultrootkeys/status;hsmrootkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=awsrootkeys/finalizers;azurerootkeys/finalizers;openbaorootkeys/finalizers;gcprootkeys/finalizers;vaultrootkeys/finalizers;hsmrootkeys/finalizers,verbs=update

// Kind is the L1 kind this reconciler watches, e.g. AWSRootKey.
func (r *RootKeyReconciler) Kind() string { return r.kind }

// finalizer keeps the per-kind names the three original controllers used.
// A single shared name would read better but would orphan the old finalizer on
// every object already in a cluster, and nothing would ever remove it.
func (r *RootKeyReconciler) finalizer() string {
	return rootKeyFinalizerPrefix + strings.ToLower(r.kind) + rootKeyFinalizerSuffix
}

// provider is the vendor part of the kind, used in operator-facing messages.
func (r *RootKeyReconciler) provider() string {
	return strings.TrimSuffix(r.kind, "RootKey")
}

func (r *RootKeyReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues("cluster", req.ClusterName, "kind", r.kind))

	cl, err := r.clusterClient(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	rk := r.newRootKey()
	if err := cl.Get(ctx, req.NamespacedName, rk); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !rk.GetDeletionTimestamp().IsZero() {
		return r.handleDeletion(ctx, cl, rk)
	}

	if controllerutil.AddFinalizer(rk, r.finalizer()) {
		if err := cl.Update(ctx, rk); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// generation is assigned by the API server and only moves when the spec
	// changes, so comparing it against the observed one is what keeps an
	// unchanged resource from calling the backend again.
	state := rk.GetCryptoState()
	if state != nil &&
		state.LifecycleState == desiredLifecycleState(rk.DesiredLifecycle()) &&
		rk.GetObservedGeneration() == rk.GetGeneration() {
		return ctrl.Result{}, ensureAutoDomainKeysForActiveAccountRoot(
			ctx, cl, rk.GetNamespace(), r.accountNamespace, state)
	}

	if state == nil || state.ID == "" {
		return r.register(ctx, cl, rk)
	}
	return r.synchronize(ctx, cl, rk)
}

// register creates the root key in the backend after making sure its tenant is
// there first.
func (r *RootKeyReconciler) register(
	ctx context.Context, cl client.Client, rk RootKey,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	accountName, err := resolveAccountName(ctx, cl)
	if err != nil {
		r.setFailed(ctx, cl, rk, "TenantResolutionFailed", err.Error())
		return ctrl.Result{}, err
	}
	if ref := rk.TenantNameRef(); ref != "" && ref != accountName {
		logger.Info("ignoring spec.tenantNameRef; using path-derived account",
			"specName", ref, "accountName", accountName)
	}

	// A key cannot hang off a tenant the backend has not registered yet. A
	// RootKey applied before its Tenant is ordinary, not an error, so requeue
	// and let it converge without anyone touching it.
	tenantResp, err := r.apiClient.CreateTenant(ctx, openkcmapi.CreateTenantRequest{Name: accountName})
	if err != nil {
		if openkcmapi.IsRetryable(err) {
			r.setPending(ctx, cl, rk, reasonTenantPending,
				"Tenant "+accountName+" is not registered yet; retrying.")
			return ctrl.Result{RequeueAfter: pollInterval}, nil
		}
		r.setFailed(ctx, cl, rk, "TenantCreateFailed", err.Error())
		return ctrl.Result{}, err
	}

	// ProviderConfig is passed to the backend and never logged: it names the
	// customer's key store, and once credential envelopes travel in it the
	// controller must stay a forwarder that cannot read them.
	resp, err := r.apiClient.CreateRootKey(ctx, openkcmapi.CreateRootKeyRequest{
		TenantID: tenantResp.ID,
		Provider: rk.ProviderName(),
		Name:     rk.GetName(),
		Config:   rk.ProviderConfig(),
	})
	if err != nil {
		if openkcmapi.IsRetryable(err) {
			r.setPending(ctx, cl, rk, reasonProcess, "Backend unavailable; retrying.")
			return ctrl.Result{RequeueAfter: pollInterval}, nil
		}
		r.setRejected(ctx, cl, rk, err)
		return ctrl.Result{}, err
	}
	logger.Info("root key registered", "rootKeyID", resp.ID, "provider", rk.ProviderName())

	now := metav1.Now()
	rk.SetCryptoState(&shared.CryptoState{
		ID:             resp.ID,
		LifecycleState: shared.LifecyclePreActive,
	})
	rk.SetOperationID(resp.ID)
	rk.SetReconciliationStatus(&shared.ReconciliationStatus{
		Success:            true,
		Message:            r.provider() + " root key registered; awaiting activation.",
		InternalKeyID:      resp.ID,
		LastTransitionTime: &now,
		IdentityInfo: &shared.IdentityInfo{
			Subject: "CN=" + accountName + " OU=Krypton, O=OpenKCM",
			CertificateSecretRef: &shared.SecretKeyReference{
				Name:      rk.ProviderName() + identitySecretSuffix,
				Namespace: identitySecretNamespace,
				Key:       identitySecretKey,
			},
		},
	})
	r.setCondition(rk, readyType, metav1.ConditionFalse, reasonProcess,
		"Root key registered, awaiting activation")
	rk.SetObservedGeneration(rk.GetGeneration())
	if err := cl.Status().Update(ctx, rk); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: pollInterval}, nil
}

// synchronize polls the backend and moves an already-registered key through its
// lifecycle.
func (r *RootKeyReconciler) synchronize(
	ctx context.Context, cl client.Client, rk RootKey,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	state := rk.GetCryptoState()

	resp, err := r.apiClient.GetRootKey(ctx, state.ID)
	if err != nil {
		// A backend that has forgotten the key is not a slow backend. Say so
		// on the object rather than retrying in silence.
		if openkcmapi.IsNotFound(err) {
			r.setFailed(ctx, cl, rk, "RootKeyMissing", "backend no longer knows root key "+state.ID)
			return ctrl.Result{}, err
		}
		if openkcmapi.IsRetryable(err) {
			return ctrl.Result{RequeueAfter: pollInterval}, nil
		}
		r.setRejected(ctx, cl, rk, err)
		return ctrl.Result{}, err
	}
	if resp.ProcessingState != processingStateReady {
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	// PreActive to Active happens once. After that Active and Deactivated are
	// driven by spec.lifecycle in reconcileLifecycle below.
	if state.LifecycleState == shared.LifecyclePreActive {
		activateResp, err := r.apiClient.ActivateKey(ctx, state.ID)
		if err != nil {
			if openkcmapi.IsRetryable(err) {
				return ctrl.Result{RequeueAfter: pollInterval}, nil
			}
			r.setRejected(ctx, cl, rk, err)
			return ctrl.Result{}, err
		}
		logger.Info("root key activated", "rootKeyID", state.ID)

		now := metav1.Now()
		state.LifecycleState = shared.LifecycleState(activateResp.LifecycleState)
		state.Version = activateResp.Version
		state.LastRotatedAt = &now
		if rs := rk.GetReconciliationStatus(); rs != nil {
			rs.LastTransitionTime = &now
			rs.Message = r.provider() + " root key bound and authenticated."
		}
		r.setCondition(rk, readyType, metav1.ConditionTrue, reasonUpstreamAuthenticated,
			"Successfully bound to "+r.provider())
		r.setCondition(rk, providerSyncedType, metav1.ConditionTrue, reasonSyncSuccessful,
			"Metadata fully replicated.")
	}

	newState, transitioned, err := reconcileLifecycle(
		ctx, r.apiClient, state.ID, state.LifecycleState, rk.DesiredLifecycle())
	if err != nil {
		r.setFailed(ctx, cl, rk, "LifecycleTransitionFailed", err.Error())
		return ctrl.Result{}, err
	}
	if transitioned {
		now := metav1.Now()
		state.LifecycleState = newState
		state.LastRotatedAt = &now
		if newState == shared.LifecycleDeactivated {
			r.setCondition(rk, readyType, metav1.ConditionFalse, reasonDeactivated,
				r.provider()+" root key deactivated per spec.lifecycle.")
			if err := cascadeDeactivateRootKey(ctx, cl, r.kind, rk.GetNamespace(), rk.GetName()); err != nil {
				return ctrl.Result{}, err
			}
		} else {
			r.setCondition(rk, readyType, metav1.ConditionTrue, reasonUpstreamAuthenticated,
				r.provider()+" root key re-activated.")
		}
	}

	rk.SetObservedGeneration(rk.GetGeneration())
	if err := cl.Status().Update(ctx, rk); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, ensureAutoDomainKeysForActiveAccountRoot(
		ctx, cl, rk.GetNamespace(), r.accountNamespace, state)
}

func (r *RootKeyReconciler) handleDeletion(
	ctx context.Context, cl client.Client, rk RootKey,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(rk, r.finalizer()) {
		return ctrl.Result{}, nil
	}
	if state := rk.GetCryptoState(); state != nil && state.ID != "" {
		if err := r.apiClient.DeleteRootKey(ctx, state.ID); err != nil {
			logger.Error(err, "failed to delete root key in backend; will retry", "rootKeyID", state.ID)
			return ctrl.Result{}, err
		}
	}
	controllerutil.RemoveFinalizer(rk, r.finalizer())
	if err := cl.Update(ctx, rk); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// setPending records that the key has not reached the backend yet. It leaves
// observedGeneration alone so the next pass still sees work to do.
func (r *RootKeyReconciler) setPending(
	ctx context.Context, cl client.Client, rk RootKey, reason, message string,
) {
	now := metav1.Now()
	rk.SetReconciliationStatus(&shared.ReconciliationStatus{
		Message:            message,
		LastTransitionTime: &now,
	})
	r.setCondition(rk, readyType, metav1.ConditionFalse, reason, message)
	_ = cl.Status().Update(ctx, rk)
}

// setRejected records that the backend looked at this key and refused it.
// ProviderSynced goes false as well, which is what separates a rejection from
// a key that is merely still on its way.
func (r *RootKeyReconciler) setRejected(
	ctx context.Context, cl client.Client, rk RootKey, cause error,
) {
	r.setCondition(rk, providerSyncedType, metav1.ConditionFalse, reasonProviderRejected, cause.Error())
	r.setFailed(ctx, cl, rk, reasonProviderRejected, cause.Error())
}

func (r *RootKeyReconciler) setFailed(
	ctx context.Context, cl client.Client, rk RootKey, reason, message string,
) {
	now := metav1.Now()
	rk.SetReconciliationStatus(&shared.ReconciliationStatus{
		Success:            false,
		Message:            message,
		LastTransitionTime: &now,
		Errors:             []string{reason + ": " + message},
	})
	r.setCondition(rk, readyType, metav1.ConditionFalse, reason, message)
	rk.SetObservedGeneration(rk.GetGeneration())
	_ = cl.Status().Update(ctx, rk)
}

func (r *RootKeyReconciler) setCondition(
	rk RootKey, condType string, status metav1.ConditionStatus, reason, message string,
) {
	meta.SetStatusCondition(rk.StatusConditions(), metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: rk.GetGeneration(),
	})
}

func (r *RootKeyReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		Named("operations-" + strings.ToLower(r.kind)).
		For(r.newRootKey()).
		Complete(r)
}

func (r *RootKeyReconciler) clusterClient(ctx context.Context, clusterName string) (client.Client, error) {
	cluster, err := r.manager.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	return cluster.GetClient(), nil
}
