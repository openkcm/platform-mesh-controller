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
	"context"
	"errors"
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operationsv1alpha1 "github.com/openkcm/platform-mesh-controller/api/operations/v1alpha1"
	"github.com/openkcm/platform-mesh-controller/api/shared"
	operations "github.com/openkcm/platform-mesh-controller/internal/controller/operations"
	"github.com/openkcm/platform-mesh-controller/internal/openkcmapi"
)

type providerCall struct {
	rootKeyID string
	request   openkcmapi.CreateProviderRequest
}

type kryptonLikeBackend struct {
	testBackend

	state sync.Mutex

	l1Lifecycle   shared.LifecycleState
	l1Processing  string
	l2Lifecycle   shared.LifecycleState
	l2Processing  string
	l1ActivateErr error

	providerCalls []providerCall
	removeCalls   []string
	l1Activations []string
	l2Activations []string
}

func newKryptonLikeBackend() *kryptonLikeBackend {
	return &kryptonLikeBackend{
		l1Lifecycle:  shared.LifecyclePreActive,
		l1Processing: openkcmapi.ProcessingStateReady,
		l2Lifecycle:  shared.LifecyclePreActive,
		l2Processing: openkcmapi.ProcessingStateReady,
	}
}

func (b *kryptonLikeBackend) completeL1() {
	b.state.Lock()
	defer b.state.Unlock()
	b.l1Lifecycle, b.l1Processing = shared.LifecycleActive, openkcmapi.ProcessingStateReady
}

func (b *kryptonLikeBackend) completeL2() {
	b.state.Lock()
	defer b.state.Unlock()
	b.l2Lifecycle, b.l2Processing = shared.LifecycleActive, openkcmapi.ProcessingStateReady
}

func (b *kryptonLikeBackend) GetRootKey(_ context.Context, id string) (*openkcmapi.GetRootKeyResponse, error) {
	b.state.Lock()
	defer b.state.Unlock()
	return &openkcmapi.GetRootKeyResponse{
		ID: id, ProcessingState: b.l1Processing, LifecycleState: string(b.l1Lifecycle),
	}, nil
}

func (b *kryptonLikeBackend) GetKey(_ context.Context, id string) (*openkcmapi.GetKeyResponse, error) {
	b.state.Lock()
	defer b.state.Unlock()
	return &openkcmapi.GetKeyResponse{ID: id, ProcessingState: b.l2Processing, LifecycleState: string(b.l2Lifecycle)}, nil
}

func (b *kryptonLikeBackend) CreateProvider(
	_ context.Context, id string, req openkcmapi.CreateProviderRequest,
) (*openkcmapi.GetRootKeyResponse, error) {
	b.state.Lock()
	defer b.state.Unlock()
	if b.l1Lifecycle != shared.LifecyclePreActive {
		return nil, openkcmapi.NewAPIError(openkcmapi.KindTransient, 500, "Internal", "internal error")
	}
	b.providerCalls = append(b.providerCalls, providerCall{rootKeyID: id, request: req})
	return &openkcmapi.GetRootKeyResponse{
		ID:              id,
		Provider:        req.Provider,
		Linked:          true,
		ProcessingState: openkcmapi.ProcessingStateReady,
		LifecycleState:  string(b.l1Lifecycle),
	}, nil
}

func (b *kryptonLikeBackend) RemoveProvider(_ context.Context, id string) error {
	b.state.Lock()
	defer b.state.Unlock()
	b.removeCalls = append(b.removeCalls, id)
	return fmt.Errorf("RemoveProvider %s: %w", id, errors.ErrUnsupported)
}

func (b *kryptonLikeBackend) ActivateKey(_ context.Context, id string) (*openkcmapi.ActivateKeyResponse, error) {
	b.state.Lock()
	defer b.state.Unlock()
	if id == testL1KeyID {
		if b.l1ActivateErr != nil {
			err := b.l1ActivateErr
			b.l1ActivateErr = nil
			return nil, err
		}
		b.l1Activations = append(b.l1Activations, id)
		b.l1Lifecycle, b.l1Processing = shared.LifecycleActive, "pending"
		return &openkcmapi.ActivateKeyResponse{
			ID: id, LifecycleState: string(b.l1Lifecycle), ProcessingState: b.l1Processing,
		}, nil
	}
	b.l2Activations = append(b.l2Activations, id)
	b.l2Lifecycle, b.l2Processing = shared.LifecycleActive, "pending"
	return &openkcmapi.ActivateKeyResponse{
		ID: id, LifecycleState: string(b.l2Lifecycle), ProcessingState: b.l2Processing,
	}, nil
}

func newRootKeyWithCredentials() *operationsv1alpha1.AWSRootKey {
	GinkgoHelper()
	rk := newRootKey()
	secret := &corev1.Secret{}
	secret.Name = "awsrootkey-" + rk.Name
	secret.Namespace = rk.Namespace
	secret.Labels = map[string]string{"openkcm.io/root-key-credentials": "true"}
	secret.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: operationsv1alpha1.GroupVersion.String(),
		Kind:       "AWSRootKey",
		Name:       rk.Name,
		UID:        rk.UID,
	}}
	secret.Data = map[string][]byte{
		"accessKeyId":     []byte("AKIAEXAMPLE"),
		"secretAccessKey": []byte("secret-value"),
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	return rk
}

func condition(dk *operationsv1alpha1.DomainKey, conditionType string) *metav1.Condition {
	return meta.FindStatusCondition(dk.Status.Conditions, conditionType)
}

var _ = Describe("DomainKeyReconciler linking through a backend that reports no link", func() {
	var (
		backend    *kryptonLikeBackend
		reconciler *operations.DomainKeyReconciler
	)

	BeforeEach(func() {
		ensureLogicalCluster(testWorkspace)
		ensureDomainKeyNamespace()
		ensureRegisteredTenant()
		backend = newKryptonLikeBackend()
		reconciler = &operations.DomainKeyReconciler{
			APIClient:        backend,
			L1Keys:           backend,
			Manager:          newTestManager(),
			AccountNamespace: domainKeyNamespace,
		}
	})

	AfterEach(func() {
		cleanupDomainKeyObject(&operationsv1alpha1.DomainKey{}, "domain-key")
		cleanupDomainKeyObject(&operationsv1alpha1.Tenant{}, testAccountName)
	})

	It("binds the provider once, activates the L1 and waits for it before the L2", func() {
		root := newRootKeyWithCredentials()
		dk := newDomainKey(root.Name)

		drive(reconciler, dk, 8)

		Expect(backend.providerCalls).To(HaveLen(1))
		call := backend.providerCalls[0]
		Expect(call.rootKeyID).To(Equal(testL1KeyID))
		Expect(call.request.Provider).To(Equal(openkcmapi.ProviderAWS))
		Expect(call.request.Config).To(Equal(map[string]string{
			openkcmapi.AWSConfigRegion:  "eu-central-1",
			openkcmapi.AWSConfigKeyARN:  "arn:aws:kms:eu-central-1:000000000000:key/abc",
			openkcmapi.AWSConfigRoleARN: "arn:aws:iam::000000000000:role/r",
		}))
		Expect(backend.l1Activations).To(HaveLen(1))
		Expect(backend.l2Activations).To(BeEmpty())

		reloaded := reloadDomainKey(dk.Name)
		linked := condition(reloaded, "RootKeyLinked")
		Expect(linked).NotTo(BeNil())
		Expect(linked.Status).To(Equal(metav1.ConditionTrue))
		ready := condition(reloaded, operations.ReadyType)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal("AwaitingRootKeyActivation"))
	})

	It("activates the L2 once the L1 is active, and never binds the provider again", func() {
		root := newRootKeyWithCredentials()
		dk := newDomainKey(root.Name)
		drive(reconciler, dk, 6)

		backend.completeL1()
		drive(reconciler, dk, 4)

		Expect(backend.providerCalls).To(HaveLen(1))
		Expect(backend.l2Activations).To(ConsistOf(testKeyID))
		activating := reloadDomainKey(dk.Name)
		Expect(activating.Status.CryptoState.LifecycleState).To(Equal(shared.LifecyclePreActive),
			"Krypton reports the L2 active as soon as the job is accepted, "+
				"but the key is not ready until its processing completes")
		ready := condition(activating, operations.ReadyType)
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal("Activating"))

		backend.completeL2()
		drive(reconciler, dk, 3)

		Expect(backend.providerCalls).To(HaveLen(1))
		Expect(backend.l2Activations).To(ConsistOf(testKeyID))
		active := reloadDomainKey(dk.Name)
		Expect(active.Status.CryptoState.LifecycleState).To(Equal(shared.LifecycleActive))
		Expect(condition(active, operations.ReadyType).Status).To(Equal(metav1.ConditionTrue))
	})

	It("keeps the link it has made when the L1 activation fails right after it", func() {
		root := newRootKeyWithCredentials()
		dk := newDomainKey(root.Name)
		drive(reconciler, dk, 2)
		backend.l1ActivateErr = errors.New("krypton unavailable")

		_, err := reconciler.Reconcile(ctx, requestFor(dk))

		Expect(err).To(HaveOccurred())
		Expect(backend.providerCalls).To(HaveLen(1))
		linked := condition(reloadDomainKey(dk.Name), "RootKeyLinked")
		Expect(linked).NotTo(BeNil(), "the link must be saved before anything else can go wrong")
		Expect(linked.Status).To(Equal(metav1.ConditionTrue))

		drive(reconciler, dk, 3)

		Expect(backend.providerCalls).To(HaveLen(1))
		Expect(backend.l1Activations).To(HaveLen(1))
	})

	It("takes over an L2 that Krypton has already activated", func() {
		backend.completeL1()
		backend.completeL2()
		dk := newDomainKey("")

		drive(reconciler, dk, 4)

		Expect(backend.l2Activations).To(BeEmpty())
		reloaded := reloadDomainKey(dk.Name)
		Expect(reloaded.Status.CryptoState.LifecycleState).To(Equal(shared.LifecycleActive))
		Expect(condition(reloaded, operations.ReadyType).Status).To(Equal(metav1.ConditionTrue))
	})

	It("refuses to switch the root key when the backend cannot remove a provider", func() {
		first := newRootKeyWithCredentials()
		second := newRootKeyWithCredentials()
		dk := newDomainKey(first.Name)
		drive(reconciler, dk, 6)
		backend.completeL1()
		drive(reconciler, dk, 2)

		reloaded := reloadDomainKey(dk.Name)
		reloaded.Spec.PrimaryRootKeyRef.Name = second.Name
		Expect(k8sClient.Update(ctx, reloaded)).To(Succeed())
		drive(reconciler, dk, 3)

		Expect(backend.removeCalls).NotTo(BeEmpty())
		Expect(backend.providerCalls).To(HaveLen(1))
		changed := condition(reloadDomainKey(dk.Name), "RootKeyChangeSupported")
		Expect(changed).NotTo(BeNil())
		Expect(changed.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition(reloadDomainKey(dk.Name), "RootKeyLinked").Status).To(Equal(metav1.ConditionTrue))
	})
})
