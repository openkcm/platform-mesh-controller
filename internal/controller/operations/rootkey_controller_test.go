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
	"bytes"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	"github.com/openkcm/openkcm-controller/api/shared"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

// The specs share one API server, so the fixtures get their own namespace:
// AccountBootstrap picks up any root key sitting in the account namespace and
// links it, which would make the order Ginkgo happens to pick matter.
//
// It is also not the account namespace the reconciler is given, which keeps the
// auto-DomainKey side effect out of specs that are about the L1 loop itself.
const (
	rootKeyNamespace        = "rootkey-specs"
	rootKeyAccountNamespace = "openkcm-accounts"
)

func ensureRootKeyNamespace() {
	GinkgoHelper()

	ns := &corev1.Namespace{}
	ns.Name = rootKeyNamespace
	err := k8sClient.Create(ctx, ns)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

// rootKeyFixtures builds a fully populated object per kind. Every string field
// carries the sentinel so the credential-hygiene spec can look for any of them
// in the controller's output.
var rootKeyFixtures = map[string]func(sentinel string) RootKey{
	"AWSRootKey": func(s string) RootKey {
		rk := &operationsv1alpha1.AWSRootKey{}
		rk.Spec.TenantNameRef = testAccountName
		rk.Spec.Region = s
		rk.Spec.KeyURI = s
		rk.Spec.EndpointURL = s
		rk.Spec.RolesAnywhere = operationsv1alpha1.AWSRolesAnywhereConfig{
			TrustAnchorARN: s, ProfileARN: s, RoleARN: s,
		}
		return rk
	},
	"AzureRootKey": func(s string) RootKey {
		rk := &operationsv1alpha1.AzureRootKey{}
		rk.Spec.TenantNameRef = testAccountName
		rk.Spec.VaultURL = s
		rk.Spec.KeyName = s
		rk.Spec.KeyVersion = s
		rk.Spec.FederatedIdentity = operationsv1alpha1.AzureFederatedIdentityConfig{
			TenantID: s, ClientID: s,
		}
		return rk
	},
	"OpenBaoRootKey": func(s string) RootKey {
		rk := &operationsv1alpha1.OpenBaoRootKey{}
		rk.Spec.TenantNameRef = testAccountName
		rk.Spec.EnginePath = s
		rk.Spec.KeyName = s
		rk.Spec.ServerAddress = s
		rk.Spec.CertAuth = operationsv1alpha1.OpenBaoCertAuthConfig{
			AuthMountPath: s, RoleName: s,
		}
		return rk
	},
	"GCPRootKey": func(s string) RootKey {
		rk := &operationsv1alpha1.GCPRootKey{}
		rk.Spec.TenantNameRef = testAccountName
		rk.Spec.KeyURI = s
		return rk
	},
	"VaultRootKey": func(s string) RootKey {
		rk := &operationsv1alpha1.VaultRootKey{}
		rk.Spec.TenantNameRef = testAccountName
		rk.Spec.ServerAddress = s
		rk.Spec.KeyName = s
		return rk
	},
	"HSMRootKey": func(s string) RootKey {
		rk := &operationsv1alpha1.HSMRootKey{}
		rk.Spec.TenantNameRef = testAccountName
		rk.Spec.SlotURI = s
		return rk
	},
}

var rootKeyCounter int

// newRootKeyFixture creates the object in the API server and returns it.
func newRootKeyFixture(kind, sentinel string) RootKey {
	GinkgoHelper()

	build, ok := rootKeyFixtures[kind]
	Expect(ok).To(BeTrue(), "no fixture for kind %s", kind)

	rootKeyCounter++
	rk := build(sentinel)
	rk.SetName(fmt.Sprintf("%s-%d", strings.ToLower(kind), rootKeyCounter))
	rk.SetNamespace(rootKeyNamespace)
	Expect(k8sClient.Create(ctx, rk)).To(Succeed())
	return rk
}

func reloadRootKey(kind, name string) RootKey {
	GinkgoHelper()

	entry := rootKeyGVK(kind)
	Expect(entry).NotTo(BeNil())
	obj := entry.newEmpty()
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: rootKeyNamespace}, obj)).To(Succeed())
	return obj.(RootKey)
}

func rootKeyRequest(rk RootKey) mcreconcile.Request {
	return mcreconcile.Request{
		ClusterName: testClusterName,
		Request: reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      rk.GetName(),
			Namespace: rk.GetNamespace(),
		}},
	}
}

// reconcileToActive drives the loop the way the manager would: finalizer pass,
// register pass, then the poll that activates.
func reconcileToActive(r *RootKeyReconciler, rk RootKey) RootKey {
	GinkgoHelper()

	for range 3 {
		_, err := r.Reconcile(ctx, rootKeyRequest(rk))
		Expect(err).NotTo(HaveOccurred())
	}
	return reloadRootKey(r.Kind(), rk.GetName())
}

var _ = Describe("RootKeyReconciler", func() {
	var backend *fakeBackend

	newReconcilerFor := func(kind string) *RootKeyReconciler {
		GinkgoHelper()
		entry := rootKeyGVK(kind)
		Expect(entry).NotTo(BeNil())
		for _, k := range rootKeyKinds {
			if k.Kind == kind {
				r := NewRootKeyReconciler(backend, rootKeyAccountNamespace, k.New)
				r.manager = newTestManager()
				return r
			}
		}
		Fail("unknown kind " + kind)
		return nil
	}

	BeforeEach(func() {
		ensureLogicalCluster(testWorkspace)
		ensureRootKeyNamespace()
		backend = &fakeBackend{}
	})

	// main.go registers all six through RootKeyReconcilers. A duplicate
	// controller name or a kind missing from the scheme only shows up at
	// manager startup, which no unit test would otherwise reach.
	It("registers every kind with the manager", func() {
		mgr := newTestManager()
		reconcilers := RootKeyReconcilers(backend, rootKeyAccountNamespace)
		Expect(reconcilers).To(HaveLen(len(rootKeyKinds)))

		seen := map[string]bool{}
		for _, r := range reconcilers {
			Expect(seen[r.Kind()]).To(BeFalse(), "kind %s registered twice", r.Kind())
			seen[r.Kind()] = true
			Expect(r.SetupWithManager(mgr)).To(Succeed(), "kind %s", r.Kind())
		}
	})

	It("covers every kind the package declares", func() {
		Expect(rootKeyFixtures).To(HaveLen(len(rootKeyKinds)),
			"a new L1 kind needs a fixture here, or it ships untested")
		for _, k := range rootKeyKinds {
			Expect(rootKeyFixtures).To(HaveKey(k.Kind))
		}
	})

	// The three original controllers covered AWS, Azure and OpenBao only.
	// GCPRootKey, VaultRootKey and HSMRootKey had a CRD, an APIResourceSchema
	// and an APIExport entry, and nothing watching them.
	for _, kind := range []string{
		"AWSRootKey", "AzureRootKey", "OpenBaoRootKey",
		"GCPRootKey", "VaultRootKey", "HSMRootKey",
	} {
		Context("for "+kind, func() {
			It("registers the key against the path-derived tenant and activates it", func() {
				r := newReconcilerFor(kind)
				rk := newRootKeyFixture(kind, "sentinel")

				reloaded := reconcileToActive(r, rk)

				Expect(backend.createTenantCalls).To(Equal([]string{testAccountName}),
					"the tenant must come from the workspace path, not from spec.tenantNameRef")
				Expect(backend.createRootKeyCalls).To(HaveLen(1))
				Expect(backend.createRootKeyCalls[0].TenantID).To(Equal("tenant-uuid"))
				Expect(backend.createRootKeyCalls[0].Name).To(Equal(rk.GetName()))
				Expect(backend.activateKeyCalls).To(Equal([]string{rootKeyID}))

				state := reloaded.GetCryptoState()
				Expect(state).NotTo(BeNil())
				Expect(state.ID).To(Equal(rootKeyID))
				Expect(state.LifecycleState).To(Equal(shared.LifecycleActive))
				Expect(reloaded.GetObservedGeneration()).To(Equal(reloaded.GetGeneration()))

				ready := meta.FindStatusCondition(*reloaded.StatusConditions(), readyType)
				Expect(ready).NotTo(BeNil())
				Expect(ready.Status).To(Equal(metav1.ConditionTrue))
				Expect(ready.Reason).To(Equal(reasonUpstreamAuthenticated))
			})

			// Only the OpenBao controller used to set this, so anything
			// gating on ProviderSynced never saw an AWS or Azure root as
			// synced. One loop means one answer for every provider.
			It("reports ProviderSynced once the key is bound", func() {
				r := newReconcilerFor(kind)
				rk := newRootKeyFixture(kind, "sentinel")

				reloaded := reconcileToActive(r, rk)

				synced := meta.FindStatusCondition(*reloaded.StatusConditions(), providerSyncedType)
				Expect(synced).NotTo(BeNil(), "every provider must report ProviderSynced")
				Expect(synced.Status).To(Equal(metav1.ConditionTrue))
			})

			It("keeps the finalizer name the per-kind controllers used", func() {
				r := newReconcilerFor(kind)
				rk := newRootKeyFixture(kind, "sentinel")

				_, err := r.Reconcile(ctx, rootKeyRequest(rk))
				Expect(err).NotTo(HaveOccurred())

				want := "operations.openkcm.io/" + strings.ToLower(kind) + "-cleanup"
				Expect(reloadRootKey(kind, rk.GetName()).GetFinalizers()).To(ContainElement(want))
				Expect(backend.createRootKeyCalls).To(BeEmpty(),
					"the backend must not be called before the finalizer is persisted")
			})
		})
	}

	Context("when the tenant has not reached the backend yet", func() {
		It("requeues instead of failing, and converges once the tenant lands", func() {
			r := newReconcilerFor("AWSRootKey")
			rk := newRootKeyFixture("AWSRootKey", "sentinel")

			backend.createTenantFn = func(openkcmapi.CreateTenantRequest) (*openkcmapi.CreateTenantResponse, error) {
				return nil, openkcmapi.NewAPIError(openkcmapi.KindTransient, 503, "Unavailable", "backend starting")
			}

			_, err := r.Reconcile(ctx, rootKeyRequest(rk)) // finalizer
			Expect(err).NotTo(HaveOccurred())

			res, err := r.Reconcile(ctx, rootKeyRequest(rk))
			Expect(err).NotTo(HaveOccurred(), "a tenant that is not there yet is ordinary, not an error")
			Expect(res.RequeueAfter).To(Equal(pollInterval))
			Expect(backend.createRootKeyCalls).To(BeEmpty(),
				"a key must not be registered before its tenant")

			pending := reloadRootKey("AWSRootKey", rk.GetName())
			ready := meta.FindStatusCondition(*pending.StatusConditions(), readyType)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Reason).To(Equal(reasonTenantPending))
			Expect(meta.FindStatusCondition(*pending.StatusConditions(), providerSyncedType)).To(BeNil(),
				"a pending tenant is not a provider rejection")

			backend.createTenantFn = nil
			reloaded := reconcileToActive(r, rk)
			Expect(reloaded.GetCryptoState().LifecycleState).To(Equal(shared.LifecycleActive))
		})
	})

	Context("when the provider rejects the key", func() {
		It("says so on a distinct condition instead of looking pending", func() {
			r := newReconcilerFor("AWSRootKey")
			rk := newRootKeyFixture("AWSRootKey", "sentinel")

			backend.createRootKeyFn = func(openkcmapi.CreateRootKeyRequest) (*openkcmapi.CreateRootKeyResponse, error) {
				return nil, openkcmapi.NewAPIError(openkcmapi.KindInvalid, 400, "InvalidArgument", "keyUri is not a KMS ARN")
			}

			_, err := r.Reconcile(ctx, rootKeyRequest(rk)) // finalizer
			Expect(err).NotTo(HaveOccurred())
			_, err = r.Reconcile(ctx, rootKeyRequest(rk))
			Expect(err).To(HaveOccurred())

			rejected := reloadRootKey("AWSRootKey", rk.GetName())
			synced := meta.FindStatusCondition(*rejected.StatusConditions(), providerSyncedType)
			Expect(synced).NotTo(BeNil())
			Expect(synced.Status).To(Equal(metav1.ConditionFalse))
			Expect(synced.Reason).To(Equal(reasonProviderRejected))
			Expect(synced.Message).To(ContainSubstring("keyUri is not a KMS ARN"),
				"the operator has to be able to read what the provider objected to")

			ready := meta.FindStatusCondition(*rejected.StatusConditions(), readyType)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		})
	})

	Context("once the key is in the state the spec asks for", func() {
		It("makes no further backend call while the generation is unchanged", func() {
			r := newReconcilerFor("AzureRootKey")
			rk := newRootKeyFixture("AzureRootKey", "sentinel")

			reconcileToActive(r, rk)
			before := len(backend.createRootKeyCalls) + len(backend.getRootKeyCalls) + len(backend.activateKeyCalls)

			_, err := r.Reconcile(ctx, rootKeyRequest(rk))
			Expect(err).NotTo(HaveOccurred())

			after := len(backend.createRootKeyCalls) + len(backend.getRootKeyCalls) + len(backend.activateKeyCalls)
			Expect(after).To(Equal(before),
				"metadata.generation has not moved, so there is nothing to send")
		})
	})

	Context("credential hygiene", func() {
		// The CRDs carry no credential field yet; every spec value is a
		// locator. This pins the invariant now so the day a kpubenc envelope
		// is added, a leak into logs or status fails the build.
		It("never writes a spec value into the log or the status", func() {
			const secret = "kpubenc-do-not-leak-me"

			logs := &bytes.Buffer{}
			logCtx := log.IntoContext(ctx, zap.New(zap.WriteTo(logs), zap.UseDevMode(true)))

			r := newReconcilerFor("OpenBaoRootKey")
			rk := newRootKeyFixture("OpenBaoRootKey", secret)

			for range 3 {
				_, err := r.Reconcile(logCtx, rootKeyRequest(rk))
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(logs.String()).NotTo(ContainSubstring(secret),
				"the controller forwards provider config; it must never log it")

			reloaded := reloadRootKey("OpenBaoRootKey", rk.GetName())
			Expect(statusText(reloaded)).NotTo(ContainSubstring(secret),
				"status is world-readable in the workspace; nothing from the spec belongs there")

			Expect(backend.createRootKeyCalls).To(HaveLen(1))
			Expect(backend.createRootKeyCalls[0].Config["keyName"]).To(Equal(secret),
				"forwarding it to the backend is the whole job")
		})

		It("does not echo a spec value back through a backend error", func() {
			const secret = "kpubenc-do-not-leak-me"

			r := newReconcilerFor("OpenBaoRootKey")
			rk := newRootKeyFixture("OpenBaoRootKey", secret)

			backend.createRootKeyFn = func(openkcmapi.CreateRootKeyRequest) (*openkcmapi.CreateRootKeyResponse, error) {
				return nil, openkcmapi.NewAPIError(openkcmapi.KindInvalid, 400, "InvalidArgument", "rejected")
			}

			_, err := r.Reconcile(ctx, rootKeyRequest(rk))
			Expect(err).NotTo(HaveOccurred())
			_, err = r.Reconcile(ctx, rootKeyRequest(rk))
			Expect(err).To(HaveOccurred())

			Expect(statusText(reloadRootKey("OpenBaoRootKey", rk.GetName()))).NotTo(ContainSubstring(secret))
		})
	})

	Context("on deletion", func() {
		It("deletes the key in the backend before releasing the finalizer", func() {
			r := newReconcilerFor("VaultRootKey")
			rk := newRootKeyFixture("VaultRootKey", "sentinel")
			reconcileToActive(r, rk)

			Expect(k8sClient.Delete(ctx, reloadRootKey("VaultRootKey", rk.GetName()))).To(Succeed())

			_, err := r.Reconcile(ctx, rootKeyRequest(rk))
			Expect(err).NotTo(HaveOccurred())

			Expect(backend.deleteRootKeyCalls).To(Equal([]string{rootKeyID}))

			entry := rootKeyGVK("VaultRootKey")
			err = k8sClient.Get(ctx,
				types.NamespacedName{Name: rk.GetName(), Namespace: rootKeyNamespace}, entry.newEmpty())
			Expect(err).To(HaveOccurred(), "the object must be gone once the finalizer is released")
		})
	})
})

// statusText renders everything the reconciler wrote to status, so a spec value
// that leaked into any message, reason or field is caught wherever it landed.
func statusText(rk RootKey) string {
	var b strings.Builder
	for _, c := range *rk.StatusConditions() {
		fmt.Fprintf(&b, "%s %s %s %s\n", c.Type, c.Status, c.Reason, c.Message)
	}
	if rs := rk.GetReconciliationStatus(); rs != nil {
		fmt.Fprintf(&b, "%s %s %v\n", rs.Message, rs.InternalKeyID, rs.Errors)
		if rs.IdentityInfo != nil {
			fmt.Fprintf(&b, "%s\n", rs.IdentityInfo.Subject)
		}
	}
	if cs := rk.GetCryptoState(); cs != nil {
		fmt.Fprintf(&b, "%s %s\n", cs.ID, cs.LifecycleState)
	}
	return b.String()
}
