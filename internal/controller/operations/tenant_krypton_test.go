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
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kryptonadmin "github.com/openkcm/krypton/pkg/api/v1/proto/admin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openkcm/openkcm-controller/internal/kryptongrpc"
)

// The fake-backend specs cover the reconcile logic, and the kryptongrpc specs
// cover the wire protocol. Neither exercises the seam between them, so this one
// drives the real reconciler against a real Krypton over a real API server.
//
// It is opt-in: set KRYPTON_TEST_ADDR to a running Krypton admin endpoint.
//
//	make postgres && make root      # in the krypton repo
//	KRYPTON_TEST_ADDR=localhost:8080 make test
//
// Without the variable the spec is skipped, so CI stays hermetic.
var _ = Describe("TenantReconciler against a live Krypton", func() {
	var reconciler *TenantReconciler

	BeforeEach(func() {
		addr := os.Getenv("KRYPTON_TEST_ADDR")
		if addr == "" {
			Skip("set KRYPTON_TEST_ADDR to run against a live Krypton")
		}

		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close() })

		ensureLogicalCluster(testWorkspace)
		reconciler = &TenantReconciler{
			APIClient: kryptongrpc.NewTenantClient(conn),
			Manager:   newTestManager(),
		}
	})

	It("provisions the tenant in Krypton and records the id it hands back", func() {
		tenant := newTenant()

		// Pass 1 persists the finalizer, pass 2 registers with Krypton,
		// pass 3 confirms readiness. The reconciler does one step per call.
		for range 3 {
			_, err := reconciler.Reconcile(ctx, requestFor(tenant))
			Expect(err).NotTo(HaveOccurred())
		}

		reloaded := reloadTenant(tenant.Name)

		cond := meta.FindStatusCondition(reloaded.Status.Conditions, readyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue), "the CR must go Ready once Krypton has the tenant")

		tenantID := reloaded.Status.OperationID
		Expect(tenantID).NotTo(BeEmpty())
		Expect(reloaded.Annotations).To(HaveKeyWithValue(tenantIDAnnotation, tenantID))

		// The id must be Krypton's, not something the controller invented, so
		// ask Krypton directly rather than trusting the reconciler's status.
		addr := os.Getenv("KRYPTON_TEST_ADDR")
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = conn.Close() }()

		resp, err := kryptonadmin.NewTenantServiceClient(conn).
			GetTenant(ctx, &kryptonadmin.GetTenantRequest{Id: tenantID})
		Expect(err).NotTo(HaveOccurred(), "Krypton must know the tenant the CR claims")
		Expect(resp.GetTenant().GetId()).To(Equal(tenantID))
		Expect(resp.GetTenant().GetName()).To(Equal(testAccountName),
			"Krypton must have stored the path-derived account name")
	})
})
