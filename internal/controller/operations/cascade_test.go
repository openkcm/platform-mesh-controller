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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
)

const (
	cascadeNamespace = "cascade-specs"
	cascadeDEKName   = "dek-a"
)

func ensureCascadeNamespace() {
	GinkgoHelper()
	ns := &corev1.Namespace{}
	ns.Name = cascadeNamespace
	err := k8sClient.Create(ctx, ns)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

func drop(obj client.Object) {
	GinkgoHelper()
	if obj.GetFinalizers() != nil {
		obj.SetFinalizers(nil)
		Expect(client.IgnoreNotFound(k8sClient.Update(ctx, obj))).To(Succeed())
	}
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed())
}

func cleanupCascadeNamespace() {
	GinkgoHelper()
	deks := &operationsv1alpha1.DataEncryptionKeyList{}
	Expect(k8sClient.List(ctx, deks, client.InNamespace(cascadeNamespace))).To(Succeed())
	for i := range deks.Items {
		drop(&deks.Items[i])
	}
	sks := &operationsv1alpha1.ServiceKeyList{}
	Expect(k8sClient.List(ctx, sks, client.InNamespace(cascadeNamespace))).To(Succeed())
	for i := range sks.Items {
		drop(&sks.Items[i])
	}
	dks := &operationsv1alpha1.DomainKeyList{}
	Expect(k8sClient.List(ctx, dks, client.InNamespace(cascadeNamespace))).To(Succeed())
	for i := range dks.Items {
		drop(&dks.Items[i])
	}
}

var _ = Describe("key-chain cascade", func() {
	var backend *fakeBackend

	BeforeEach(func() {
		ensureLogicalCluster(testWorkspace)
		ensureCascadeNamespace()
		backend = &fakeBackend{}
	})

	AfterEach(cleanupCascadeNamespace)

	It("creates and links a DomainKey when a ServiceKey references none", func() {
		reconciler := &ServiceKeyReconciler{APIClient: backend, Manager: newTestManager()}
		sk := &operationsv1alpha1.ServiceKey{}
		sk.Name = "svc-a"
		sk.Namespace = cascadeNamespace
		sk.Spec = operationsv1alpha1.ServiceKeySpec{TenantNameRef: testAccountName}
		Expect(k8sClient.Create(ctx, sk)).To(Succeed())

		for range 2 {
			_, err := reconciler.Reconcile(ctx, requestFor(sk))
			Expect(err).NotTo(HaveOccurred())
		}

		reloaded := &operationsv1alpha1.ServiceKey{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "svc-a", Namespace: cascadeNamespace}, reloaded)).To(Succeed())
		Expect(reloaded.Spec.DomainKeyRef).To(Equal(testAccountName),
			"the cascade must link the ServiceKey to the created DomainKey")

		dk := &operationsv1alpha1.DomainKey{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testAccountName, Namespace: cascadeNamespace}, dk)).To(Succeed())
	})

	It("reuses the namespace DomainKey instead of creating a second", func() {
		existing := &operationsv1alpha1.DomainKey{}
		existing.Name = "existing-dk"
		existing.Namespace = cascadeNamespace
		existing.Spec = operationsv1alpha1.DomainKeySpec{Type: domainKeyTypeTeam, TenantNameRef: testAccountName}
		Expect(k8sClient.Create(ctx, existing)).To(Succeed())

		reconciler := &ServiceKeyReconciler{APIClient: backend, Manager: newTestManager()}
		sk := &operationsv1alpha1.ServiceKey{}
		sk.Name = "svc-b"
		sk.Namespace = cascadeNamespace
		sk.Spec = operationsv1alpha1.ServiceKeySpec{TenantNameRef: testAccountName}
		Expect(k8sClient.Create(ctx, sk)).To(Succeed())

		for range 2 {
			_, err := reconciler.Reconcile(ctx, requestFor(sk))
			Expect(err).NotTo(HaveOccurred())
		}

		reloaded := &operationsv1alpha1.ServiceKey{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "svc-b", Namespace: cascadeNamespace}, reloaded)).To(Succeed())
		Expect(reloaded.Spec.DomainKeyRef).To(Equal("existing-dk"),
			"the cascade must reuse the namespace's DomainKey, not create a second")

		dks := &operationsv1alpha1.DomainKeyList{}
		Expect(k8sClient.List(ctx, dks, client.InNamespace(cascadeNamespace))).To(Succeed())
		Expect(dks.Items).To(HaveLen(1))
	})

	It("creates and links a ServiceKey when a data key references none", func() {
		reconciler := &DataEncryptionKeyReconciler{APIClient: backend, Manager: newTestManager()}
		dek := &operationsv1alpha1.DataEncryptionKey{}
		dek.Name = cascadeDEKName
		dek.Namespace = cascadeNamespace
		dek.Spec = operationsv1alpha1.DataEncryptionKeySpec{TenantNameRef: testAccountName}
		Expect(k8sClient.Create(ctx, dek)).To(Succeed())

		for range 2 {
			_, err := reconciler.Reconcile(ctx, requestFor(dek))
			Expect(err).NotTo(HaveOccurred())
		}

		reloaded := &operationsv1alpha1.DataEncryptionKey{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cascadeDEKName, Namespace: cascadeNamespace}, reloaded)).To(Succeed())
		Expect(reloaded.Spec.ServiceKeyRef).To(Equal(cascadeDEKName),
			"the cascade must link the data key to the created ServiceKey")

		sk := &operationsv1alpha1.ServiceKey{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cascadeDEKName, Namespace: cascadeNamespace}, sk)).To(Succeed())
		Expect(sk.Spec.DomainKeyRef).To(BeEmpty(),
			"the auto ServiceKey stays unlinked so its own reconciler cascades the DomainKey")
	})

	It("creates the referenced ServiceKey when a data key names one that is missing", func() {
		reconciler := &DataEncryptionKeyReconciler{APIClient: backend, Manager: newTestManager()}
		dek := &operationsv1alpha1.DataEncryptionKey{}
		dek.Name = "dek-c"
		dek.Namespace = cascadeNamespace
		dek.Spec = operationsv1alpha1.DataEncryptionKeySpec{TenantNameRef: testAccountName, ServiceKeyRef: "named-sk"}
		Expect(k8sClient.Create(ctx, dek)).To(Succeed())

		for range 2 {
			_, err := reconciler.Reconcile(ctx, requestFor(dek))
			Expect(err).NotTo(HaveOccurred())
		}

		sk := &operationsv1alpha1.ServiceKey{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "named-sk", Namespace: cascadeNamespace}, sk)).To(Succeed(),
			"a referenced-but-missing parent must be created, per issue #17")
	})
})
