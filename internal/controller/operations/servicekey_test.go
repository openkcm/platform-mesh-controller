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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	operationsv1alpha1 "github.com/openkcm/platform-mesh-controller/api/operations/v1alpha1"
	"github.com/openkcm/platform-mesh-controller/api/shared"
	operations "github.com/openkcm/platform-mesh-controller/internal/controller/operations"
	"github.com/openkcm/platform-mesh-controller/internal/openkcmapi"
)

const (
	serviceKeyNamespace   = "servicekey-specs"
	serviceKeyName        = "service-key"
	serviceKeyParentName  = "domain-key"
	serviceKeyParentKeyID = "domain-key-id"
	secondServiceKeyName  = "second-service-key"
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
	newScopedServiceKeyParent(lifecycle, operationsv1alpha1.DomainKeyScopeNamespace)
}

func newInstanceServiceKeyParent(lifecycle shared.LifecycleState) {
	GinkgoHelper()
	newScopedServiceKeyParent(lifecycle, operationsv1alpha1.DomainKeyScopeInstance)
}

func newScopedServiceKeyParent(lifecycle shared.LifecycleState, scope operationsv1alpha1.DomainKeyScope) {
	GinkgoHelper()
	domainKey := &operationsv1alpha1.DomainKey{}
	domainKey.Name = serviceKeyParentName
	domainKey.Namespace = serviceKeyNamespace
	domainKey.Spec = operationsv1alpha1.DomainKeySpec{
		Type:          testDomainKeyTypeTeam,
		Scope:         scope,
		TenantNameRef: testAccountName,
	}
	Expect(k8sClient.Create(ctx, domainKey)).To(Succeed())
	domainKey.Status.CryptoState = &shared.CryptoState{ID: serviceKeyParentKeyID, LifecycleState: lifecycle}
	Expect(k8sClient.Status().Update(ctx, domainKey)).To(Succeed())
}

func newServiceKey() *operationsv1alpha1.ServiceKey {
	GinkgoHelper()
	return newServiceKeyNamed(serviceKeyName)
}

func newServiceKeyNamed(name string) *operationsv1alpha1.ServiceKey {
	GinkgoHelper()
	serviceKey := &operationsv1alpha1.ServiceKey{}
	serviceKey.Name = name
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

	It("gives an Instance domain key to one service key only", func() {
		// given
		newServiceKeyTenant(true)
		newInstanceServiceKeyParent(shared.LifecycleActive)
		drive(reconciler, newServiceKey(), 3)
		second := newServiceKeyNamed(secondServiceKeyName)

		// when
		drive(reconciler, second, 3)

		// then
		Expect(backend.createKeyCalls).To(HaveLen(1))
		reloaded := &operationsv1alpha1.ServiceKey{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(second), reloaded)).To(Succeed())
		ready := meta.FindStatusCondition(reloaded.Status.Conditions, operations.ReadyType)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Reason).To(Equal(operations.ReasonDomainKeyInUse))
	})

	It("hands an Instance domain key to the next service key once the first is deleted", func() {
		// given
		newServiceKeyTenant(true)
		newInstanceServiceKeyParent(shared.LifecycleActive)
		first := newServiceKey()
		drive(reconciler, first, 3)
		second := newServiceKeyNamed(secondServiceKeyName)
		drive(reconciler, second, 2)
		Expect(k8sClient.Delete(ctx, reloadServiceKey())).To(Succeed())
		drive(reconciler, first, 1)

		// when
		drive(reconciler, second, 2)

		// then
		Expect(backend.createKeyCalls).To(HaveLen(2))
		Expect(backend.createKeyCalls[1].Name).To(Equal(operations.ServiceKeyOpenKCMName(second)))
	})

	It("lets a Namespace domain key serve many service keys", func() {
		// given
		newServiceKeyTenant(true)
		newServiceKeyParent(shared.LifecycleActive)
		drive(reconciler, newServiceKey(), 3)
		second := newServiceKeyNamed(secondServiceKeyName)

		// when
		drive(reconciler, second, 3)

		// then
		Expect(backend.createKeyCalls).To(HaveLen(2))
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

func TestInstanceDomainKeyTakenBy(t *testing.T) {
	const orders, billing, ordersKeyID = "orders", "billing", "orders-key"
	instance := &operationsv1alpha1.DomainKey{
		Name:      "orders-db",
		Namespace: serviceKeyNamespace,
		Spec:      operationsv1alpha1.DomainKeySpec{Scope: operationsv1alpha1.DomainKeyScopeInstance},
	}
	namespaced := &operationsv1alpha1.DomainKey{
		Name:      "team",
		Namespace: serviceKeyNamespace,
		Spec:      operationsv1alpha1.DomainKeySpec{Scope: operationsv1alpha1.DomainKeyScopeNamespace},
	}
	unscoped := &operationsv1alpha1.DomainKey{Name: "legacy", Namespace: serviceKeyNamespace}
	serviceKey := func(
		name string, dk *operationsv1alpha1.DomainKey, day int, keyID string,
	) *operationsv1alpha1.ServiceKey {
		sk := &operationsv1alpha1.ServiceKey{
			Name:              name,
			Namespace:         serviceKeyNamespace,
			CreationTimestamp: metav1.Date(2026, 1, day, 0, 0, 0, 0, time.UTC),
			Spec:              operationsv1alpha1.ServiceKeySpec{DomainKeyRef: dk.Name},
		}
		if keyID != "" {
			sk.Status.CryptoState = &shared.CryptoState{ID: keyID}
		}
		return sk
	}
	deleted := metav1.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	leaving := serviceKey(orders, instance, 1, ordersKeyID)
	leaving.DeletionTimestamp = &deleted
	leaving.Finalizers = []string{operations.ServiceKeyFinalizer}

	cases := []struct {
		name  string
		dk    *operationsv1alpha1.DomainKey
		skeys []*operationsv1alpha1.ServiceKey
		asks  string
		want  string
	}{
		{
			name: "the ServiceKey that already has its key keeps an Instance DomainKey",
			dk:   instance,
			skeys: []*operationsv1alpha1.ServiceKey{
				serviceKey(orders, instance, 2, ordersKeyID),
				serviceKey(billing, instance, 1, ""),
			},
			asks: billing,
			want: orders,
		},
		{
			name: "the oldest ServiceKey wins while none has a key",
			dk:   instance,
			skeys: []*operationsv1alpha1.ServiceKey{
				serviceKey(orders, instance, 1, ""),
				serviceKey(billing, instance, 2, ""),
			},
			asks: billing,
			want: orders,
		},
		{
			name: "the winner itself is free to go on",
			dk:   instance,
			skeys: []*operationsv1alpha1.ServiceKey{
				serviceKey(orders, instance, 1, ""),
				serviceKey(billing, instance, 2, ""),
			},
			asks: orders,
			want: "",
		},
		{
			name:  "a ServiceKey being deleted frees the Instance DomainKey",
			dk:    instance,
			skeys: []*operationsv1alpha1.ServiceKey{leaving, serviceKey(billing, instance, 2, "")},
			asks:  billing,
			want:  "",
		},
		{
			name: "a Namespace DomainKey serves every ServiceKey",
			dk:   namespaced,
			skeys: []*operationsv1alpha1.ServiceKey{
				serviceKey(orders, namespaced, 1, ordersKeyID),
				serviceKey(billing, namespaced, 2, ""),
			},
			asks: billing,
			want: "",
		},
		{
			name: "a DomainKey without a scope serves every ServiceKey",
			dk:   unscoped,
			skeys: []*operationsv1alpha1.ServiceKey{
				serviceKey(orders, unscoped, 1, ordersKeyID),
				serviceKey(billing, unscoped, 2, ""),
			},
			asks: billing,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// given
			scheme := runtime.NewScheme()
			require.NoError(t, operationsv1alpha1.AddToScheme(scheme))
			builder := fake.NewClientBuilder().WithScheme(scheme)
			var asking *operationsv1alpha1.ServiceKey
			for _, sk := range tc.skeys {
				builder = builder.WithObjects(sk.DeepCopy())
				if sk.Name == tc.asks {
					asking = sk
				}
			}
			cl := builder.Build()

			// when
			got, err := operations.InstanceDomainKeyTakenBy(t.Context(), cl, asking, tc.dk)

			// then
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
