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
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	"sigs.k8s.io/multicluster-runtime/providers/single"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

const (
	testClusterName = "test-workspace"
	testAccountName = "acme-prod"
	testWorkspace   = "root:orgs:acme:" + testAccountName
)

// newTestManager wires a multicluster manager over the envtest API server. The
// client is deliberately uncached: nothing starts the manager in these tests,
// so a cache-backed reader would never sync.
func newTestManager() mcmanager.Manager {
	GinkgoHelper()

	cl, err := cluster.New(cfg, func(o *cluster.Options) {
		o.Scheme = scheme.Scheme
		o.NewClient = func(c *rest.Config, opts client.Options) (client.Client, error) {
			// cluster.New injects the informer cache as the read source; drop
			// it so reads hit the API server directly. Nothing starts this
			// manager, so a cache-backed reader would never sync.
			opts.Cache = nil
			return client.New(c, opts)
		}
	})
	Expect(err).NotTo(HaveOccurred())

	mgr, err := mcmanager.New(cfg, single.New(testClusterName, cl), manager.Options{
		Scheme:         scheme.Scheme,
		Metrics:        metricsserver.Options{BindAddress: "0"},
		LeaderElection: false,
	})
	Expect(err).NotTo(HaveOccurred())
	return mgr
}

// ensureLogicalCluster creates the object path.go derives the account name
// from. Named "cluster" because that is the only name kcp ever uses.
func ensureLogicalCluster(path string) {
	GinkgoHelper()

	lc := &kcpcorev1alpha1.LogicalCluster{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: "cluster"}, lc)
	if err == nil {
		lc.Annotations = map[string]string{pathAnnotation: path}
		Expect(k8sClient.Update(ctx, lc)).To(Succeed())
		return
	}

	lc = &kcpcorev1alpha1.LogicalCluster{}
	lc.Name = "cluster"
	lc.Annotations = map[string]string{pathAnnotation: path}
	Expect(k8sClient.Create(ctx, lc)).To(Succeed())
}

var tenantCounter int

func newTenant() *operationsv1alpha1.Tenant {
	GinkgoHelper()

	tenantCounter++
	t := &operationsv1alpha1.Tenant{}
	t.Name = fmt.Sprintf("tenant-%d", tenantCounter)
	t.Namespace = "default"
	Expect(k8sClient.Create(ctx, t)).To(Succeed())
	return t
}

func reloadTenant(name string) *operationsv1alpha1.Tenant {
	GinkgoHelper()

	t := &operationsv1alpha1.Tenant{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, t)).To(Succeed())
	return t
}

func requestFor(t *operationsv1alpha1.Tenant) mcreconcile.Request {
	return mcreconcile.Request{
		ClusterName: testClusterName,
		Request: reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      t.Name,
			Namespace: t.Namespace,
		}},
	}
}

var _ = Describe("TenantReconciler", func() {
	var (
		backend    *fakeBackend
		reconciler *TenantReconciler
	)

	BeforeEach(func() {
		ensureLogicalCluster(testWorkspace)
		backend = &fakeBackend{}
		reconciler = &TenantReconciler{APIClient: backend, Manager: newTestManager()}
	})

	Context("when a Tenant is first seen", func() {
		It("adds the finalizer before touching the backend", func() {
			tenant := newTenant()

			res, err := reconciler.Reconcile(ctx, requestFor(tenant))

			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())

			create, get, del := backend.counts()
			Expect(create).To(Equal(0), "backend must not be called before the finalizer is persisted")
			Expect(get).To(Equal(0))
			Expect(del).To(Equal(0))

			Expect(reloadTenant(tenant.Name).Finalizers).To(ContainElement(tenantFinalizer))
		})
	})

	Context("on the create pass", func() {
		It("registers the tenant under the path-derived account name", func() {
			tenant := newTenant()
			_, err := reconciler.Reconcile(ctx, requestFor(tenant)) // finalizer
			Expect(err).NotTo(HaveOccurred())

			res, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(pollInterval))

			Expect(backend.createTenantCalls).To(Equal([]string{testAccountName}),
				"the account name must come from the workspace path, not metadata.name")

			reloaded := reloadTenant(tenant.Name)
			Expect(reloaded.Annotations).To(HaveKeyWithValue(tenantIDAnnotation, "tenant-uuid"))
			cond := meta.FindStatusCondition(reloaded.Status.Conditions, readyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(reasonProcess))
		})

		It("surfaces a backend failure on the object instead of only logging it", func() {
			tenant := newTenant()
			_, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())

			backend.createTenantFn = func(openkcmapi.CreateTenantRequest) (*openkcmapi.CreateTenantResponse, error) {
				return nil, errors.New("krypton unavailable")
			}

			_, err = reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).To(HaveOccurred())

			cond := meta.FindStatusCondition(reloadTenant(tenant.Name).Status.Conditions, readyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(reasonFailed))
			Expect(cond.Message).To(ContainSubstring("krypton unavailable"))
		})
	})

	Context("on the readiness pass", func() {
		It("keeps polling while the backend is still processing", func() {
			tenant := newTenant()
			_, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())

			backend.getTenantFn = func(id string) (*openkcmapi.GetTenantResponse, error) {
				return &openkcmapi.GetTenantResponse{ID: id, ProcessingState: "processing"}, nil
			}

			res, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(pollInterval))

			cond := meta.FindStatusCondition(reloadTenant(tenant.Name).Status.Conditions, readyType)
			Expect(cond.Reason).To(Equal(reasonProcess), "must not go Ready before the backend says so")
		})

		It("goes Ready once the backend reports the tenant provisioned", func() {
			tenant := newTenant()
			_, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())

			res, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())
			Expect(backend.getTenantCalls).To(Equal([]string{"tenant-uuid"}))

			reloaded := reloadTenant(tenant.Name)
			cond := meta.FindStatusCondition(reloaded.Status.Conditions, readyType)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(reloaded.Status.OperationID).To(Equal("tenant-uuid"))
			Expect(reloaded.Status.ObservedGeneration).To(Equal(reloaded.Generation))
		})

		It("does not create a second tenant once the first is Ready", func() {
			tenant := newTenant()
			for range 3 {
				_, err := reconciler.Reconcile(ctx, requestFor(tenant))
				Expect(err).NotTo(HaveOccurred())
			}

			_, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())

			create, _, _ := backend.counts()
			Expect(create).To(Equal(1), "a settled Tenant must not re-register with the backend")
		})
	})

	Context("when the backend cannot delete tenants", func() {
		BeforeEach(func() {
			backend.noDelete = true
		})

		It("never takes a finalizer it cannot release", func() {
			tenant := newTenant()

			for range 3 {
				_, err := reconciler.Reconcile(ctx, requestFor(tenant))
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(reloadTenant(tenant.Name).Finalizers).NotTo(ContainElement(tenantFinalizer),
				"a finalizer against a backend with no delete wedges the object in Terminating")
		})

		It("lets the object be deleted straight away", func() {
			tenant := newTenant()
			for range 3 {
				_, err := reconciler.Reconcile(ctx, requestFor(tenant))
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(k8sClient.Delete(ctx, reloadTenant(tenant.Name))).To(Succeed())

			err := k8sClient.Get(ctx,
				types.NamespacedName{Name: tenant.Name, Namespace: "default"},
				&operationsv1alpha1.Tenant{})
			Expect(err).To(HaveOccurred(), "nothing should hold the object back")
		})
	})

	Context("when a registration is already recorded", func() {
		It("resumes from the stored id instead of registering again", func() {
			tenant := newTenant()
			_, err := reconciler.Reconcile(ctx, requestFor(tenant)) // finalizer
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, requestFor(tenant)) // create
			Expect(err).NotTo(HaveOccurred())

			// Simulate the status write failing after the annotation landed:
			// the id is on the object, but no condition says Processing.
			stripped := reloadTenant(tenant.Name)
			stripped.Status.Conditions = nil
			Expect(k8sClient.Status().Update(ctx, stripped)).To(Succeed())

			_, err = reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())

			create, get, _ := backend.counts()
			Expect(create).To(Equal(1),
				"a recorded tenant id must never lead to a second CreateTenant")
			Expect(get).To(BeNumerically(">=", 1), "it must resume via GetTenant instead")
		})

		It("does not register again when the spec changes after Ready", func() {
			tenant := newTenant()
			for range 3 {
				_, err := reconciler.Reconcile(ctx, requestFor(tenant))
				Expect(err).NotTo(HaveOccurred())
			}

			// A user edits the spec: generation moves past observedGeneration
			// and the steady-state guard no longer short-circuits.
			edited := reloadTenant(tenant.Name)
			edited.Spec.Region = "eu-west"
			Expect(k8sClient.Update(ctx, edited)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())

			create, _, _ := backend.counts()
			Expect(create).To(Equal(1), "editing the spec must not mint a second tenant")
		})
	})

	Context("on deletion", func() {
		It("removes the tenant from the backend and releases the finalizer", func() {
			tenant := newTenant()
			for range 3 {
				_, err := reconciler.Reconcile(ctx, requestFor(tenant))
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(k8sClient.Delete(ctx, reloadTenant(tenant.Name))).To(Succeed())

			_, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())

			Expect(backend.deleteTenantCalls).To(Equal([]string{"tenant-uuid"}))

			err = k8sClient.Get(ctx,
				types.NamespacedName{Name: tenant.Name, Namespace: "default"},
				&operationsv1alpha1.Tenant{})
			Expect(err).To(HaveOccurred(), "the object should be gone once the finalizer is released")
		})

		It("holds the finalizer while the backend refuses the delete", func() {
			tenant := newTenant()
			for range 3 {
				_, err := reconciler.Reconcile(ctx, requestFor(tenant))
				Expect(err).NotTo(HaveOccurred())
			}

			backend.deleteTenantFn = func(string) error {
				return errors.New("tenant_has_keys")
			}
			Expect(k8sClient.Delete(ctx, reloadTenant(tenant.Name))).To(Succeed())

			_, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).To(HaveOccurred())

			// This is the behaviour that wedges a Tenant forever once Krypton
			// has no DeleteTenant at all: the finalizer is only released after
			// the backend call succeeds. See krypton#145.
			still := reloadTenant(tenant.Name)
			Expect(still.Finalizers).To(ContainElement(tenantFinalizer))
			Expect(still.DeletionTimestamp).NotTo(BeNil())
		})
	})
})
