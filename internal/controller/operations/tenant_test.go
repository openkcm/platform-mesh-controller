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
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	"github.com/openkcm/openkcm-controller/internal/mockapi"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

const testTenantID = "tenant-uuid"

var tenantCounter int

func newTenant() *operationsv1alpha1.Tenant {
	GinkgoHelper()

	tenantCounter++
	t := &operationsv1alpha1.Tenant{}
	t.Name = fmt.Sprintf("tenant-%d", tenantCounter)
	t.Namespace = defaultTenantNamespace
	Expect(k8sClient.Create(ctx, t)).To(Succeed())
	return t
}

func reloadTenant(name string) *operationsv1alpha1.Tenant {
	GinkgoHelper()

	t := &operationsv1alpha1.Tenant{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: defaultTenantNamespace}, t)).To(Succeed())
	return t
}

func tenantGone(string) (*openkcmapi.GetTenantResponse, error) {
	return nil, openkcmapi.NewAPIError(openkcmapi.KindNotFound, http.StatusNotFound, "tenant_not_found", "no such tenant")
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
			Expect(reloaded.Annotations).To(HaveKeyWithValue(tenantIDAnnotation, testTenantID))
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
			Expect(backend.getTenantCalls).To(Equal([]string{testTenantID}))

			reloaded := reloadTenant(tenant.Name)
			cond := meta.FindStatusCondition(reloaded.Status.Conditions, readyType)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(reloaded.Status.OperationID).To(Equal(testTenantID))
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

	Context("when the backend cannot delete tenants yet", func() {
		BeforeEach(func() {
			backend.noDelete = true
		})

		It("still takes the finalizer", func() {
			tenant := newTenant()

			for range 3 {
				_, err := reconciler.Reconcile(ctx, requestFor(tenant))
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(reloadTenant(tenant.Name).Finalizers).To(ContainElement(tenantFinalizer))
		})

		It("releases the object when the backend cannot delete", func() {
			tenant := newTenant()
			for range 3 {
				_, err := reconciler.Reconcile(ctx, requestFor(tenant))
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(k8sClient.Delete(ctx, reloadTenant(tenant.Name))).To(Succeed())

			_, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())

			Expect(backend.deleteTenantCalls).To(Equal([]string{testTenantID}))

			err = k8sClient.Get(ctx,
				types.NamespacedName{Name: tenant.Name, Namespace: defaultTenantNamespace},
				&operationsv1alpha1.Tenant{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
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

			backend.getTenantFn = tenantGone
			Expect(k8sClient.Delete(ctx, reloadTenant(tenant.Name))).To(Succeed())

			_, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())

			Expect(backend.deleteTenantCalls).To(Equal([]string{testTenantID}))

			err = k8sClient.Get(ctx,
				types.NamespacedName{Name: tenant.Name, Namespace: defaultTenantNamespace},
				&operationsv1alpha1.Tenant{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("releases the finalizer against the mock API once the tenant is gone", func() {
			srv := httptest.NewServer(mockapi.NewServer("").Handler)
			DeferCleanup(srv.Close)
			reconciler.APIClient = openkcmapi.NewClient(srv.URL)

			tenant := newTenant()
			for range 2 {
				_, err := reconciler.Reconcile(ctx, requestFor(tenant))
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(reloadTenant(tenant.Name).Annotations).To(HaveKey(tenantIDAnnotation))

			Expect(k8sClient.Delete(ctx, reloadTenant(tenant.Name))).To(Succeed())
			_, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx,
				types.NamespacedName{Name: tenant.Name, Namespace: defaultTenantNamespace},
				&operationsv1alpha1.Tenant{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("holds the finalizer until the backend stops reporting the tenant", func() {
			tenant := newTenant()
			for range 3 {
				_, err := reconciler.Reconcile(ctx, requestFor(tenant))
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(k8sClient.Delete(ctx, reloadTenant(tenant.Name))).To(Succeed())

			res, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(pollInterval))
			Expect(reloadTenant(tenant.Name).Finalizers).To(ContainElement(tenantFinalizer))

			backend.getTenantFn = tenantGone
			_, err = reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx,
				types.NamespacedName{Name: tenant.Name, Namespace: defaultTenantNamespace},
				&operationsv1alpha1.Tenant{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("keeps the finalizer when the backend cannot confirm the delete", func() {
			tenant := newTenant()
			for range 3 {
				_, err := reconciler.Reconcile(ctx, requestFor(tenant))
				Expect(err).NotTo(HaveOccurred())
			}

			backend.getTenantFn = func(string) (*openkcmapi.GetTenantResponse, error) {
				return nil, openkcmapi.NewAPIError(
					openkcmapi.KindTransient, http.StatusServiceUnavailable, "unavailable", "try later")
			}
			Expect(k8sClient.Delete(ctx, reloadTenant(tenant.Name))).To(Succeed())

			_, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).To(HaveOccurred())
			Expect(reloadTenant(tenant.Name).Finalizers).To(ContainElement(tenantFinalizer))
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

			still := reloadTenant(tenant.Name)
			Expect(still.Finalizers).To(ContainElement(tenantFinalizer))
			Expect(still.DeletionTimestamp).NotTo(BeNil())
		})
	})
})
