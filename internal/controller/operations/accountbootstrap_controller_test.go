package operations

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcpapisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	"github.com/openkcm/openkcm-controller/api/shared"
)

const testOpenBaoRootKeyKind = "OpenBaoRootKey"

func TestIsOperationsAPIBinding(t *testing.T) {
	binding := &kcpapisv1alpha2.APIBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "openkcm"},
		Spec: kcpapisv1alpha2.APIBindingSpec{
			Reference: kcpapisv1alpha2.BindingReference{
				Export: &kcpapisv1alpha2.ExportBindingReference{Name: operationsAPIExportName},
			},
		},
	}
	if !isOperationsAPIBinding(binding) {
		t.Fatal("operations.openkcm.io APIBinding was not recognized")
	}

	binding.Spec.Reference.Export.Name = "other.openkcm.io"
	if isOperationsAPIBinding(binding) {
		t.Fatal("non-operations APIBinding was recognized")
	}

	binding.Spec.Reference.Export = nil
	if isOperationsAPIBinding(binding) {
		t.Fatal("APIBinding without export reference was recognized")
	}
}

func TestAccountBootstrapDefaultsCreateTenantAndUnlinkedDomainKey(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add operations scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := &AccountBootstrapReconciler{DefaultRegion: "eu-central"}
	accountName := "ig-clean-account"
	if err := reconciler.ensureTenant(ctx, cl, defaultTenantNamespace, accountName); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}
	if err := reconciler.ensureDomainKey(ctx, cl, defaultTenantNamespace, accountName); err != nil {
		t.Fatalf("ensure domain key: %v", err)
	}

	tenant := &operationsv1alpha1.Tenant{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: defaultTenantNamespace, Name: accountName}, tenant); err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if tenant.Spec.Region != "eu-central" {
		t.Fatalf("tenant region = %q, want eu-central", tenant.Spec.Region)
	}
	if tenant.Annotations[bootstrapAnnotation] != bootstrapAnnotationAuto {
		t.Fatalf("tenant bootstrap annotation = %q, want auto", tenant.Annotations[bootstrapAnnotation])
	}

	// DomainKey must be auto-created in an unlinked state (no
	// primaryRootKeyRef). The DomainKey reconciler will surface
	// AwaitingPrimaryRootKey until the user links an L1.
	domainKey := &operationsv1alpha1.DomainKey{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: defaultTenantNamespace, Name: accountName}, domainKey); err != nil {
		t.Fatalf("get domain key: %v", err)
	}
	if domainKey.Spec.TenantNameRef != accountName {
		t.Fatalf("dk tenantNameRef = %q, want %q", domainKey.Spec.TenantNameRef, accountName)
	}
	if domainKey.Spec.Type != "Team" {
		t.Fatalf("dk type = %q, want Team", domainKey.Spec.Type)
	}
	if domainKey.Spec.PrimaryRootKeyRef != nil {
		t.Fatalf("dk primaryRootKeyRef must be nil at bootstrap, got %#v", domainKey.Spec.PrimaryRootKeyRef)
	}
	if domainKey.Annotations[bootstrapAnnotation] != bootstrapAnnotationAuto {
		t.Fatalf("dk bootstrap annotation = %q, want auto", domainKey.Annotations[bootstrapAnnotation])
	}

	// ensureDomainKey must be idempotent — second call is a no-op.
	if err := reconciler.ensureDomainKey(ctx, cl, defaultTenantNamespace, accountName); err != nil {
		t.Fatalf("ensure domain key (second call): %v", err)
	}
	dks := &operationsv1alpha1.DomainKeyList{}
	if err := cl.List(ctx, dks); err != nil {
		t.Fatalf("list dk: %v", err)
	}
	if len(dks.Items) != 1 {
		t.Fatalf("ensureDomainKey not idempotent, count = %d", len(dks.Items))
	}
}

func TestEnsureAutoDomainKeyForNamespaceUsesAccountRootKey(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add operations scheme: %v", err)
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&operationsv1alpha1.OpenBaoRootKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "account-root",
					Namespace:         defaultTenantNamespace,
					CreationTimestamp: metav1.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				},
				Status: operationsv1alpha1.OpenBaoRootKeyStatus{
					CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
				},
			},
			&operationsv1alpha1.AWSRootKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "account-fallback",
					Namespace:         defaultTenantNamespace,
					CreationTimestamp: metav1.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
				},
				Status: operationsv1alpha1.AWSRootKeyStatus{
					CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
				},
			},
		).
		Build()

	if err := ensureAutoDomainKeyForNamespace(ctx, cl, defaultTenantNamespace, "team-a", "igor"); err != nil {
		t.Fatalf("ensure auto domain key: %v", err)
	}

	dk := &operationsv1alpha1.DomainKey{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "team-a", Name: "team-a"}, dk); err != nil {
		t.Fatalf("get domain key: %v", err)
	}
	if dk.Annotations[bootstrapAnnotation] != bootstrapAnnotationAuto {
		t.Fatalf("dk bootstrap annotation = %q, want auto", dk.Annotations[bootstrapAnnotation])
	}
	if dk.Spec.PrimaryRootKeyRef == nil {
		t.Fatal("dk primaryRootKeyRef is nil")
	}
	if dk.Spec.PrimaryRootKeyRef.Kind != testOpenBaoRootKeyKind ||
		dk.Spec.PrimaryRootKeyRef.Namespace != defaultTenantNamespace ||
		dk.Spec.PrimaryRootKeyRef.Name != "account-root" {
		t.Fatalf("primaryRootKeyRef = %#v, want OpenBaoRootKey/default/account-root", dk.Spec.PrimaryRootKeyRef)
	}
	if len(dk.Spec.FallbackRootKeyRefs) != 0 {
		t.Fatalf("fallbackRootKeyRefs = %#v, want none", dk.Spec.FallbackRootKeyRefs)
	}
}

func TestEnsureAutoDomainKeyForNamespaceSkipsDeletingRootKey(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add operations scheme: %v", err)
	}
	deletionTime := metav1.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&operationsv1alpha1.OpenBaoRootKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deleting-root",
					Namespace:         defaultTenantNamespace,
					CreationTimestamp: metav1.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
					DeletionTimestamp: &deletionTime,
					Finalizers:        []string{"openkcm.io/test-finalizer"},
				},
				Status: operationsv1alpha1.OpenBaoRootKeyStatus{
					CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
				},
			},
			&operationsv1alpha1.AWSRootKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "account-fallback",
					Namespace:         defaultTenantNamespace,
					CreationTimestamp: metav1.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
				},
				Status: operationsv1alpha1.AWSRootKeyStatus{
					CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
				},
			},
		).
		Build()

	if err := ensureAutoDomainKeyForNamespace(ctx, cl, defaultTenantNamespace, "team-a", "igor"); err != nil {
		t.Fatalf("ensure auto domain key: %v", err)
	}

	dk := &operationsv1alpha1.DomainKey{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "team-a", Name: "team-a"}, dk); err != nil {
		t.Fatalf("get domain key: %v", err)
	}
	if dk.Spec.PrimaryRootKeyRef == nil {
		t.Fatal("dk primaryRootKeyRef is nil")
	}
	if dk.Spec.PrimaryRootKeyRef.Kind != "AWSRootKey" ||
		dk.Spec.PrimaryRootKeyRef.Namespace != defaultTenantNamespace ||
		dk.Spec.PrimaryRootKeyRef.Name != "account-fallback" {
		t.Fatalf("primaryRootKeyRef = %#v, want AWSRootKey/default/account-fallback", dk.Spec.PrimaryRootKeyRef)
	}
}

func TestAutoDomainKeyNameKeepsAccountNamespaceCompatible(t *testing.T) {
	if got := autoDomainKeyName(defaultTenantNamespace, defaultTenantNamespace, "igor"); got != "igor" {
		t.Fatalf("account namespace auto name = %q, want igor", got)
	}
	if got := autoDomainKeyName(defaultTenantNamespace, "team-a", "igor"); got != "team-a" {
		t.Fatalf("namespace auto name = %q, want team-a", got)
	}
}

func TestDomainKeyOpenKCMNameIncludesNamespace(t *testing.T) {
	dk := &operationsv1alpha1.DomainKey{
		ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: "team-a"},
	}
	if got := domainKeyOpenKCMName(dk); got != "team-a.payments" {
		t.Fatalf("OpenKCM name = %q, want team-a.payments", got)
	}
}

func TestEnsureAutoDomainKeysForAccountRootSkipsNamespaceLocalRoot(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add operations scheme: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	if err := ensureAutoDomainKeysForAccountRoot(ctx, cl, "team-a", defaultTenantNamespace); err != nil {
		t.Fatalf("ensure auto domain keys: %v", err)
	}

	dks := &operationsv1alpha1.DomainKeyList{}
	if err := cl.List(ctx, dks); err != nil {
		t.Fatalf("list domain keys: %v", err)
	}
	if len(dks.Items) != 0 {
		t.Fatalf("domain keys = %#v, want none", dks.Items)
	}
}

func TestEnsureAutoDomainKeyForNamespaceSkipsExistingDomainKey(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add operations scheme: %v", err)
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&operationsv1alpha1.OpenBaoRootKey{
				ObjectMeta: metav1.ObjectMeta{Name: "account-root", Namespace: defaultTenantNamespace},
				Status: operationsv1alpha1.OpenBaoRootKeyStatus{
					CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
				},
			},
			&operationsv1alpha1.DomainKey{
				ObjectMeta: metav1.ObjectMeta{Name: "custom-domain", Namespace: "team-a"},
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          "Team",
					TenantNameRef: "igor",
				},
			},
		).
		Build()

	if err := ensureAutoDomainKeyForNamespace(ctx, cl, defaultTenantNamespace, "team-a", "igor"); err != nil {
		t.Fatalf("ensure auto domain key: %v", err)
	}

	dks := &operationsv1alpha1.DomainKeyList{}
	if err := cl.List(ctx, dks, client.InNamespace("team-a")); err != nil {
		t.Fatalf("list domain keys: %v", err)
	}
	if len(dks.Items) != 1 || dks.Items[0].Name != "custom-domain" {
		t.Fatalf("domain keys = %#v, want only custom-domain", dks.Items)
	}
}

func TestDomainKeyPrimaryRootKeyResolution(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add operations scheme: %v", err)
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&operationsv1alpha1.OpenBaoRootKey{
			ObjectMeta: metav1.ObjectMeta{Name: "ig-clean-account-root", Namespace: defaultTenantNamespace},
			Status: operationsv1alpha1.OpenBaoRootKeyStatus{
				CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
			},
		}).
		Build()

	reconciler := &DomainKeyReconciler{AccountNamespace: defaultTenantNamespace}
	domainKey := &operationsv1alpha1.DomainKey{
		ObjectMeta: metav1.ObjectMeta{Name: "ig-clean-account", Namespace: defaultTenantNamespace},
		Spec: operationsv1alpha1.DomainKeySpec{
			PrimaryRootKeyRef: &shared.TypedReference{
				APIGroup: operationsv1alpha1.GroupVersion.Group,
				Kind:     testOpenBaoRootKeyKind,
				Name:     "ig-clean-account-root",
			},
		},
	}
	if err := reconciler.resolvePrimaryRootKey(ctx, cl, domainKey); err != nil {
		t.Fatalf("resolve existing OpenBaoRootKey: %v", err)
	}

	domainKey.Namespace = "team-a"
	domainKey.Spec.PrimaryRootKeyRef.Namespace = defaultTenantNamespace
	if err := reconciler.resolvePrimaryRootKey(ctx, cl, domainKey); err != nil {
		t.Fatalf("resolve cross-namespace OpenBaoRootKey: %v", err)
	}

	domainKey.Namespace = defaultTenantNamespace
	domainKey.Spec.PrimaryRootKeyRef.Namespace = ""
	inactive := &operationsv1alpha1.OpenBaoRootKey{
		ObjectMeta: metav1.ObjectMeta{Name: "inactive-root", Namespace: defaultTenantNamespace},
		Status: operationsv1alpha1.OpenBaoRootKeyStatus{
			CryptoState: &shared.CryptoState{LifecycleState: shared.LifecyclePreActive},
		},
	}
	if err := cl.Create(ctx, inactive); err != nil {
		t.Fatalf("create inactive root key: %v", err)
	}
	domainKey.Spec.PrimaryRootKeyRef.Name = "inactive-root"
	if err := reconciler.resolvePrimaryRootKey(ctx, cl, domainKey); err == nil {
		t.Fatal("resolve inactive root key: got nil error")
	} else {
		assertPendingRootKeyResolution(t, err)
	}

	domainKey.Spec.PrimaryRootKeyRef.Name = "missing-root"
	if err := reconciler.resolvePrimaryRootKey(ctx, cl, domainKey); err == nil {
		t.Fatal("resolve missing root key: got nil error")
	} else {
		assertPendingRootKeyResolution(t, err)
	}

	domainKey.Spec.PrimaryRootKeyRef.Kind = "NotARootKey"
	if err := reconciler.resolvePrimaryRootKey(ctx, cl, domainKey); err == nil {
		t.Fatal("resolve unsupported root key kind: got nil error")
	} else if _, ok := rootKeyResolutionFailureResult(err); ok {
		t.Fatal("resolve unsupported root key kind: got retryable error")
	}

	domainKey.Spec.PrimaryRootKeyRef.Kind = testOpenBaoRootKeyKind
	domainKey.Spec.PrimaryRootKeyRef.Name = "ig-clean-account-root"
	domainKey.Spec.PrimaryRootKeyRef.Namespace = "other-team"
	if err := reconciler.resolvePrimaryRootKey(ctx, cl, domainKey); err == nil {
		t.Fatal("resolve foreign namespace root key: got nil error")
	} else if _, ok := rootKeyResolutionFailureResult(err); ok {
		t.Fatal("resolve foreign namespace root key: got retryable error")
	}
}

func TestDomainKeyPrimaryRootKeyLifecycleRejectsInvalidNamespace(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add operations scheme: %v", err)
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&operationsv1alpha1.OpenBaoRootKey{
			ObjectMeta: metav1.ObjectMeta{Name: "account-root", Namespace: defaultTenantNamespace},
			Status: operationsv1alpha1.OpenBaoRootKeyStatus{
				CryptoState: &shared.CryptoState{LifecycleState: shared.LifecycleActive},
			},
		}).
		Build()

	reconciler := &DomainKeyReconciler{AccountNamespace: defaultTenantNamespace}
	domainKey := &operationsv1alpha1.DomainKey{
		ObjectMeta: metav1.ObjectMeta{Name: "team-a", Namespace: "team-a"},
		Spec: operationsv1alpha1.DomainKeySpec{
			Type:          "Team",
			TenantNameRef: "igor",
			PrimaryRootKeyRef: &shared.TypedReference{
				APIGroup:  operationsv1alpha1.GroupVersion.Group,
				Kind:      testOpenBaoRootKeyKind,
				Namespace: "other-team",
				Name:      "account-root",
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

	state, err := reconciler.primaryRootKeyLifecycle(ctx, cl, domainKey)
	if err == nil {
		t.Fatal("primaryRootKeyLifecycle invalid namespace: got nil error")
	}
	if state != "" {
		t.Fatalf("primaryRootKeyLifecycle state = %q, want empty on invalid namespace", state)
	}
	if _, ok := rootKeyResolutionFailureResult(err); ok {
		t.Fatalf("invalid namespace was treated as retryable parent resolution: %v", err)
	}
	if domainKey.Status.CryptoState.LifecycleState != shared.LifecycleActive {
		t.Fatalf("domain key lifecycle mutated to %q, want Active", domainKey.Status.CryptoState.LifecycleState)
	}
}

func TestDomainKeyPrimaryRootKeyLifecycleTreatsClearedRefAsInactiveParent(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add operations scheme: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := &DomainKeyReconciler{AccountNamespace: defaultTenantNamespace}
	domainKey := &operationsv1alpha1.DomainKey{
		ObjectMeta: metav1.ObjectMeta{Name: "team-a", Namespace: "team-a"},
		Spec: operationsv1alpha1.DomainKeySpec{
			Type:          "Team",
			TenantNameRef: "igor",
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

	state, err := reconciler.primaryRootKeyLifecycle(ctx, cl, domainKey)
	if err != nil {
		t.Fatalf("primaryRootKeyLifecycle cleared ref: %v", err)
	}
	if state != "" {
		t.Fatalf("primaryRootKeyLifecycle state = %q, want empty for cleared ref", state)
	}
	if got := effectiveDesiredLifecycle(domainKey.Spec.Lifecycle, state); got != shared.DesiredLifecycleDeactivated {
		t.Fatalf("effective desired with cleared ref = %q, want Deactivated", got)
	}
}

func assertPendingRootKeyResolution(t *testing.T, err error) {
	t.Helper()

	result, ok := rootKeyResolutionFailureResult(err)
	if !ok {
		t.Fatalf("root key resolution error is not retryable: %v", err)
	}
	if result.RequeueAfter != pollInterval {
		t.Fatalf("RequeueAfter = %s, want %s", result.RequeueAfter, pollInterval)
	}
}

func TestDomainKeySingleton(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add operations scheme: %v", err)
	}

	earlier := &operationsv1alpha1.DomainKey{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "first",
			Namespace:         defaultTenantNamespace,
			CreationTimestamp: metav1.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	later := &operationsv1alpha1.DomainKey{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "second",
			Namespace:         defaultTenantNamespace,
			CreationTimestamp: metav1.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(earlier.DeepCopy(), later.DeepCopy()).
		Build()

	reconciler := &DomainKeyReconciler{}

	// "first" has no earlier sibling; it should win.
	got, err := reconciler.findEarlierDomainKey(ctx, cl, earlier)
	if err != nil {
		t.Fatalf("findEarlierDomainKey for earliest: %v", err)
	}
	if got != nil {
		t.Fatalf("earliest DomainKey got rejected by %q", got.Name)
	}

	// "second" should be rejected by "first".
	got, err = reconciler.findEarlierDomainKey(ctx, cl, later)
	if err != nil {
		t.Fatalf("findEarlierDomainKey for later: %v", err)
	}
	if got == nil {
		t.Fatal("later DomainKey was not rejected")
	}
	if got.Name != "first" {
		t.Fatalf("rejected by %q, want %q", got.Name, "first")
	}

	// Deleted earlier siblings should not block.
	deletionTime := metav1.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	deletingEarlier := &operationsv1alpha1.DomainKey{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "deleting",
			Namespace:         defaultTenantNamespace,
			CreationTimestamp: metav1.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC),
			DeletionTimestamp: &deletionTime,
			Finalizers:        []string{domainKeyFinalizer},
		},
	}
	cl2 := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(deletingEarlier.DeepCopy(), earlier.DeepCopy()).
		Build()
	got, err = reconciler.findEarlierDomainKey(ctx, cl2, earlier)
	if err != nil {
		t.Fatalf("findEarlierDomainKey with deleting sibling: %v", err)
	}
	if got != nil {
		t.Fatalf("deleting sibling %q wrongly blocked the new DomainKey", got.Name)
	}

	// Same creationTimestamp — deterministic tiebreaker by name.
	tieA := &operationsv1alpha1.DomainKey{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "alpha",
			Namespace:         defaultTenantNamespace,
			CreationTimestamp: metav1.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	tieB := &operationsv1alpha1.DomainKey{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "beta",
			Namespace:         defaultTenantNamespace,
			CreationTimestamp: metav1.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	cl3 := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(tieA.DeepCopy(), tieB.DeepCopy()).
		Build()
	got, err = reconciler.findEarlierDomainKey(ctx, cl3, tieB)
	if err != nil {
		t.Fatalf("findEarlierDomainKey tiebreak: %v", err)
	}
	if got == nil || got.Name != "alpha" {
		t.Fatalf("tiebreak winner = %v, want alpha", got)
	}
}

// The OIDC block is optional in the v0.7.0 schema, so ensureTenant must leave
// it unset unless the operator was actually configured with defaults — an
// empty block would claim a trust relationship that does not exist.
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
			audiences: []string{"openkcm"},
			wantSet:   true,
		},
		{
			name:      "all three are carried over",
			issuer:    "https://issuer.example",
			jwksURI:   "https://issuer.example/keys",
			audiences: []string{"openkcm", "platform-mesh"},
			wantSet:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// given
			ctx := t.Context()
			scheme := runtime.NewScheme()
			if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
				t.Fatalf("add operations scheme: %v", err)
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).Build()

			reconciler := &AccountBootstrapReconciler{
				DefaultRegion:        "eu-central",
				DefaultOIDCIssuer:    tt.issuer,
				DefaultOIDCJWKSURI:   tt.jwksURI,
				DefaultOIDCAudiences: tt.audiences,
			}

			// when
			if err := reconciler.ensureTenant(ctx, cl, defaultTenantNamespace, accountName); err != nil {
				t.Fatalf("ensure tenant: %v", err)
			}

			// then
			tenant := &operationsv1alpha1.Tenant{}
			key := types.NamespacedName{Namespace: defaultTenantNamespace, Name: accountName}
			if err := cl.Get(ctx, key, tenant); err != nil {
				t.Fatalf("get tenant: %v", err)
			}
			if tenant.Spec.Region != "eu-central" {
				t.Errorf("region = %q, want eu-central", tenant.Spec.Region)
			}

			got := tenant.Spec.OIDCProvider
			if !tt.wantSet {
				if got != nil {
					t.Fatalf("OIDCProvider = %#v, want nil when no defaults are set", got)
				}
				return
			}
			if got == nil {
				t.Fatal("OIDCProvider is nil, want it populated from the operator defaults")
			}
			if got.Issuer != tt.issuer {
				t.Errorf("issuer = %q, want %q", got.Issuer, tt.issuer)
			}
			if got.JWKSURI != tt.jwksURI {
				t.Errorf("jwksURI = %q, want %q", got.JWKSURI, tt.jwksURI)
			}
			if len(got.Audiences) != len(tt.audiences) {
				t.Fatalf("audiences = %v, want %v", got.Audiences, tt.audiences)
			}
			for i, a := range tt.audiences {
				if got.Audiences[i] != a {
					t.Errorf("audiences[%d] = %q, want %q", i, got.Audiences[i], a)
				}
			}
		})
	}
}

// The audience slice is copied rather than aliased, so a later mutation of the
// operator's configuration cannot reach back into an already-created Tenant.
func TestEnsureTenantCopiesAudiences(t *testing.T) {
	// given
	ctx := t.Context()
	scheme := runtime.NewScheme()
	if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add operations scheme: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	audiences := []string{"openkcm"}
	reconciler := &AccountBootstrapReconciler{DefaultOIDCAudiences: audiences}

	// when
	if err := reconciler.ensureTenant(ctx, cl, defaultTenantNamespace, "acme-prod"); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}
	audiences[0] = "mutated"

	// then
	tenant := &operationsv1alpha1.Tenant{}
	key := types.NamespacedName{Namespace: defaultTenantNamespace, Name: "acme-prod"}
	if err := cl.Get(ctx, key, tenant); err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if tenant.Spec.OIDCProvider.Audiences[0] != "openkcm" {
		t.Errorf("audiences[0] = %q, want openkcm: the slice must be copied, not aliased",
			tenant.Spec.OIDCProvider.Audiences[0])
	}
}
