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
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	"github.com/openkcm/openkcm-controller/api/shared"
)

const (
	dataEncryptionKeyReconciliationNamespace = "dataencryptionkey-reconciliation-specs"
	dataEncryptionKeyServiceKeyName          = "service-key"
)

func ensureDataEncryptionKeyReconciliationNamespace() {
	GinkgoHelper()

	namespace := &corev1.Namespace{}
	namespace.Name = dataEncryptionKeyReconciliationNamespace
	err := k8sClient.Create(ctx, namespace)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

func newDataEncryptionKeyReconciliationTenant(recorded bool) {
	GinkgoHelper()

	tenant := &operationsv1alpha1.Tenant{}
	tenant.Name = testAccountName
	tenant.Namespace = dataEncryptionKeyReconciliationNamespace
	if recorded {
		tenant.Annotations = map[string]string{tenantIDAnnotation: testTenantID}
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
}

func newActiveServiceKeyForDataEncryptionKeyReconciliation() {
	GinkgoHelper()

	serviceKey := &operationsv1alpha1.ServiceKey{}
	serviceKey.Name = dataEncryptionKeyServiceKeyName
	serviceKey.Namespace = dataEncryptionKeyReconciliationNamespace
	serviceKey.Spec = operationsv1alpha1.ServiceKeySpec{
		TenantNameRef: testAccountName,
	}
	Expect(k8sClient.Create(ctx, serviceKey)).To(Succeed())
	serviceKey.Status.CryptoState = &shared.CryptoState{
		ID:             "service-key-id",
		LifecycleState: shared.LifecycleActive,
	}
	Expect(k8sClient.Status().Update(ctx, serviceKey)).To(Succeed())
}

func newDataEncryptionKeyForReconciliation() *operationsv1alpha1.DataEncryptionKey {
	GinkgoHelper()

	dataEncryptionKey := &operationsv1alpha1.DataEncryptionKey{}
	dataEncryptionKey.Name = "data-encryption-key"
	dataEncryptionKey.Namespace = dataEncryptionKeyReconciliationNamespace
	dataEncryptionKey.Spec = operationsv1alpha1.DataEncryptionKeySpec{
		TenantNameRef: testAccountName,
		ServiceKeyRef: dataEncryptionKeyServiceKeyName,
		KMIP: &operationsv1alpha1.DataEncryptionKeyKMIP{
			Attributes: map[string]string{"purpose": "database"},
		},
	}
	Expect(k8sClient.Create(ctx, dataEncryptionKey)).To(Succeed())
	return dataEncryptionKey
}

func cleanupDataEncryptionKeyReconciliationObject(obj client.Object) {
	GinkgoHelper()

	stored := obj.DeepCopyObject().(client.Object)
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), stored); err != nil {
		return
	}
	stored.SetFinalizers(nil)
	Expect(k8sClient.Update(ctx, stored)).To(Succeed())
	Expect(k8sClient.Delete(ctx, stored)).To(Succeed())
}

var _ = Describe("DataEncryptionKeyReconciler", func() {
	var reconciler *DataEncryptionKeyReconciler
	var backend *provisioningBackend

	BeforeEach(func() {
		ensureLogicalCluster(testWorkspace)
		ensureDataEncryptionKeyReconciliationNamespace()
		backend = &provisioningBackend{}
		reconciler = &DataEncryptionKeyReconciler{
			APIClient:        backend,
			Manager:          newTestManager(),
			AccountNamespace: dataEncryptionKeyReconciliationNamespace,
		}
	})

	AfterEach(func() {
		dataEncryptionKey := &operationsv1alpha1.DataEncryptionKey{}
		dataEncryptionKey.Name = "data-encryption-key"
		dataEncryptionKey.Namespace = dataEncryptionKeyReconciliationNamespace
		cleanupDataEncryptionKeyReconciliationObject(dataEncryptionKey)

		serviceKey := &operationsv1alpha1.ServiceKey{}
		serviceKey.Name = dataEncryptionKeyServiceKeyName
		serviceKey.Namespace = dataEncryptionKeyReconciliationNamespace
		cleanupDataEncryptionKeyReconciliationObject(serviceKey)

		tenant := &operationsv1alpha1.Tenant{}
		tenant.Name = testAccountName
		tenant.Namespace = dataEncryptionKeyReconciliationNamespace
		cleanupDataEncryptionKeyReconciliationObject(tenant)
	})

	It("provisions a data encryption key for the recorded Tenant identity", func() {
		newDataEncryptionKeyReconciliationTenant(true)
		newActiveServiceKeyForDataEncryptionKeyReconciliation()
		dataEncryptionKey := newDataEncryptionKeyForReconciliation()

		_, err := reconciler.Reconcile(ctx, requestFor(dataEncryptionKey))
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, requestFor(dataEncryptionKey))
		Expect(err).NotTo(HaveOccurred())

		Expect(backend.dekRequests).To(HaveLen(1))
		Expect(backend.dekRequests[0].TenantID).To(Equal(testTenantID))
		Expect(backend.dekRequests[0].ServiceKeyID).To(Equal("service-key-id"))
		Expect(backend.dekRequests[0].KMIPAttributes).To(Equal(map[string]string{"purpose": "database"}))
		createTenantCalls, _, _ := backend.counts()
		Expect(createTenantCalls).To(BeZero())
	})

	It("reports AwaitingTenant and requeues until Tenant reconciliation records an identity", func() {
		newDataEncryptionKeyReconciliationTenant(false)
		newActiveServiceKeyForDataEncryptionKeyReconciliation()
		dataEncryptionKey := newDataEncryptionKeyForReconciliation()

		_, err := reconciler.Reconcile(ctx, requestFor(dataEncryptionKey))
		Expect(err).NotTo(HaveOccurred())
		result, err := reconciler.Reconcile(ctx, requestFor(dataEncryptionKey))
		Expect(err).NotTo(HaveOccurred())

		Expect(result.RequeueAfter).To(Equal(pollInterval))
		Expect(backend.dekRequests).To(BeEmpty())
		createTenantCalls, _, _ := backend.counts()
		Expect(createTenantCalls).To(BeZero())
		reloaded := &operationsv1alpha1.DataEncryptionKey{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(dataEncryptionKey), reloaded)).To(Succeed())
		ready := meta.FindStatusCondition(reloaded.Status.Conditions, readyType)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Reason).To(Equal("AwaitingTenant"))
	})
})
