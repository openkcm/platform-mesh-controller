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
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operationsv1alpha1 "github.com/openkcm/platform-mesh-controller/api/operations/v1alpha1"
	"github.com/openkcm/platform-mesh-controller/api/shared"
	operations "github.com/openkcm/platform-mesh-controller/internal/controller/operations"
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
		tenant.Annotations = map[string]string{operations.TenantIDAnnotation: testTenantID}
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

func reloadDataEncryptionKey(dek *operationsv1alpha1.DataEncryptionKey) *operationsv1alpha1.DataEncryptionKey {
	GinkgoHelper()

	reloaded := &operationsv1alpha1.DataEncryptionKey{}
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(dek), reloaded)).To(Succeed())
	return reloaded
}

func cleanupDataEncryptionKeyReconciliationObject(obj client.Object) {
	GinkgoHelper()

	stored := obj.DeepCopyObject().(client.Object)
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), stored); err != nil {
		return
	}
	stored.SetFinalizers(nil)
	Expect(k8sClient.Update(ctx, stored)).To(Succeed())
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, stored))).To(Succeed())
}

var _ = Describe("DataEncryptionKeyReconciler", func() {
	var reconciler *operations.DataEncryptionKeyReconciler
	var backend *provisioningBackend

	BeforeEach(func() {
		ensureLogicalCluster(testWorkspace)
		ensureDataEncryptionKeyReconciliationNamespace()
		backend = &provisioningBackend{}
		reconciler = &operations.DataEncryptionKeyReconciler{
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
		Expect(backend.dekRequests[0].Name).To(Equal(operations.DataEncryptionKeyOpenKCMName(dataEncryptionKey)))
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

		Expect(result.RequeueAfter).To(Equal(operations.PollInterval))
		Expect(backend.dekRequests).To(BeEmpty())
		createTenantCalls, _, _ := backend.counts()
		Expect(createTenantCalls).To(BeZero())
		reloaded := &operationsv1alpha1.DataEncryptionKey{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(dataEncryptionKey), reloaded)).To(Succeed())
		ready := meta.FindStatusCondition(reloaded.Status.Conditions, operations.ReadyType)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Reason).To(Equal("AwaitingTenant"))
	})

	It("activates the key once the backend has it ready", func() {
		// given
		newDataEncryptionKeyReconciliationTenant(true)
		newActiveServiceKeyForDataEncryptionKeyReconciliation()
		dataEncryptionKey := newDataEncryptionKeyForReconciliation()

		// when
		drive(reconciler, dataEncryptionKey, 4)

		// then
		Expect(backend.activateKeyCalls).To(ConsistOf(provisionedDataEncryptionKeyID))
		reloaded := reloadDataEncryptionKey(dataEncryptionKey)
		Expect(reloaded.Status.CryptoState.LifecycleState).To(Equal(shared.LifecycleActive))
		Expect(meta.IsStatusConditionTrue(reloaded.Status.Conditions, operations.ReadyType)).To(BeTrue())
	})

	It("reports ready when the backend already holds the key active", func() {
		// given
		newDataEncryptionKeyReconciliationTenant(true)
		newActiveServiceKeyForDataEncryptionKeyReconciliation()
		dataEncryptionKey := newDataEncryptionKeyForReconciliation()
		backend.keyLifecycle = shared.LifecycleActive

		// when
		drive(reconciler, dataEncryptionKey, 4)

		// then
		Expect(backend.activateKeyCalls).To(BeEmpty())
		reloaded := reloadDataEncryptionKey(dataEncryptionKey)
		Expect(reloaded.Status.CryptoState.LifecycleState).To(Equal(shared.LifecycleActive))
		Expect(meta.IsStatusConditionTrue(reloaded.Status.Conditions, operations.ReadyType)).To(BeTrue())
	})

	It("keeps the key pre-active while it should be deactivated", func() {
		// given
		newDataEncryptionKeyReconciliationTenant(true)
		newActiveServiceKeyForDataEncryptionKeyReconciliation()
		dataEncryptionKey := newDataEncryptionKeyForReconciliation()
		dataEncryptionKey.Spec.Lifecycle = shared.DesiredLifecycleDeactivated
		Expect(k8sClient.Update(ctx, dataEncryptionKey)).To(Succeed())

		// when
		drive(reconciler, dataEncryptionKey, 4)

		// then
		Expect(backend.activateKeyCalls).To(BeEmpty())
		reloaded := reloadDataEncryptionKey(dataEncryptionKey)
		Expect(reloaded.Status.CryptoState.LifecycleState).To(Equal(shared.LifecyclePreActive))
	})

	It("reports a key the backend already deactivated as not ready", func() {
		// given
		newDataEncryptionKeyReconciliationTenant(true)
		newActiveServiceKeyForDataEncryptionKeyReconciliation()
		dataEncryptionKey := newDataEncryptionKeyForReconciliation()
		drive(reconciler, dataEncryptionKey, 3)
		backend.keyLifecycle = shared.LifecycleDeactivated
		deactivated := reloadDataEncryptionKey(dataEncryptionKey)
		deactivated.Spec.Lifecycle = shared.DesiredLifecycleDeactivated
		Expect(k8sClient.Update(ctx, deactivated)).To(Succeed())

		// when
		drive(reconciler, dataEncryptionKey, 1)

		// then
		reloaded := reloadDataEncryptionKey(dataEncryptionKey)
		Expect(reloaded.Status.CryptoState.LifecycleState).To(Equal(shared.LifecycleDeactivated))
		Expect(meta.IsStatusConditionTrue(reloaded.Status.Conditions, operations.ReadyType)).To(BeFalse())
	})

	It("reports the failure when the backend cannot deactivate the key", func() {
		// given
		newDataEncryptionKeyReconciliationTenant(true)
		newActiveServiceKeyForDataEncryptionKeyReconciliation()
		dataEncryptionKey := newDataEncryptionKeyForReconciliation()
		drive(reconciler, dataEncryptionKey, 3)
		backend.noDeactivate = true
		deactivated := reloadDataEncryptionKey(dataEncryptionKey)
		deactivated.Spec.Lifecycle = shared.DesiredLifecycleDeactivated
		Expect(k8sClient.Update(ctx, deactivated)).To(Succeed())

		// when
		_, err := reconciler.Reconcile(ctx, requestFor(dataEncryptionKey))

		// then
		Expect(err).To(MatchError(errors.ErrUnsupported))
		reloaded := reloadDataEncryptionKey(dataEncryptionKey)
		Expect(reloaded.Status.CryptoState.LifecycleState).To(Equal(shared.LifecycleActive))
		ready := meta.FindStatusCondition(reloaded.Status.Conditions, operations.ReadyType)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Reason).To(Equal("LifecycleTransitionFailed"))
	})

	DescribeTable("drops the finalizer once the backend has dealt with the key",
		func(noDelete bool) {
			// given
			newDataEncryptionKeyReconciliationTenant(true)
			newActiveServiceKeyForDataEncryptionKeyReconciliation()
			dataEncryptionKey := newDataEncryptionKeyForReconciliation()
			drive(reconciler, dataEncryptionKey, 3)
			backend.noDelete = noDelete

			// when
			Expect(k8sClient.Delete(ctx, reloadDataEncryptionKey(dataEncryptionKey))).To(Succeed())
			drive(reconciler, dataEncryptionKey, 1)

			// then
			Expect(backend.deleteKeyCalls).To(ConsistOf(provisionedDataEncryptionKeyID))
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dataEncryptionKey), &operationsv1alpha1.DataEncryptionKey{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		},
		Entry("the backend deletes it", false),
		Entry("the backend cannot delete keys", true),
	)

	It("keeps the finalizer while the backend fails to delete the key", func() {
		// given
		newDataEncryptionKeyReconciliationTenant(true)
		newActiveServiceKeyForDataEncryptionKeyReconciliation()
		dataEncryptionKey := newDataEncryptionKeyForReconciliation()
		drive(reconciler, dataEncryptionKey, 3)
		backend.deleteKeyErr = errors.New("backend unavailable")

		// when
		Expect(k8sClient.Delete(ctx, reloadDataEncryptionKey(dataEncryptionKey))).To(Succeed())
		_, err := reconciler.Reconcile(ctx, requestFor(dataEncryptionKey))

		// then
		Expect(err).To(HaveOccurred())
		Expect(reloadDataEncryptionKey(dataEncryptionKey).Finalizers).NotTo(BeEmpty())
	})
})

func TestDataEncryptionKeyOpenKCMNameIsUniquePerTenant(t *testing.T) {
	// given
	dataEncryptionKey := &operationsv1alpha1.DataEncryptionKey{}
	dataEncryptionKey.Namespace = "team-a"
	dataEncryptionKey.Name = "orders"
	serviceKey := &operationsv1alpha1.ServiceKey{}
	serviceKey.Namespace = dataEncryptionKey.Namespace
	serviceKey.Name = dataEncryptionKey.Name
	elsewhere := dataEncryptionKey.DeepCopy()
	elsewhere.Namespace = "team-b"

	// when
	name := operations.DataEncryptionKeyOpenKCMName(dataEncryptionKey)

	// then
	assert.Equal(t, "dataencryptionkey:team-a.orders", name)
	assert.NotEqual(t, operations.ServiceKeyOpenKCMName(serviceKey), name, "service key of the same name")
	assert.NotEqual(t, operations.DataEncryptionKeyOpenKCMName(elsewhere), name, "same name in another namespace")
}
