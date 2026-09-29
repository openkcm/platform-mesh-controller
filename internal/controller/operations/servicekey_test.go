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

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	"github.com/openkcm/openkcm-controller/api/shared"
	operations "github.com/openkcm/openkcm-controller/internal/controller/operations"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

const (
	serviceKeyNamespace   = "servicekey-specs"
	serviceKeyName        = "service-key"
	serviceKeyParentName  = "domain-key"
	serviceKeyParentKeyID = "domain-key-id"
)

func ensureServiceKeyNamespace() {
	GinkgoHelper()
	namespace := &corev1.Namespace{}
	namespace.Name = serviceKeyNamespace
	err := k8sClient.Create(ctx, namespace)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

func newServiceKeyTenant(registered bool) {
	GinkgoHelper()
	tenant := &operationsv1alpha1.Tenant{}
	tenant.Name = testAccountName
	tenant.Namespace = serviceKeyNamespace
	if registered {
		tenant.Annotations = map[string]string{operations.TenantIDAnnotation: testTenantID}
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
}

func newServiceKeyParent(lifecycle shared.LifecycleState) {
	GinkgoHelper()
	domainKey := &operationsv1alpha1.DomainKey{}
	domainKey.Name = serviceKeyParentName
	domainKey.Namespace = serviceKeyNamespace
	domainKey.Spec = operationsv1alpha1.DomainKeySpec{
		Type:          testDomainKeyTypeTeam,
		TenantNameRef: testAccountName,
	}
	Expect(k8sClient.Create(ctx, domainKey)).To(Succeed())
	domainKey.Status.CryptoState = &shared.CryptoState{ID: serviceKeyParentKeyID, LifecycleState: lifecycle}
	Expect(k8sClient.Status().Update(ctx, domainKey)).To(Succeed())
}

func newServiceKey() *operationsv1alpha1.ServiceKey {
	GinkgoHelper()
	serviceKey := &operationsv1alpha1.ServiceKey{}
	serviceKey.Name = serviceKeyName
	serviceKey.Namespace = serviceKeyNamespace
	serviceKey.Spec = operationsv1alpha1.ServiceKeySpec{
		TenantNameRef: testAccountName,
		DomainKeyRef:  serviceKeyParentName,
	}
	Expect(k8sClient.Create(ctx, serviceKey)).To(Succeed())
	return serviceKey
}

func reloadServiceKey() *operationsv1alpha1.ServiceKey {
	GinkgoHelper()
	serviceKey := &operationsv1alpha1.ServiceKey{}
	key := client.ObjectKey{Name: serviceKeyName, Namespace: serviceKeyNamespace}
	Expect(k8sClient.Get(ctx, key, serviceKey)).To(Succeed())
	return serviceKey
}

func cleanupServiceKeyNamespace() {
	GinkgoHelper()
	serviceKeys := &operationsv1alpha1.ServiceKeyList{}
	Expect(k8sClient.List(ctx, serviceKeys, client.InNamespace(serviceKeyNamespace))).To(Succeed())
	for i := range serviceKeys.Items {
		drop(&serviceKeys.Items[i])
	}
	domainKeys := &operationsv1alpha1.DomainKeyList{}
	Expect(k8sClient.List(ctx, domainKeys, client.InNamespace(serviceKeyNamespace))).To(Succeed())
	for i := range domainKeys.Items {
		drop(&domainKeys.Items[i])
	}
	tenants := &operationsv1alpha1.TenantList{}
	Expect(k8sClient.List(ctx, tenants, client.InNamespace(serviceKeyNamespace))).To(Succeed())
	for i := range tenants.Items {
		drop(&tenants.Items[i])
	}
}

var _ = Describe("ServiceKeyReconciler", func() {
	var (
		backend    *testBackend
		reconciler *operations.ServiceKeyReconciler
	)

	BeforeEach(func() {
		ensureLogicalCluster(testWorkspace)
		ensureServiceKeyNamespace()
		backend = &testBackend{}
		reconciler = &operations.ServiceKeyReconciler{
			APIClient:        backend,
			Manager:          newTestManager(),
			AccountNamespace: serviceKeyNamespace,
		}
	})

	AfterEach(cleanupServiceKeyNamespace)

	It("creates its key under the domain key and activates it", func() {
		// given
		newServiceKeyTenant(true)
		newServiceKeyParent(shared.LifecycleActive)
		serviceKey := newServiceKey()

		// when
		drive(reconciler, serviceKey, 4)

		// then
		Expect(backend.createKeyCalls).To(ConsistOf(openkcmapi.CreateKeyRequest{
			TenantID: testTenantID,
			Kind:     "L3",
			Name:     operations.ServiceKeyOpenKCMName(serviceKey),
			ParentID: serviceKeyParentKeyID,
		}))
		Expect(backend.activateKeyCalls).To(ConsistOf(testKeyID))
		reloaded := reloadServiceKey()
		Expect(reloaded.Status.CryptoState.ID).To(Equal(testKeyID))
		Expect(reloaded.Status.CryptoState.LifecycleState).To(Equal(shared.LifecycleActive))
		Expect(meta.IsStatusConditionTrue(reloaded.Status.Conditions, operations.ReadyType)).To(BeTrue())
	})

	It("reports ready when the backend already holds the key active", func() {
		// given
		newServiceKeyTenant(true)
		newServiceKeyParent(shared.LifecycleActive)
		serviceKey := newServiceKey()
		backend.keyLifecycle = shared.LifecycleActive

		// when
		drive(reconciler, serviceKey, 4)

		// then
		Expect(backend.activateKeyCalls).To(BeEmpty())
		reloaded := reloadServiceKey()
		Expect(reloaded.Status.CryptoState.LifecycleState).To(Equal(shared.LifecycleActive))
		Expect(meta.IsStatusConditionTrue(reloaded.Status.Conditions, operations.ReadyType)).To(BeTrue())
	})

	It("keeps the key pre-active while it should be deactivated", func() {
		// given
		newServiceKeyTenant(true)
		newServiceKeyParent(shared.LifecycleActive)
		serviceKey := newServiceKey()
		serviceKey.Spec.Lifecycle = shared.DesiredLifecycleDeactivated
		Expect(k8sClient.Update(ctx, serviceKey)).To(Succeed())

		// when
		drive(reconciler, serviceKey, 4)

		// then
		Expect(backend.activateKeyCalls).To(BeEmpty())
		Expect(reloadServiceKey().Status.CryptoState.LifecycleState).To(Equal(shared.LifecyclePreActive))
	})

	It("reports the failure when the backend cannot deactivate the key", func() {
		// given
		newServiceKeyTenant(true)
		newServiceKeyParent(shared.LifecycleActive)
		serviceKey := newServiceKey()
		drive(reconciler, serviceKey, 3)
		backend.noDeactivate = true
		deactivated := reloadServiceKey()
		deactivated.Spec.Lifecycle = shared.DesiredLifecycleDeactivated
		Expect(k8sClient.Update(ctx, deactivated)).To(Succeed())

		// when
		_, err := reconciler.Reconcile(ctx, requestFor(serviceKey))

		// then
		Expect(err).To(MatchError(errors.ErrUnsupported))
		reloaded := reloadServiceKey()
		Expect(reloaded.Status.CryptoState.LifecycleState).To(Equal(shared.LifecycleActive))
		ready := meta.FindStatusCondition(reloaded.Status.Conditions, operations.ReadyType)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Reason).To(Equal("LifecycleTransitionFailed"))
	})

	It("waits while its domain key is not active", func() {
		// given
		newServiceKeyTenant(true)
		newServiceKeyParent(shared.LifecyclePreActive)
		serviceKey := newServiceKey()

		// when
		drive(reconciler, serviceKey, 3)

		// then
		Expect(backend.createKeyCalls).To(BeEmpty())
		ready := meta.FindStatusCondition(reloadServiceKey().Status.Conditions, operations.ReadyType)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Reason).To(Equal("DomainKeyNotActive"))
	})

	It("waits for the tenant to be registered", func() {
		// given
		newServiceKeyTenant(false)
		newServiceKeyParent(shared.LifecycleActive)
		serviceKey := newServiceKey()

		// when
		drive(reconciler, serviceKey, 3)

		// then
		Expect(backend.createKeyCalls).To(BeEmpty())
		ready := meta.FindStatusCondition(reloadServiceKey().Status.Conditions, operations.ReadyType)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Reason).To(Equal("AwaitingTenant"))
	})

	DescribeTable("drops the finalizer once the backend has dealt with the key",
		func(noDelete bool) {
			// given
			newServiceKeyTenant(true)
			newServiceKeyParent(shared.LifecycleActive)
			serviceKey := newServiceKey()
			drive(reconciler, serviceKey, 3)
			backend.noDelete = noDelete

			// when
			Expect(k8sClient.Delete(ctx, reloadServiceKey())).To(Succeed())
			drive(reconciler, serviceKey, 1)

			// then
			Expect(backend.deleteKeyCalls).To(ConsistOf(testKeyID))
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(serviceKey), &operationsv1alpha1.ServiceKey{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		},
		Entry("the backend deletes it", false),
		Entry("the backend cannot delete keys", true),
	)

	It("keeps the finalizer while the backend fails to delete the key", func() {
		// given
		newServiceKeyTenant(true)
		newServiceKeyParent(shared.LifecycleActive)
		serviceKey := newServiceKey()
		drive(reconciler, serviceKey, 3)
		backend.deleteKeyErr = errors.New("backend unavailable")

		// when
		Expect(k8sClient.Delete(ctx, reloadServiceKey())).To(Succeed())
		_, err := reconciler.Reconcile(ctx, requestFor(serviceKey))

		// then
		Expect(err).To(HaveOccurred())
		Expect(reloadServiceKey().Finalizers).NotTo(BeEmpty())
	})
})

func TestServiceKeyOpenKCMNameIsUniquePerTenant(t *testing.T) {
	// given
	serviceKey := &operationsv1alpha1.ServiceKey{}
	serviceKey.Namespace = "team-a"
	serviceKey.Name = "payments"
	domainKey := &operationsv1alpha1.DomainKey{}
	domainKey.Namespace = serviceKey.Namespace
	domainKey.Name = serviceKey.Name
	elsewhere := serviceKey.DeepCopy()
	elsewhere.Namespace = "team-b"

	// when
	name := operations.ServiceKeyOpenKCMName(serviceKey)

	// then
	assert.Equal(t, "servicekey:team-a.payments", name)
	assert.NotEqual(t, operations.DomainKeyOpenKCMName(domainKey), name, "domain key of the same name")
	assert.NotEqual(t, operations.ServiceKeyOpenKCMName(elsewhere), name, "service key of the same name elsewhere")
}
