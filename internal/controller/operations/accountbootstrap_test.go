package operations_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcpapisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	operationsv1alpha1 "github.com/openkcm/platform-mesh-controller/api/operations/v1alpha1"
	"github.com/openkcm/platform-mesh-controller/api/shared"
	operations "github.com/openkcm/platform-mesh-controller/internal/controller/operations"
)

const (
	testOpenBaoRootKeyKind      = "OpenBaoRootKey"
	testAWSRootKeyKind          = "AWSRootKey"
	testRegion                  = "eu-central"
	testDefaultTenantNamespace  = "default"
	testBootstrapAnnotation     = "operations.openkcm.io/bootstrap"
	testBootstrapAnnotationAuto = "auto"
	testDomainKeyTypeTeam       = "Team"
	testDomainKeyFinalizer      = "operations.openkcm.io/domainkey-cleanup"
	openkcmAudience             = "openkcm"
	igCleanAccount              = "ig-clean-account"
	igCleanAccountRoot          = "ig-clean-account-root"
	accountRoot                 = "account-root"
	accountFallback             = "account-fallback"
	teamA                       = "team-a"
	igorTenant                  = "igor"
	testInstanceDomainKeyName   = "orders-db"
)

func TestIsOperationsAPIBinding(t *testing.T) {
	binding := &kcpapisv1alpha2.APIBinding{
		Name: "openkcm",
		Spec: kcpapisv1alpha2.APIBindingSpec{
			Reference: kcpapisv1alpha2.BindingReference{
				Export: &kcpapisv1alpha2.ExportBindingReference{Name: "operations.openkcm.io"},
			},
		},
	}
	assert.True(t, operations.IsOperationsAPIBinding(binding))

	binding.Spec.Reference.Export.Name = "other.openkcm.io"
	assert.False(t, operations.IsOperationsAPIBinding(binding))

	binding.Spec.Reference.Export = nil
	assert.False(t, operations.IsOperationsAPIBinding(binding))
}

func TestAccountBootstrapDefaultsCreateTenantAndUnlinkedDomainKey(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := &operations.AccountBootstrapReconciler{DefaultRegion: testRegion}
	accountName := igCleanAccount
	require.NoError(t, operations.EnsureTenant(
		reconciler,
		ctx,
		cl,
		testDefaultTenantNamespace,
		accountName,
	))
	require.NoError(t, operations.EnsureDomainKey(
		reconciler,
		ctx,
		cl,
		testDefaultTenantNamespace,
		accountName,
	))

	tenant := &operationsv1alpha1.Tenant{}
	require.NoError(t, cl.Get(
		ctx,
		types.NamespacedName{Namespace: testDefaultTenantNamespace, Name: accountName},
		tenant,
	))
	assert.Equal(t, testRegion, tenant.Spec.Region)
	assert.Equal(t, testBootstrapAnnotationAuto, tenant.Annotations[testBootstrapAnnotation])

	domainKey := &operationsv1alpha1.DomainKey{}
	require.NoError(t, cl.Get(
		ctx,
		types.NamespacedName{Namespace: testDefaultTenantNamespace, Name: accountName},
		domainKey,
	))
	assert.Equal(t, accountName, domainKey.Spec.TenantNameRef)
	assert.Equal(t, testDomainKeyTypeTeam, domainKey.Spec.Type)
	assert.Nil(t, domainKey.Spec.PrimaryRootKeyRef)
	assert.Equal(t, testBootstrapAnnotationAuto, domainKey.Annotations[testBootstrapAnnotation])

	require.NoError(t, operations.EnsureDomainKey(
		reconciler,
		ctx,
		cl,
		testDefaultTenantNamespace,
		accountName,
	))
	domainKeys := &operationsv1alpha1.DomainKeyList{}
	require.NoError(t, cl.List(ctx, domainKeys))
	assert.Len(t, domainKeys.Items, 1)
}

func TestEnsureAutoDomainKeyForNamespaceUsesAccountRootKey(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&operationsv1alpha1.OpenBaoRootKey{
				Name:              accountRoot,
				Namespace:         testDefaultTenantNamespace,
				CreationTimestamp: metav1.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				Status: operationsv1alpha1.OpenBaoRootKeyStatus{
					CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
				},
			},
			&operationsv1alpha1.AWSRootKey{
				Name:              accountFallback,
				Namespace:         testDefaultTenantNamespace,
				CreationTimestamp: metav1.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
				Status: operationsv1alpha1.AWSRootKeyStatus{
					CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
				},
			},
		).
		Build()

	require.NoError(t, operations.EnsureAutoDomainKeyForNamespace(
		ctx,
		cl,
		testDefaultTenantNamespace,
		teamA,
		igorTenant,
	))

	domainKey := &operationsv1alpha1.DomainKey{}
	require.NoError(t, cl.Get(ctx, types.NamespacedName{Namespace: teamA, Name: teamA}, domainKey))
	assert.Equal(t, testBootstrapAnnotationAuto, domainKey.Annotations[testBootstrapAnnotation])
	require.NotNil(t, domainKey.Spec.PrimaryRootKeyRef)
	assert.Equal(t, testOpenBaoRootKeyKind, domainKey.Spec.PrimaryRootKeyRef.Kind)
	assert.Equal(t, testDefaultTenantNamespace, domainKey.Spec.PrimaryRootKeyRef.Namespace)
	assert.Equal(t, accountRoot, domainKey.Spec.PrimaryRootKeyRef.Name)
	assert.Empty(t, domainKey.Spec.FallbackRootKeyRefs)
}

func TestEnsureAutoDomainKeyForNamespaceSkipsDeletingRootKey(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme))
	deletionTime := metav1.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&operationsv1alpha1.OpenBaoRootKey{
				Name:              "deleting-root",
				Namespace:         testDefaultTenantNamespace,
				CreationTimestamp: metav1.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				DeletionTimestamp: &deletionTime,
				Finalizers:        []string{"openkcm.io/test-finalizer"},
				Status: operationsv1alpha1.OpenBaoRootKeyStatus{
					CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
				},
			},
			&operationsv1alpha1.AWSRootKey{
				Name:              accountFallback,
				Namespace:         testDefaultTenantNamespace,
				CreationTimestamp: metav1.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
				Status: operationsv1alpha1.AWSRootKeyStatus{
					CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
				},
			},
		).
		Build()

	require.NoError(t, operations.EnsureAutoDomainKeyForNamespace(
		ctx,
		cl,
		testDefaultTenantNamespace,
		teamA,
		igorTenant,
	))

	domainKey := &operationsv1alpha1.DomainKey{}
	require.NoError(t, cl.Get(ctx, types.NamespacedName{Namespace: teamA, Name: teamA}, domainKey))
	require.NotNil(t, domainKey.Spec.PrimaryRootKeyRef)
	assert.Equal(t, testAWSRootKeyKind, domainKey.Spec.PrimaryRootKeyRef.Kind)
	assert.Equal(t, testDefaultTenantNamespace, domainKey.Spec.PrimaryRootKeyRef.Namespace)
	assert.Equal(t, accountFallback, domainKey.Spec.PrimaryRootKeyRef.Name)
}

func TestAutoDomainKeyNameKeepsAccountNamespaceCompatible(t *testing.T) {
	assert.Equal(
		t,
		igorTenant,
		operations.AutoDomainKeyName(testDefaultTenantNamespace, testDefaultTenantNamespace, igorTenant),
	)
	assert.Equal(
		t,
		teamA,
		operations.AutoDomainKeyName(testDefaultTenantNamespace, teamA, igorTenant),
	)
}

func TestDomainKeyOpenKCMNameIncludesNamespace(t *testing.T) {
	domainKey := &operationsv1alpha1.DomainKey{Name: "payments", Namespace: teamA}
	assert.Equal(t, "team-a.payments", operations.DomainKeyOpenKCMName(domainKey))
}

func TestEnsureAutoDomainKeysForAccountRootSkipsNamespaceLocalRoot(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	require.NoError(t, operations.EnsureAutoDomainKeysForAccountRoot(
		ctx,
		cl,
		teamA,
		testDefaultTenantNamespace,
	))

	domainKeys := &operationsv1alpha1.DomainKeyList{}
	require.NoError(t, cl.List(ctx, domainKeys))
	assert.Empty(t, domainKeys.Items)
}

func TestEnsureAutoDomainKeyForNamespaceSkipsExistingDomainKey(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&operationsv1alpha1.OpenBaoRootKey{
				Name: accountRoot, Namespace: testDefaultTenantNamespace,
				Status: operationsv1alpha1.OpenBaoRootKeyStatus{
					CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
				},
			},
			&operationsv1alpha1.DomainKey{
				Name: "custom-domain", Namespace: teamA,
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          testDomainKeyTypeTeam,
					TenantNameRef: igorTenant,
				},
			},
		).
		Build()

	require.NoError(t, operations.EnsureAutoDomainKeyForNamespace(
		ctx,
		cl,
		testDefaultTenantNamespace,
		teamA,
		igorTenant,
	))

	domainKeys := &operationsv1alpha1.DomainKeyList{}
	require.NoError(t, cl.List(ctx, domainKeys, client.InNamespace(teamA)))
	require.Len(t, domainKeys.Items, 1)
	assert.Equal(t, "custom-domain", domainKeys.Items[0].Name)
}

func TestDomainKeyPrimaryRootKeyResolution(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&operationsv1alpha1.OpenBaoRootKey{
			Name: igCleanAccountRoot, Namespace: testDefaultTenantNamespace,
			Status: operationsv1alpha1.OpenBaoRootKeyStatus{
				CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
			},
		}).
		Build()

	reconciler := &operations.DomainKeyReconciler{AccountNamespace: testDefaultTenantNamespace}
	domainKey := &operationsv1alpha1.DomainKey{
		Name: igCleanAccount, Namespace: testDefaultTenantNamespace,
		Spec: operationsv1alpha1.DomainKeySpec{
			PrimaryRootKeyRef: &shared.TypedReference{
				APIGroup: operationsv1alpha1.GroupVersion.Group,
				Kind:     testOpenBaoRootKeyKind,
				Name:     igCleanAccountRoot,
			},
		},
	}
	require.NoError(t, operations.ResolvePrimaryRootKey(reconciler, ctx, cl, domainKey))

	domainKey.Namespace = teamA
	domainKey.Spec.PrimaryRootKeyRef.Namespace = testDefaultTenantNamespace
	require.NoError(t, operations.ResolvePrimaryRootKey(reconciler, ctx, cl, domainKey))

	domainKey.Namespace = testDefaultTenantNamespace
	domainKey.Spec.PrimaryRootKeyRef.Namespace = ""
	inactive := &operationsv1alpha1.OpenBaoRootKey{
		Name: "inactive-root", Namespace: testDefaultTenantNamespace,
		Status: operationsv1alpha1.OpenBaoRootKeyStatus{
			CryptoState: &shared.CryptoState{LifecycleState: shared.LifecyclePreActive},
		},
	}
	require.NoError(t, cl.Create(ctx, inactive))
	domainKey.Spec.PrimaryRootKeyRef.Name = "inactive-root"
	err := operations.ResolvePrimaryRootKey(reconciler, ctx, cl, domainKey)
	require.Error(t, err)
	assertPendingRootKeyResolution(t, err)

	domainKey.Spec.PrimaryRootKeyRef.Name = "missing-root"
	err = operations.ResolvePrimaryRootKey(reconciler, ctx, cl, domainKey)
	require.Error(t, err)
	assertPendingRootKeyResolution(t, err)

	domainKey.Spec.PrimaryRootKeyRef.Kind = "NotARootKey"
	err = operations.ResolvePrimaryRootKey(reconciler, ctx, cl, domainKey)
	require.Error(t, err)
	_, retryable := operations.RootKeyResolutionFailureResult(err)
	assert.False(t, retryable)

	domainKey.Spec.PrimaryRootKeyRef.Kind = testOpenBaoRootKeyKind
	domainKey.Spec.PrimaryRootKeyRef.Name = igCleanAccountRoot
	domainKey.Spec.PrimaryRootKeyRef.Namespace = "other-team"
	err = operations.ResolvePrimaryRootKey(reconciler, ctx, cl, domainKey)
	require.Error(t, err)
	_, retryable = operations.RootKeyResolutionFailureResult(err)
	assert.False(t, retryable)
}

func TestDomainKeyPrimaryRootKeyLifecycleRejectsInvalidNamespace(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&operationsv1alpha1.OpenBaoRootKey{
			Name: accountRoot, Namespace: testDefaultTenantNamespace,
			Status: operationsv1alpha1.OpenBaoRootKeyStatus{
				CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
			},
		}).
		Build()

	reconciler := &operations.DomainKeyReconciler{AccountNamespace: testDefaultTenantNamespace}
	domainKey := &operationsv1alpha1.DomainKey{
		Name: teamA, Namespace: teamA,
		Spec: operationsv1alpha1.DomainKeySpec{
			Type:          testDomainKeyTypeTeam,
			TenantNameRef: igorTenant,
			PrimaryRootKeyRef: &shared.TypedReference{
				APIGroup:  operationsv1alpha1.GroupVersion.Group,
				Kind:      testOpenBaoRootKeyKind,
				Namespace: "other-team",
				Name:      accountRoot,
			},
			Lifecycle: shared.DesiredLifecycleActive,
		},
		Status: operationsv1alpha1.DomainKeyStatus{
			ObservedGeneration: 1,
			CryptoState: &shared.CryptoState{
				ID:             "domain-key-id",
				LifecycleState: shared.LifecycleActive,
			},
		},
	}

	state, err := operations.PrimaryRootKeyLifecycle(reconciler, ctx, cl, domainKey)
	require.Error(t, err)
	assert.Empty(t, state)
	_, retryable := operations.RootKeyResolutionFailureResult(err)
	assert.False(t, retryable)
	assert.Equal(t, shared.LifecycleActive, domainKey.Status.CryptoState.LifecycleState)
}

func TestDomainKeyPrimaryRootKeyLifecycleWithoutRefUsesDefaultRoot(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := &operations.DomainKeyReconciler{AccountNamespace: testDefaultTenantNamespace}
	domainKey := &operationsv1alpha1.DomainKey{
		Name: teamA, Namespace: teamA,
		Spec: operationsv1alpha1.DomainKeySpec{
			Type:          testDomainKeyTypeTeam,
			TenantNameRef: igorTenant,
			Lifecycle:     shared.DesiredLifecycleActive,
		},
		Status: operationsv1alpha1.DomainKeyStatus{
			ObservedGeneration: 1,
			CryptoState: &shared.CryptoState{
				ID:             "domain-key-id",
				LifecycleState: shared.LifecycleActive,
			},
		},
	}

	state, err := operations.PrimaryRootKeyLifecycle(reconciler, ctx, cl, domainKey)
	require.NoError(t, err)
	assert.Equal(t, shared.LifecycleActive, state)
	assert.Equal(
		t,
		shared.DesiredLifecycleActive,
		operations.EffectiveDesiredLifecycle(domainKey.Spec.Lifecycle, state),
	)
}

func assertPendingRootKeyResolution(t *testing.T, err error) {
	t.Helper()

	result, retryable := operations.RootKeyResolutionFailureResult(err)
	require.True(t, retryable)
	assert.Equal(t, operations.PollInterval, result.RequeueAfter)
}

func TestDomainKeySingleton(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme))

	earliest := &operationsv1alpha1.DomainKey{
		Name:              "first",
		Namespace:         testDefaultTenantNamespace,
		CreationTimestamp: metav1.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	later := &operationsv1alpha1.DomainKey{
		Name:              "second",
		Namespace:         testDefaultTenantNamespace,
		CreationTimestamp: metav1.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(earliest.DeepCopy(), later.DeepCopy()).
		Build()

	reconciler := &operations.DomainKeyReconciler{}

	winner, err := operations.FindEarlierDomainKey(reconciler, ctx, cl, earliest)
	require.NoError(t, err)
	assert.Nil(t, winner)

	winner, err = operations.FindEarlierDomainKey(reconciler, ctx, cl, later)
	require.NoError(t, err)
	require.NotNil(t, winner)
	assert.Equal(t, "first", winner.Name)

	deletionTime := metav1.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	deletingEarlier := &operationsv1alpha1.DomainKey{
		Name:              "deleting",
		Namespace:         testDefaultTenantNamespace,
		CreationTimestamp: metav1.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC),
		DeletionTimestamp: &deletionTime,
		Finalizers:        []string{testDomainKeyFinalizer},
	}
	clWithDeletingSibling := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(deletingEarlier.DeepCopy(), earliest.DeepCopy()).
		Build()
	winner, err = operations.FindEarlierDomainKey(reconciler, ctx, clWithDeletingSibling, earliest)
	require.NoError(t, err)
	assert.Nil(t, winner)

	tieA := &operationsv1alpha1.DomainKey{
		Name:              "alpha",
		Namespace:         testDefaultTenantNamespace,
		CreationTimestamp: metav1.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	tieB := &operationsv1alpha1.DomainKey{
		Name:              "beta",
		Namespace:         testDefaultTenantNamespace,
		CreationTimestamp: metav1.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	clWithTie := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(tieA.DeepCopy(), tieB.DeepCopy()).
		Build()
	winner, err = operations.FindEarlierDomainKey(reconciler, ctx, clWithTie, tieB)
	require.NoError(t, err)
	require.NotNil(t, winner)
	assert.Equal(t, "alpha", winner.Name)
}

func TestDomainKeySingletonIgnoresInstanceDomainKeys(t *testing.T) {
	// given
	ctx := t.Context()
	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme))
	earlierInstance := &operationsv1alpha1.DomainKey{
		Name:              testInstanceDomainKeyName,
		Namespace:         testDefaultTenantNamespace,
		CreationTimestamp: metav1.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Spec:              operationsv1alpha1.DomainKeySpec{Scope: operationsv1alpha1.DomainKeyScopeInstance},
	}
	namespaced := &operationsv1alpha1.DomainKey{
		Name:              "team",
		Namespace:         testDefaultTenantNamespace,
		CreationTimestamp: metav1.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		Spec:              operationsv1alpha1.DomainKeySpec{Scope: operationsv1alpha1.DomainKeyScopeNamespace},
	}
	laterInstance := &operationsv1alpha1.DomainKey{
		Name:              "billing-db",
		Namespace:         testDefaultTenantNamespace,
		CreationTimestamp: metav1.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		Spec:              operationsv1alpha1.DomainKeySpec{Scope: operationsv1alpha1.DomainKeyScopeInstance},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(earlierInstance.DeepCopy(), namespaced.DeepCopy(), laterInstance.DeepCopy()).
		Build()
	reconciler := &operations.DomainKeyReconciler{}

	// when
	namespacedWinner, namespacedErr := operations.FindEarlierDomainKey(reconciler, ctx, cl, namespaced)
	instanceWinner, instanceErr := operations.FindEarlierDomainKey(reconciler, ctx, cl, laterInstance)

	// then
	require.NoError(t, namespacedErr)
	require.NoError(t, instanceErr)
	assert.Nil(t, namespacedWinner, "an older Instance DomainKey does not take the Namespace slot")
	assert.Nil(t, instanceWinner, "Instance DomainKeys are not limited per namespace")
}

func TestEnsureAutoDomainKeyForNamespaceIgnoresInstanceDomainKeys(t *testing.T) {
	// given
	ctx := t.Context()
	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&operationsv1alpha1.OpenBaoRootKey{
				Name: accountRoot, Namespace: testDefaultTenantNamespace,
				Status: operationsv1alpha1.OpenBaoRootKeyStatus{
					CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
				},
			},
			&operationsv1alpha1.DomainKey{
				Name: testInstanceDomainKeyName, Namespace: teamA,
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          testDomainKeyTypeTeam,
					Scope:         operationsv1alpha1.DomainKeyScopeInstance,
					TenantNameRef: igorTenant,
				},
			},
		).
		Build()

	// when
	err := operations.EnsureAutoDomainKeyForNamespace(ctx, cl, testDefaultTenantNamespace, teamA, igorTenant)

	// then
	require.NoError(t, err)
	domainKeys := &operationsv1alpha1.DomainKeyList{}
	require.NoError(t, cl.List(ctx, domainKeys, client.InNamespace(teamA)))
	names := make([]string, 0, len(domainKeys.Items))
	for _, dk := range domainKeys.Items {
		names = append(names, dk.Name)
	}
	assert.ElementsMatch(t, []string{testInstanceDomainKeyName, teamA}, names)
}

func TestEnsureTenantOIDCDefaulting(t *testing.T) {
	const accountName = "acme-prod"

	tests := []struct {
		name      string
		issuer    string
		jwksURI   string
		audiences []string
		wantSet   bool
	}{
		{
			name:    "no defaults configured leaves the block unset",
			wantSet: false,
		},
		{
			name:    "issuer alone is enough to populate it",
			issuer:  "https://issuer.example",
			wantSet: true,
		},
		{
			name:    "jwks uri alone is enough to populate it",
			jwksURI: "https://issuer.example/keys",
			wantSet: true,
		},
		{
			name:      "audiences alone are enough to populate it",
			audiences: []string{openkcmAudience},
			wantSet:   true,
		},
		{
			name:      "all three are carried over",
			issuer:    "https://issuer.example",
			jwksURI:   "https://issuer.example/keys",
			audiences: []string{openkcmAudience, "platform-mesh"},
			wantSet:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			scheme := runtime.NewScheme()
			require.NoError(t, operationsv1alpha1.AddToScheme(scheme))
			cl := fake.NewClientBuilder().WithScheme(scheme).Build()
			reconciler := &operations.AccountBootstrapReconciler{
				DefaultRegion:        testRegion,
				DefaultOIDCIssuer:    tt.issuer,
				DefaultOIDCJWKSURI:   tt.jwksURI,
				DefaultOIDCAudiences: tt.audiences,
			}

			require.NoError(t, operations.EnsureTenant(
				reconciler,
				ctx,
				cl,
				testDefaultTenantNamespace,
				accountName,
			))

			tenant := &operationsv1alpha1.Tenant{}
			require.NoError(t, cl.Get(
				ctx,
				types.NamespacedName{Namespace: testDefaultTenantNamespace, Name: accountName},
				tenant,
			))
			assert.Equal(t, testRegion, tenant.Spec.Region)

			provider := tenant.Spec.OIDCProvider
			if !tt.wantSet {
				assert.Nil(t, provider)
				return
			}
			require.NotNil(t, provider)
			assert.Equal(t, tt.issuer, provider.Issuer)
			assert.Equal(t, tt.jwksURI, provider.JWKSURI)
			assert.Equal(t, tt.audiences, provider.Audiences)
		})
	}
}

func TestEnsureTenantCopiesAudiences(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	audiences := []string{openkcmAudience}
	reconciler := &operations.AccountBootstrapReconciler{DefaultOIDCAudiences: audiences}
	require.NoError(t, operations.EnsureTenant(
		reconciler,
		ctx,
		cl,
		testDefaultTenantNamespace,
		"acme-prod",
	))
	audiences[0] = "mutated"

	tenant := &operationsv1alpha1.Tenant{}
	require.NoError(t, cl.Get(
		ctx,
		types.NamespacedName{Namespace: testDefaultTenantNamespace, Name: "acme-prod"},
		tenant,
	))
	require.NotNil(t, tenant.Spec.OIDCProvider)
	require.NotEmpty(t, tenant.Spec.OIDCProvider.Audiences)
	assert.Equal(t, openkcmAudience, tenant.Spec.OIDCProvider.Audiences[0])
}
