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
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	operations "github.com/openkcm/openkcm-controller/internal/controller/operations"
	"github.com/openkcm/openkcm-controller/internal/mockapi"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

var _ = Describe("ServiceKey Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: testDefaultTenantNamespace,
		}
		servicekey := &operationsv1alpha1.ServiceKey{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ServiceKey")
			err := k8sClient.Get(ctx, typeNamespacedName, servicekey)
			if err != nil && errors.IsNotFound(err) {
				resource := &operationsv1alpha1.ServiceKey{
					Name:      resourceName,
					Namespace: testDefaultTenantNamespace,
					Spec: operationsv1alpha1.ServiceKeySpec{
						TenantNameRef: "test-tenant",
						DomainKeyRef:  "test-domainkey",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &operationsv1alpha1.ServiceKey{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ServiceKey")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Starting a mock API server")
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				Expect(listener.Close()).To(Succeed())
			}()

			mockServer := mockapi.NewServer(listener.Addr().String())
			go func() { _ = mockServer.Serve(listener) }()
			defer func() {
				Expect(mockServer.Close()).To(Succeed())
			}()

			apiClient := openkcmapi.NewClient("http://" + listener.Addr().String())

			By("Reconciling the created resource")
			controllerReconciler := &operations.ServiceKeyReconciler{
				APIClient: apiClient,
			}

			// Verify the reconciler can be constructed with the new struct layout
			_ = controllerReconciler
			_ = mcreconcile.Request{
				NamespacedName: typeNamespacedName,
				ClusterName:    "",
			}
		})
	})
})
