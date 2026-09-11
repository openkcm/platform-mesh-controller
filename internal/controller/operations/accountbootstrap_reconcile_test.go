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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kcpapisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	"k8s.io/apimachinery/pkg/types"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
)

var bindingCounter int

// newBinding creates an APIBinding in the state the test needs. Every gate in
// Reconcile keys off this object, so each spec differs only in how it is built.
func newBinding(exportName string, phase kcpapisv1alpha2.APIBindingPhaseType) *kcpapisv1alpha2.APIBinding {
	GinkgoHelper()

	bindingCounter++
	b := &kcpapisv1alpha2.APIBinding{}
	b.Name = fmt.Sprintf("binding-%d", bindingCounter)
	b.Spec.Reference.Export = &kcpapisv1alpha2.ExportBindingReference{Name: exportName}
	Expect(k8sClient.Create(ctx, b)).To(Succeed())

	if phase != "" {
		b.Status.Phase = phase
		Expect(k8sClient.Status().Update(ctx, b)).To(Succeed())
	}
	return b
}

func requestForBinding(b *kcpapisv1alpha2.APIBinding) mcreconcile.Request {
	return mcreconcile.Request{
		ClusterName: testClusterName,
		Name:        b.Name,
	}
}

// tenantExists reports whether bootstrap minted the Tenant for the account the
// test workspace resolves to.
func tenantExists() bool {
	GinkgoHelper()

	t := &operationsv1alpha1.Tenant{}
	key := types.NamespacedName{Namespace: defaultTenantNamespace, Name: testAccountName}
	err := k8sClient.Get(ctx, key, t)
	return err == nil
}

func deleteTenantIfPresent() {
	GinkgoHelper()

	t := &operationsv1alpha1.Tenant{}
	key := types.NamespacedName{Namespace: defaultTenantNamespace, Name: testAccountName}
	if err := k8sClient.Get(ctx, key, t); err == nil {
		Expect(k8sClient.Delete(ctx, t)).To(Succeed())
	}
	dk := &operationsv1alpha1.DomainKey{}
	if err := k8sClient.Get(ctx, key, dk); err == nil {
		dk.Finalizers = nil
		Expect(k8sClient.Update(ctx, dk)).To(Succeed())
		Expect(k8sClient.Delete(ctx, dk)).To(Succeed())
	}
}

var _ = Describe("AccountBootstrapReconciler", func() {
	var reconciler *AccountBootstrapReconciler

	BeforeEach(func() {
		ensureLogicalCluster(testWorkspace)
		deleteTenantIfPresent()
		reconciler = &AccountBootstrapReconciler{
			Manager:       newTestManager(),
			DefaultRegion: testRegion,
		}
	})

	Context("when the binding is not ours", func() {
		It("bootstraps nothing", func() {
			b := newBinding("something.else.io", kcpapisv1alpha2.APIBindingPhaseBound)

			res, err := reconciler.Reconcile(ctx, requestForBinding(b))

			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())
			Expect(tenantExists()).To(BeFalse(),
				"a foreign APIExport must not trigger OpenKCM bootstrap")
		})
	})

	Context("when the binding is not Bound yet", func() {
		It("waits instead of bootstrapping against an unusable binding", func() {
			b := newBinding(operationsAPIExportName, kcpapisv1alpha2.APIBindingPhaseBinding)

			res, err := reconciler.Reconcile(ctx, requestForBinding(b))

			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(pollInterval))
			Expect(tenantExists()).To(BeFalse())
		})
	})

	Context("when the binding is being deleted", func() {
		It("skips it rather than writing into a terminating workspace", func() {
			b := newBinding(operationsAPIExportName, kcpapisv1alpha2.APIBindingPhaseBound)

			// A finalizer keeps the object around long enough to be observed
			// with a deletionTimestamp, which is the state Reconcile guards on.
			b.Finalizers = []string{"test.openkcm.io/hold"}
			Expect(k8sClient.Update(ctx, b)).To(Succeed())
			Expect(k8sClient.Delete(ctx, b)).To(Succeed())

			res, err := reconciler.Reconcile(ctx, requestForBinding(b))

			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())
			Expect(tenantExists()).To(BeFalse(),
				"a terminating binding drags terminating CRDs with it; nothing can be created")

			reloaded := &kcpapisv1alpha2.APIBinding{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: b.Name}, reloaded)).To(Succeed())
			reloaded.Finalizers = nil
			Expect(k8sClient.Update(ctx, reloaded)).To(Succeed())
		})
	})

	Context("when the workspace is not an account", func() {
		It("skips org-level workspaces", func() {
			ensureLogicalCluster("root:orgs:acme")
			DeferCleanup(func() { ensureLogicalCluster(testWorkspace) })

			b := newBinding(operationsAPIExportName, kcpapisv1alpha2.APIBindingPhaseBound)

			res, err := reconciler.Reconcile(ctx, requestForBinding(b))

			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())
			Expect(tenantExists()).To(BeFalse())
		})
	})

	Context("when a bound OpenKCM binding lands in an account workspace", func() {
		It("mints the Tenant and the singleton DomainKey", func() {
			b := newBinding(operationsAPIExportName, kcpapisv1alpha2.APIBindingPhaseBound)

			_, err := reconciler.Reconcile(ctx, requestForBinding(b))
			Expect(err).NotTo(HaveOccurred())

			key := types.NamespacedName{Namespace: defaultTenantNamespace, Name: testAccountName}

			tenant := &operationsv1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, key, tenant)).To(Succeed(),
				"the Tenant must be named after the account derived from the workspace path")
			Expect(tenant.Spec.Region).To(Equal(testRegion))

			dk := &operationsv1alpha1.DomainKey{}
			Expect(k8sClient.Get(ctx, key, dk)).To(Succeed())
			Expect(dk.Spec.TenantNameRef).To(Equal(testAccountName))
			Expect(dk.Spec.PrimaryRootKeyRef).To(BeNil(),
				"bootstrap must leave the root key unlinked for the user to choose")
		})

		It("is idempotent across repeated reconciles", func() {
			b := newBinding(operationsAPIExportName, kcpapisv1alpha2.APIBindingPhaseBound)

			for range 3 {
				_, err := reconciler.Reconcile(ctx, requestForBinding(b))
				Expect(err).NotTo(HaveOccurred())
			}

			// Other specs in this suite create their own Tenants, so count
			// only the ones bootstrap is responsible for.
			tenants := &operationsv1alpha1.TenantList{}
			Expect(k8sClient.List(ctx, tenants)).To(Succeed())
			minted := 0
			for _, t := range tenants.Items {
				if t.Name == testAccountName {
					minted++
				}
			}
			Expect(minted).To(Equal(1), "bootstrap must not mint a second Tenant")

			dks := &operationsv1alpha1.DomainKeyList{}
			Expect(k8sClient.List(ctx, dks)).To(Succeed())
			mintedDKs := 0
			for _, dk := range dks.Items {
				if dk.Name == testAccountName {
					mintedDKs++
				}
			}
			Expect(mintedDKs).To(Equal(1), "the DomainKey is a singleton per account")
		})
	})
})
