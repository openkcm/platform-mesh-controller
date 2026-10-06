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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	operationsv1alpha1 "github.com/openkcm/platform-mesh-controller/api/operations/v1alpha1"
	operations "github.com/openkcm/platform-mesh-controller/internal/controller/operations"
	"github.com/openkcm/platform-mesh-controller/internal/openkcmapi"
)

const (
	rootKeyReconciliationNamespace = "rootkey-reconciliation-specs"
	provisionedDataEncryptionKeyID = "data-encryption-key-id"
)

// provisioningBackend records key-provisioning calls while inheriting the
// tenant call recording used by the existing reconciler test backend.
type provisioningBackend struct {
	testBackend
	rootKeyRequests []openkcmapi.CreateRootKeyRequest
	dekRequests     []openkcmapi.CreateDEKRequest
}

func (b *provisioningBackend) CreateRootKey(
	_ context.Context,
	req openkcmapi.CreateRootKeyRequest,
) (*openkcmapi.CreateRootKeyResponse, error) {
	b.mu.Lock()
	b.rootKeyRequests = append(b.rootKeyRequests, req)
	b.mu.Unlock()
	return &openkcmapi.CreateRootKeyResponse{ID: "root-key-id", ProcessingState: openkcmapi.ProcessingStateReady}, nil
}

func (b *provisioningBackend) CreateDEK(
	_ context.Context,
	req openkcmapi.CreateDEKRequest,
) (*openkcmapi.CreateDEKResponse, error) {
	b.mu.Lock()
	b.dekRequests = append(b.dekRequests, req)
	b.mu.Unlock()
	return &openkcmapi.CreateDEKResponse{
		ID:              provisionedDataEncryptionKeyID,
		ProcessingState: openkcmapi.ProcessingStateReady,
	}, nil
}

type rootKeyTestReconciler interface {
	Reconcile(context.Context, mcreconcile.Request) (ctrl.Result, error)
}

type rootKeyReconciliationCase struct {
	newRootKey       func() client.Object
	newReconciler    func(*provisioningBackend) rootKeyTestReconciler
	provider         string
	statusConditions func(client.Object) []metav1.Condition
}

func ensureRootKeyReconciliationNamespace() {
	GinkgoHelper()

	namespace := &corev1.Namespace{}
	namespace.Name = rootKeyReconciliationNamespace
	err := k8sClient.Create(ctx, namespace)
	if err != nil && !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

func ensureRootKeyReconciliationTenant(recorded bool) {
	GinkgoHelper()

	tenant := &operationsv1alpha1.Tenant{}
	tenant.Name = testAccountName
	tenant.Namespace = rootKeyReconciliationNamespace
	if recorded {
		tenant.Annotations = map[string]string{operations.TenantIDAnnotation: testTenantID}
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
}

func cleanupRootKeyReconciliationObject(obj client.Object) {
	GinkgoHelper()

	stored := obj.DeepCopyObject().(client.Object)
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), stored); err != nil {
		return
	}
	stored.SetFinalizers(nil)
	Expect(k8sClient.Update(ctx, stored)).To(Succeed())
	Expect(k8sClient.Delete(ctx, stored)).To(Succeed())
}

func newAWSRootKeyForReconciliation() client.Object {
	GinkgoHelper()

	rootKey := &operationsv1alpha1.AWSRootKey{}
	rootKey.Name = "aws-root-key"
	rootKey.Namespace = rootKeyReconciliationNamespace
	rootKey.Spec = operationsv1alpha1.AWSRootKeySpec{
		TenantNameRef: testAccountName,
		Region:        "eu-central-1",
		KeyURI:        "arn:aws:kms:eu-central-1:000000000000:key/root",
		RolesAnywhere: operationsv1alpha1.AWSRolesAnywhereConfig{
			TrustAnchorARN: "arn:aws:rolesanywhere:eu-central-1:000000000000:trust-anchor/root",
			ProfileARN:     "arn:aws:rolesanywhere:eu-central-1:000000000000:profile/root",
			RoleARN:        "arn:aws:iam::000000000000:role/root",
		},
	}
	Expect(k8sClient.Create(ctx, rootKey)).To(Succeed())
	return rootKey
}

func newAzureRootKeyForReconciliation() client.Object {
	GinkgoHelper()

	rootKey := &operationsv1alpha1.AzureRootKey{}
	rootKey.Name = "azure-root-key"
	rootKey.Namespace = rootKeyReconciliationNamespace
	rootKey.Spec = operationsv1alpha1.AzureRootKeySpec{
		TenantNameRef: testAccountName,
		VaultURL:      "https://openkcm.vault.azure.net",
		KeyName:       "root",
		FederatedIdentity: operationsv1alpha1.AzureFederatedIdentityConfig{
			TenantID: "azure-tenant-id",
			ClientID: "azure-client-id",
		},
	}
	Expect(k8sClient.Create(ctx, rootKey)).To(Succeed())
	return rootKey
}

func newOpenBaoRootKeyForReconciliation() client.Object {
	GinkgoHelper()

	rootKey := &operationsv1alpha1.OpenBaoRootKey{}
	rootKey.Name = "openbao-root-key"
	rootKey.Namespace = rootKeyReconciliationNamespace
	rootKey.Spec = operationsv1alpha1.OpenBaoRootKeySpec{
		TenantNameRef: testAccountName,
		EnginePath:    "transit",
		KeyName:       "root",
		ServerAddress: "https://openbao.openkcm.example",
		CertAuth: operationsv1alpha1.OpenBaoCertAuthConfig{
			AuthMountPath: "cert",
			RoleName:      "openkcm",
		},
	}
	Expect(k8sClient.Create(ctx, rootKey)).To(Succeed())
	return rootKey
}

func rootKeyReconciliationCases() []any {
	return []any{
		Entry("AWS", rootKeyReconciliationCase{
			newRootKey: newAWSRootKeyForReconciliation,
			newReconciler: func(backend *provisioningBackend) rootKeyTestReconciler {
				return &operations.AWSRootKeyReconciler{
					APIClient:        backend,
					Manager:          newTestManager(),
					AccountNamespace: rootKeyReconciliationNamespace,
				}
			},
			provider: "aws",
			statusConditions: func(obj client.Object) []metav1.Condition {
				return obj.(*operationsv1alpha1.AWSRootKey).Status.Conditions
			},
		}),
		Entry("Azure", rootKeyReconciliationCase{
			newRootKey: newAzureRootKeyForReconciliation,
			newReconciler: func(backend *provisioningBackend) rootKeyTestReconciler {
				return &operations.AzureRootKeyReconciler{
					APIClient:        backend,
					Manager:          newTestManager(),
					AccountNamespace: rootKeyReconciliationNamespace,
				}
			},
			provider: "azure",
			statusConditions: func(obj client.Object) []metav1.Condition {
				return obj.(*operationsv1alpha1.AzureRootKey).Status.Conditions
			},
		}),
		Entry("OpenBao", rootKeyReconciliationCase{
			newRootKey: newOpenBaoRootKeyForReconciliation,
			newReconciler: func(backend *provisioningBackend) rootKeyTestReconciler {
				return &operations.OpenBaoRootKeyReconciler{
					APIClient:        backend,
					Manager:          newTestManager(),
					AccountNamespace: rootKeyReconciliationNamespace,
				}
			},
			provider: "openbao",
			statusConditions: func(obj client.Object) []metav1.Condition {
				return obj.(*operationsv1alpha1.OpenBaoRootKey).Status.Conditions
			},
		}),
	}
}

var _ = Describe("RootKeyReconciler", func() {
	BeforeEach(func() {
		ensureLogicalCluster(testWorkspace)
		ensureRootKeyReconciliationNamespace()
	})

	DescribeTable("provisions root keys for the Tenant identity already recorded by Tenant reconciliation",
		append([]any{
			func(testCase rootKeyReconciliationCase) {
				backend := &provisioningBackend{}
				reconciler := testCase.newReconciler(backend)
				ensureRootKeyReconciliationTenant(true)
				rootKey := testCase.newRootKey()
				DeferCleanup(func() {
					cleanupRootKeyReconciliationObject(rootKey)
					tenant := &operationsv1alpha1.Tenant{}
					tenant.Name = testAccountName
					tenant.Namespace = rootKeyReconciliationNamespace
					cleanupRootKeyReconciliationObject(tenant)
				})

				_, err := reconciler.Reconcile(ctx, requestFor(rootKey))
				Expect(err).NotTo(HaveOccurred())
				_, err = reconciler.Reconcile(ctx, requestFor(rootKey))
				Expect(err).NotTo(HaveOccurred())

				Expect(backend.rootKeyRequests).To(HaveLen(1))
				Expect(backend.rootKeyRequests[0].TenantID).To(Equal(testTenantID))
				Expect(backend.rootKeyRequests[0].Provider).To(Equal(testCase.provider))
				createTenantCalls, _, _ := backend.counts()
				Expect(createTenantCalls).To(BeZero())
			},
		}, rootKeyReconciliationCases()...)...,
	)

	DescribeTable("requeues root keys until Tenant reconciliation records a backend identity",
		append([]any{
			func(testCase rootKeyReconciliationCase) {
				backend := &provisioningBackend{}
				reconciler := testCase.newReconciler(backend)
				ensureRootKeyReconciliationTenant(false)
				rootKey := testCase.newRootKey()
				DeferCleanup(func() {
					cleanupRootKeyReconciliationObject(rootKey)
					cleanupRootKeyReconciliationObject(&operationsv1alpha1.Tenant{
						Name:      testAccountName,
						Namespace: rootKeyReconciliationNamespace,
					})
				})

				_, err := reconciler.Reconcile(ctx, requestFor(rootKey))
				Expect(err).NotTo(HaveOccurred())
				result, err := reconciler.Reconcile(ctx, requestFor(rootKey))
				Expect(err).NotTo(HaveOccurred())

				Expect(result.RequeueAfter).To(Equal(operations.PollInterval))
				Expect(backend.rootKeyRequests).To(BeEmpty())
				createTenantCalls, _, _ := backend.counts()
				Expect(createTenantCalls).To(BeZero())
				reloaded := rootKey.DeepCopyObject().(client.Object)
				Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: rootKey.GetName(), Namespace: rootKey.GetNamespace(),
				}, reloaded)).To(Succeed())
				ready := meta.FindStatusCondition(testCase.statusConditions(reloaded), operations.ReadyType)
				Expect(ready).NotTo(BeNil())
				Expect(ready.Reason).To(Equal("AwaitingTenant"))
			},
		}, rootKeyReconciliationCases()...)...,
	)
})
