package operations

import (
	"context"
	"time"

	kcpapisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	"github.com/openkcm/openkcm-controller/api/shared"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

func IsOperationsAPIBindingForTest(binding *kcpapisv1alpha2.APIBinding) bool {
	return isOperationsAPIBinding(binding)
}

func EnsureTenantForTest(
	reconciler *AccountBootstrapReconciler,
	ctx context.Context,
	cl client.Client,
	namespace string,
	accountName string,
) error {
	return reconciler.ensureTenant(ctx, cl, namespace, accountName)
}

func EnsureDomainKeyForTest(
	reconciler *AccountBootstrapReconciler,
	ctx context.Context,
	cl client.Client,
	namespace string,
	accountName string,
) error {
	return reconciler.ensureDomainKey(ctx, cl, namespace, accountName)
}

func EnsureAutoDomainKeysForAccountRootForTest(
	ctx context.Context,
	cl client.Client,
	rootNamespace string,
	accountNamespace string,
) error {
	return ensureAutoDomainKeysForAccountRoot(ctx, cl, rootNamespace, accountNamespace)
}

func EnsureAutoDomainKeyForNamespaceForTest(
	ctx context.Context,
	cl client.Client,
	accountNamespace string,
	namespace string,
	accountName string,
) error {
	return ensureAutoDomainKeyForNamespace(ctx, cl, accountNamespace, namespace, accountName)
}

func AutoDomainKeyNameForTest(accountNamespace string, namespace string, accountName string) string {
	return autoDomainKeyName(accountNamespace, namespace, accountName)
}

func DomainKeyOpenKCMNameForTest(domainKey *operationsv1alpha1.DomainKey) string {
	return domainKeyOpenKCMName(domainKey)
}

func ResolvePrimaryRootKeyForTest(
	reconciler *DomainKeyReconciler,
	ctx context.Context,
	cl client.Client,
	domainKey *operationsv1alpha1.DomainKey,
) error {
	return reconciler.resolvePrimaryRootKey(ctx, cl, domainKey)
}

func PrimaryRootKeyLifecycleForTest(
	reconciler *DomainKeyReconciler,
	ctx context.Context,
	cl client.Client,
	domainKey *operationsv1alpha1.DomainKey,
) (shared.LifecycleState, error) {
	return reconciler.primaryRootKeyLifecycle(ctx, cl, domainKey)
}

func RootKeyResolutionFailureResultForTest(err error) (ctrl.Result, bool) {
	return rootKeyResolutionFailureResult(err)
}

func EffectiveDesiredLifecycleForTest(
	specLifecycle shared.DesiredLifecycle,
	parentState shared.LifecycleState,
) shared.DesiredLifecycle {
	return effectiveDesiredLifecycle(specLifecycle, parentState)
}

func FindEarlierDomainKeyForTest(
	reconciler *DomainKeyReconciler,
	ctx context.Context,
	cl client.Client,
	domainKey *operationsv1alpha1.DomainKey,
) (*operationsv1alpha1.DomainKey, error) {
	return reconciler.findEarlierDomainKey(ctx, cl, domainKey)
}

func PollIntervalForTest() time.Duration {
	return pollInterval
}

func CascadeTestContext() context.Context {
	return ctx
}

func CascadeTestClient() client.Client {
	return k8sClient
}

func EnsureCascadeLogicalClusterForTest() {
	ensureLogicalCluster(testWorkspace)
}

func NewCascadeTestBackend() Backend {
	return &fakeBackend{}
}

func NewCascadeTestManager() mcmanager.Manager {
	return newTestManager()
}

func CascadeRequestForTest(obj client.Object) mcreconcile.Request {
	return requestFor(obj)
}
