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

package operations_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	operations "github.com/openkcm/openkcm-controller/internal/controller/operations"
)

const (
	cascadeNamespace         = "cascade-specs"
	cascadeDEKName           = "dek-a"
	cascadeAccountName       = "acme-prod"
	cascadeDomainKeyTypeTeam = "Team"
)

func ensureCascadeNamespace() {
	GinkgoHelper()
	namespace := &corev1.Namespace{}
	namespace.Name = cascadeNamespace
	err := k8sClient.Create(ctx, namespace)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

func drop(obj client.Object) {
	GinkgoHelper()
	if obj.GetFinalizers() != nil {
		obj.SetFinalizers(nil)
		Expect(client.IgnoreNotFound(
			k8sClient.Update(ctx, obj),
		)).To(Succeed())
	}
	Expect(client.IgnoreNotFound(
		k8sClient.Delete(ctx, obj),
	)).To(Succeed())
}

func cleanupCascadeNamespace() {
	GinkgoHelper()
	dataEncryptionKeys := &operationsv1alpha1.DataEncryptionKeyList{}
	Expect(k8sClient.List(
		ctx,
		dataEncryptionKeys,
		client.InNamespace(cascadeNamespace),
	)).To(Succeed())
	for index := range dataEncryptionKeys.Items {
		drop(&dataEncryptionKeys.Items[index])
	}
	serviceKeys := &operationsv1alpha1.ServiceKeyList{}
	Expect(k8sClient.List(
		ctx,
		serviceKeys,
		client.InNamespace(cascadeNamespace),
	)).To(Succeed())
	for index := range serviceKeys.Items {
		drop(&serviceKeys.Items[index])
	}
	domainKeys := &operationsv1alpha1.DomainKeyList{}
	Expect(k8sClient.List(
		ctx,
		domainKeys,
		client.InNamespace(cascadeNamespace),
	)).To(Succeed())
	for index := range domainKeys.Items {
		drop(&domainKeys.Items[index])
	}
}

var _ = Describe("key-chain cascade", func() {
	var backend operations.Backend

	BeforeEach(func() {
		ensureLogicalCluster(testWorkspace)
		ensureCascadeNamespace()
		backend = &testBackend{}
	})

	AfterEach(cleanupCascadeNamespace)

	It("creates and links a DomainKey when a ServiceKey references none", func() {
		reconciler := &operations.ServiceKeyReconciler{
			APIClient: backend,
			Manager:   newTestManager(),
		}
		serviceKey := &operationsv1alpha1.ServiceKey{}
		serviceKey.Name = "svc-a"
		serviceKey.Namespace = cascadeNamespace
		serviceKey.Spec = operationsv1alpha1.ServiceKeySpec{TenantNameRef: cascadeAccountName}
		Expect(k8sClient.Create(ctx, serviceKey)).To(Succeed())

		for range 2 {
			_, err := reconciler.Reconcile(ctx, requestFor(serviceKey))
			Expect(err).NotTo(HaveOccurred())
		}

		reloaded := &operationsv1alpha1.ServiceKey{}
		Expect(k8sClient.Get(
			ctx,
			types.NamespacedName{Name: "svc-a", Namespace: cascadeNamespace},
			reloaded,
		)).To(Succeed())
		Expect(reloaded.Spec.DomainKeyRef).To(Equal(cascadeAccountName),
			"the cascade must link the ServiceKey to the created DomainKey")

		domainKey := &operationsv1alpha1.DomainKey{}
		Expect(k8sClient.Get(
			ctx,
			types.NamespacedName{Name: cascadeAccountName, Namespace: cascadeNamespace},
			domainKey,
		)).To(Succeed())
	})

	It("reuses the namespace DomainKey instead of creating a second", func() {
		existing := &operationsv1alpha1.DomainKey{}
		existing.Name = "existing-dk"
		existing.Namespace = cascadeNamespace
		existing.Spec = operationsv1alpha1.DomainKeySpec{
			Type:          cascadeDomainKeyTypeTeam,
			TenantNameRef: cascadeAccountName,
		}
		Expect(k8sClient.Create(ctx, existing)).To(Succeed())

		reconciler := &operations.ServiceKeyReconciler{
			APIClient: backend,
			Manager:   newTestManager(),
		}
		serviceKey := &operationsv1alpha1.ServiceKey{}
		serviceKey.Name = "svc-b"
		serviceKey.Namespace = cascadeNamespace
		serviceKey.Spec = operationsv1alpha1.ServiceKeySpec{TenantNameRef: cascadeAccountName}
		Expect(k8sClient.Create(ctx, serviceKey)).To(Succeed())

		for range 2 {
			_, err := reconciler.Reconcile(ctx, requestFor(serviceKey))
			Expect(err).NotTo(HaveOccurred())
		}

		reloaded := &operationsv1alpha1.ServiceKey{}
		Expect(k8sClient.Get(
			ctx,
			types.NamespacedName{Name: "svc-b", Namespace: cascadeNamespace},
			reloaded,
		)).To(Succeed())
		Expect(reloaded.Spec.DomainKeyRef).To(Equal("existing-dk"),
			"the cascade must reuse the namespace's DomainKey, not create a second")

		domainKeys := &operationsv1alpha1.DomainKeyList{}
		Expect(k8sClient.List(
			ctx,
			domainKeys,
			client.InNamespace(cascadeNamespace),
		)).To(Succeed())
		Expect(domainKeys.Items).To(HaveLen(1))
	})

	It("creates and links a ServiceKey when a data key references none", func() {
		reconciler := &operations.DataEncryptionKeyReconciler{
			APIClient: backend,
			Manager:   newTestManager(),
		}
		dataEncryptionKey := &operationsv1alpha1.DataEncryptionKey{}
		dataEncryptionKey.Name = cascadeDEKName
		dataEncryptionKey.Namespace = cascadeNamespace
		dataEncryptionKey.Spec = operationsv1alpha1.DataEncryptionKeySpec{TenantNameRef: cascadeAccountName}
		Expect(k8sClient.Create(ctx, dataEncryptionKey)).To(Succeed())

		for range 2 {
			_, err := reconciler.Reconcile(
				ctx,
				requestFor(dataEncryptionKey),
			)
			Expect(err).NotTo(HaveOccurred())
		}

		reloaded := &operationsv1alpha1.DataEncryptionKey{}
		Expect(k8sClient.Get(
			ctx,
			types.NamespacedName{Name: cascadeDEKName, Namespace: cascadeNamespace},
			reloaded,
		)).To(Succeed())
		Expect(reloaded.Spec.ServiceKeyRef).To(Equal(cascadeDEKName),
			"the cascade must link the data key to the created ServiceKey")

		serviceKey := &operationsv1alpha1.ServiceKey{}
		Expect(k8sClient.Get(
			ctx,
			types.NamespacedName{Name: cascadeDEKName, Namespace: cascadeNamespace},
			serviceKey,
		)).To(Succeed())
		Expect(serviceKey.Spec.DomainKeyRef).To(BeEmpty(),
			"the auto ServiceKey stays unlinked so its own reconciler cascades the DomainKey")
	})

	It("creates the referenced ServiceKey when a data key names one that is missing", func() {
		reconciler := &operations.DataEncryptionKeyReconciler{
			APIClient: backend,
			Manager:   newTestManager(),
		}
		dataEncryptionKey := &operationsv1alpha1.DataEncryptionKey{}
		dataEncryptionKey.Name = "dek-c"
		dataEncryptionKey.Namespace = cascadeNamespace
		dataEncryptionKey.Spec = operationsv1alpha1.DataEncryptionKeySpec{
			TenantNameRef: cascadeAccountName,
			ServiceKeyRef: "named-sk",
		}
		Expect(k8sClient.Create(ctx, dataEncryptionKey)).To(Succeed())

		for range 2 {
			_, err := reconciler.Reconcile(
				ctx,
				requestFor(dataEncryptionKey),
			)
			Expect(err).NotTo(HaveOccurred())
		}

		serviceKey := &operationsv1alpha1.ServiceKey{}
		Expect(k8sClient.Get(
			ctx,
			types.NamespacedName{Name: "named-sk", Namespace: cascadeNamespace},
			serviceKey,
		)).To(Succeed(), "a referenced-but-missing parent must be created, per issue #17")
	})
})
