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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	"github.com/openkcm/openkcm-controller/api/shared"
)

// domainKeyNamespace isolates these specs: the DomainKey singleton is scoped
// per namespace, so a dedicated one keeps parallel specs from colliding. It is
// also the account namespace here, so the registered Tenant lives in it.
const domainKeyNamespace = "domainkey-specs"

func ensureDomainKeyNamespace() {
	GinkgoHelper()
	ns := &corev1.Namespace{}
	ns.Name = domainKeyNamespace
	err := k8sClient.Create(ctx, ns)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

// ensureRegisteredTenant mimics what the Tenant reconciler leaves behind: the
// account's Tenant carrying the backend tenant id, which the DomainKey
// reconciler reads instead of registering the tenant itself.
func ensureRegisteredTenant() {
	GinkgoHelper()
	tenant := &operationsv1alpha1.Tenant{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: testAccountName, Namespace: domainKeyNamespace}, tenant)
	if apierrors.IsNotFound(err) {
		tenant = &operationsv1alpha1.Tenant{}
		tenant.Name = testAccountName
		tenant.Namespace = domainKeyNamespace
		tenant.Annotations = map[string]string{tenantIDAnnotation: testTenantID}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
		return
	}
	Expect(err).NotTo(HaveOccurred())
	tenant.Annotations = map[string]string{tenantIDAnnotation: testTenantID}
	Expect(k8sClient.Update(ctx, tenant)).To(Succeed())
}

var domainKeyRootCounter int

// newActiveRootKey creates an AWSRootKey already registered and active.
func newActiveRootKey() *operationsv1alpha1.AWSRootKey {
	GinkgoHelper()
	domainKeyRootCounter++

	rk := &operationsv1alpha1.AWSRootKey{}
	rk.Name = fmt.Sprintf("root-%d", domainKeyRootCounter)
	rk.Namespace = domainKeyNamespace
	rk.Spec = operationsv1alpha1.AWSRootKeySpec{
		TenantNameRef: testAccountName,
		Region:        "eu-central-1",
		KeyURI:        "arn:aws:kms:eu-central-1:000000000000:key/abc",
		RolesAnywhere: operationsv1alpha1.AWSRolesAnywhereConfig{
			TrustAnchorARN: "arn:aws:rolesanywhere:eu-central-1:000000000000:trust-anchor/a",
			ProfileARN:     "arn:aws:rolesanywhere:eu-central-1:000000000000:profile/p",
			RoleARN:        "arn:aws:iam::000000000000:role/r",
		},
	}
	Expect(k8sClient.Create(ctx, rk)).To(Succeed())

	rk.Status.CryptoState = &shared.CryptoState{ID: "root-key-id", LifecycleState: shared.LifecycleActive}
	Expect(k8sClient.Status().Update(ctx, rk)).To(Succeed())
	return rk
}

func newDomainKey(rootKeyName string) *operationsv1alpha1.DomainKey {
	GinkgoHelper()

	dk := &operationsv1alpha1.DomainKey{}
	dk.Name = "domain-key"
	dk.Namespace = domainKeyNamespace
	dk.Spec = operationsv1alpha1.DomainKeySpec{
		Type:          "Team",
		TenantNameRef: testAccountName,
	}
	if rootKeyName != "" {
		dk.Spec.PrimaryRootKeyRef = &shared.TypedReference{
			APIGroup:  operationsv1alpha1.GroupVersion.Group,
			Kind:      testAWSRootKeyKind,
			Namespace: domainKeyNamespace,
			Name:      rootKeyName,
		}
	}
	Expect(k8sClient.Create(ctx, dk)).To(Succeed())
	return dk
}

func reloadDomainKey(name string) *operationsv1alpha1.DomainKey {
	GinkgoHelper()
	dk := &operationsv1alpha1.DomainKey{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: domainKeyNamespace}, dk)).To(Succeed())
	return dk
}

func drive(reconciler *DomainKeyReconciler, dk *operationsv1alpha1.DomainKey, passes int) {
	GinkgoHelper()
	for range passes {
		_, err := reconciler.Reconcile(ctx, requestFor(dk))
		Expect(err).NotTo(HaveOccurred())
	}
}

// cleanupDomainKeyObject drops any leftover object so specs stay isolated: the
// DomainKey singleton and the account Tenant both live under one fixed name.
func cleanupDomainKeyObject(obj client.Object, name string) {
	GinkgoHelper()
	obj.SetName(name)
	obj.SetNamespace(domainKeyNamespace)
	got := obj.DeepCopyObject().(client.Object)
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), got); err != nil {
		return
	}
	got.SetFinalizers(nil)
	Expect(k8sClient.Update(ctx, got)).To(Succeed())
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, got))).To(Succeed())
}

var _ = Describe("DomainKeyReconciler", func() {
	var (
		backend    *fakeBackend
		reconciler *DomainKeyReconciler
	)

	BeforeEach(func() {
		ensureLogicalCluster(testWorkspace)
		ensureDomainKeyNamespace()
		backend = &fakeBackend{}
		reconciler = &DomainKeyReconciler{
			APIClient:        backend,
			Manager:          newTestManager(),
			AccountNamespace: domainKeyNamespace,
		}
	})

	AfterEach(func() {
		cleanupDomainKeyObject(&operationsv1alpha1.DomainKey{}, "domain-key")
		cleanupDomainKeyObject(&operationsv1alpha1.Tenant{}, testAccountName)
	})

	It("adds the finalizer before touching the backend", func() {
		ensureRegisteredTenant()
		root := newActiveRootKey()
		dk := newDomainKey(root.Name)

		_, err := reconciler.Reconcile(ctx, requestFor(dk))
		Expect(err).NotTo(HaveOccurred())

		Expect(backend.createKeyCalls).To(BeEmpty(), "the backend must not be called before the finalizer is persisted")
		Expect(reloadDomainKey(dk.Name).Finalizers).To(ContainElement(domainKeyFinalizer))
	})

	It("registers and activates the key once its root is active", func() {
		ensureRegisteredTenant()
		root := newActiveRootKey()
		dk := newDomainKey(root.Name)

		drive(reconciler, dk, 4)

		Expect(backend.createKeyCalls).To(HaveLen(1))
		Expect(backend.createKeyCalls[0].TenantID).To(Equal(testTenantID),
			"the key must be created under the tenant the Tenant reconciler registered")
		Expect(backend.createKeyCalls[0].Kind).To(Equal("L2"))
		Expect(backend.activateKeyCalls).To(ContainElement(fakeKeyID))

		reloaded := reloadDomainKey(dk.Name)
		Expect(reloaded.Status.CryptoState).NotTo(BeNil())
		Expect(reloaded.Status.CryptoState.ID).To(Equal(fakeKeyID))
		Expect(reloaded.Status.CryptoState.LifecycleState).To(Equal(shared.LifecycleActive))
		ready := meta.FindStatusCondition(reloaded.Status.Conditions, readyType)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionTrue))
	})

	It("creates the key on the default root when none is linked", func() {
		ensureRegisteredTenant()
		dk := newDomainKey("")

		drive(reconciler, dk, 4)

		Expect(backend.createKeyCalls).To(HaveLen(1),
			"an unlinked domain key uses the backend default root, it does not wait")
		Expect(reloadDomainKey(dk.Name).Status.CryptoState.LifecycleState).To(Equal(shared.LifecycleActive))
	})

	It("waits for the tenant to be registered before creating a key", func() {
		root := newActiveRootKey()
		dk := newDomainKey(root.Name)

		drive(reconciler, dk, 3)

		Expect(backend.createKeyCalls).To(BeEmpty(),
			"without a registered tenant there is nothing to hang the key on")
		ready := meta.FindStatusCondition(reloadDomainKey(dk.Name).Status.Conditions, readyType)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
	})

	It("waits for a non-active root key instead of registering a key", func() {
		ensureRegisteredTenant()
		root := newActiveRootKey()
		root.Status.CryptoState = &shared.CryptoState{LifecycleState: shared.LifecyclePreActive}
		Expect(k8sClient.Status().Update(ctx, root)).To(Succeed())
		dk := newDomainKey(root.Name)

		drive(reconciler, dk, 3)

		Expect(backend.createKeyCalls).To(BeEmpty(),
			"a linked root that is not active yet blocks the domain key")
	})
})
