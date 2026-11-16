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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	kcpapisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"

	operationsv1alpha1 "github.com/openkcm/platform-mesh-controller/api/operations/v1alpha1"
)

// AccountBootstrapReconciler watches APIBinding events through the
// operations.openkcm.io APIExport virtual workspace and ensures each account
// workspace that binds OpenKCM gets the default Tenant CR plus the
// single Encryption Domain (L2 DomainKey).
//
// v0.9.1 revisits the v0.7.3 "Tenant-only" choice. Since v0.8.5 enforces L2
// singleton and v0.9.1 makes primaryRootKeyRef optional, the bootstrap can
// safely create the singleton DomainKey without a primary L1 attached - the
// DomainKey reconciler will sit in Ready=False/AwaitingPrimaryRootKey until
// the user registers a Root Key and edits the DomainKey to link it (via the
// Edit affordance in the EncryptionDomainCard).
type AccountBootstrapReconciler struct {
	Manager mcmanager.Manager

	// Defaults applied when minting a new Tenant CR. Optional - if all
	// empty, the Tenant ships with no OIDCProvider block (the v0.7.0
	// schema makes oidcProvider optional).
	DefaultRegion        string
	DefaultOIDCIssuer    string
	DefaultOIDCJWKSURI   string
	DefaultOIDCAudiences []string

	// TenantNamespace is the namespace the Tenant CR is created in.
	// Defaults to "default".
	TenantNamespace string
}

const (
	defaultTenantNamespace  = "default"
	operationsAPIExportName = "operations.openkcm.io"
	bootstrapAnnotation     = "operations.openkcm.io/bootstrap"
	bootstrapAnnotationAuto = "auto"
)

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=create
// +kubebuilder:rbac:groups=apis.kcp.io,resources=apibindings,verbs=get;list;watch
// +kubebuilder:rbac:groups=core.kcp.io,resources=logicalclusters,verbs=get
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=tenants,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=domainkeys,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=operations.openkcm.io,resources=l1keyaccesses,verbs=get;create

// Reconcile fires when an account workspace creates/binds operations.openkcm.io.
func (r *AccountBootstrapReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("cluster", req.ClusterName)

	cl, err := r.clusterClient(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	binding := &kcpapisv1alpha2.APIBinding{}
	if err := cl.Get(ctx, req.NamespacedName, binding); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !isOperationsAPIBinding(binding) {
		logger.V(1).Info("APIBinding is not OpenKCM operations export; skipping", "binding", binding.Name)
		return ctrl.Result{}, nil
	}
	// Deleting bindings drag along terminating CRDs and torn-down
	// LogicalClusters; trying to create Tenant/DomainKey against them only
	// produces "CRD is terminating" / "LogicalCluster not found" log noise.
	// Nothing useful can happen here.
	if !binding.DeletionTimestamp.IsZero() {
		logger.V(1).Info("APIBinding is being deleted; skipping", "binding", binding.Name)
		return ctrl.Result{}, nil
	}
	if binding.Status.Phase != kcpapisv1alpha2.APIBindingPhaseBound {
		logger.V(1).Info("OpenKCM APIBinding is not Bound yet; waiting", "binding", binding.Name, "phase", binding.Status.Phase)
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	path, err := workspacePath(ctx, cl)
	if err != nil {
		// Workspaces that are mid-deletion lose their LogicalCluster before
		// their APIBindings finish draining. Treat NotFound as "skip", not
		// "error" - there's nothing to bootstrap in a workspace that's
		// going away.
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("LogicalCluster missing; workspace is likely terminating, skipping", "binding", binding.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	accountName, ok := accountNameFromPath(path)
	if !ok {
		logger.V(1).Info("cluster is not an account workspace; skipping", "path", path)
		return ctrl.Result{}, nil
	}

	namespace := defaultAccountNamespace(r.TenantNamespace)

	// Best-effort ensure the destination namespace exists. The openkcm
	// provider-syncagent typically lacks cluster-scope rights to create
	// namespaces in fresh account workspaces; that's OK as long as
	// "default" already exists (which is the common case under KCP).
	// Ignore AlreadyExists and Forbidden - if the namespace genuinely
	// doesn't exist, the subsequent Tenant/DomainKey create will return
	// a clear error.
	ns := &corev1.Namespace{Name: namespace}
	if err := cl.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) && !apierrors.IsForbidden(err) {
		return ctrl.Result{}, err
	}

	if err := r.ensureTenant(ctx, cl, namespace, accountName); err != nil {
		return ctrl.Result{}, err
	}
	if err := ensureL1KeyAccess(ctx, cl); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureDomainKey(ctx, cl, namespace, accountName); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("bootstrapped OpenKCM account defaults",
		"tenant", accountName, "domainKey", accountName, "path", path)
	return ctrl.Result{}, nil
}

func ensureL1KeyAccess(ctx context.Context, cl client.Client) error {
	marker := &operationsv1alpha1.L1KeyAccess{}
	err := cl.Get(ctx, types.NamespacedName{Name: operationsv1alpha1.L1KeyAccessName}, marker)
	if !apierrors.IsNotFound(err) {
		return err
	}
	marker.Name = operationsv1alpha1.L1KeyAccessName
	return client.IgnoreAlreadyExists(cl.Create(ctx, marker))
}

func (r *AccountBootstrapReconciler) ensureTenant(ctx context.Context, cl client.Client, namespace, accountName string) error {
	existing := &operationsv1alpha1.Tenant{}
	err := cl.Get(ctx, types.NamespacedName{Namespace: namespace, Name: accountName}, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	tenant := &operationsv1alpha1.Tenant{
		Name:      accountName,
		Namespace: namespace,
		Annotations: map[string]string{
			bootstrapAnnotation: bootstrapAnnotationAuto,
		},
		Spec: operationsv1alpha1.TenantSpec{
			Region: r.DefaultRegion,
		},
	}
	if r.DefaultOIDCIssuer != "" || r.DefaultOIDCJWKSURI != "" || len(r.DefaultOIDCAudiences) > 0 {
		tenant.Spec.OIDCProvider = &operationsv1alpha1.OIDCProvider{
			Issuer:    r.DefaultOIDCIssuer,
			JWKSURI:   r.DefaultOIDCJWKSURI,
			Audiences: append([]string{}, r.DefaultOIDCAudiences...),
		}
	}
	if err := cl.Create(ctx, tenant); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	return nil
}

// ensureDomainKey idempotently creates the singleton Encryption Domain
// (L2 DomainKey) for this account. The auto-created DomainKey ships
// with no primaryRootKeyRef - DomainKeyReconciler sits in
// Ready=False/AwaitingPrimaryRootKey until the user links a Root Key
// via the Edit affordance in the OpenKCM UI.
func (r *AccountBootstrapReconciler) ensureDomainKey(ctx context.Context, cl client.Client, namespace, accountName string) error {
	existing := &operationsv1alpha1.DomainKey{}
	err := cl.Get(ctx, types.NamespacedName{Namespace: namespace, Name: accountName}, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	dk := &operationsv1alpha1.DomainKey{
		Name:      accountName,
		Namespace: namespace,
		Annotations: map[string]string{
			bootstrapAnnotation: bootstrapAnnotationAuto,
		},
		Spec: operationsv1alpha1.DomainKeySpec{
			Type:          domainKeyTypeTeam,
			TenantNameRef: accountName,
			// PrimaryRootKeyRef intentionally left nil - user links it later.
		},
	}
	if err := cl.Create(ctx, dk); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	return nil
}

func defaultAccountNamespace(namespace string) string {
	if namespace == "" {
		return defaultTenantNamespace
	}
	return namespace
}

// SetupWithManager registers the controller against the operations
// multicluster manager.
func (r *AccountBootstrapReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		Named("operations-accountbootstrap").
		For(&kcpapisv1alpha2.APIBinding{}).
		Complete(r)
}

func (r *AccountBootstrapReconciler) clusterClient(ctx context.Context, clusterName multicluster.ClusterName) (client.Client, error) {
	cluster, err := r.Manager.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	return cluster.GetClient(), nil
}

func isOperationsAPIBinding(binding *kcpapisv1alpha2.APIBinding) bool {
	if binding.Spec.Reference.Export == nil {
		return false
	}
	return binding.Spec.Reference.Export.Name == operationsAPIExportName
}
