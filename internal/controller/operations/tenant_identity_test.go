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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
)

func TestResolveTenantID(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme))
	account := accountIdentity{Name: "acme", Namespace: "accounts"}

	t.Run("returns the recorded backend identity", func(t *testing.T) {
		t.Parallel()

		tenant := &operationsv1alpha1.Tenant{}
		tenant.Name = account.Name
		tenant.Namespace = account.Namespace
		tenant.Annotations = map[string]string{tenantIDAnnotation: "tenant-id"}
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tenant).Build()

		id, err := resolveTenantID(t.Context(), client, account)

		require.NoError(t, err)
		assert.Equal(t, "tenant-id", id)
	})

	t.Run("waits when the Tenant has no backend identity", func(t *testing.T) {
		t.Parallel()

		client := fake.NewClientBuilder().WithScheme(scheme).Build()

		id, err := resolveTenantID(t.Context(), client, account)

		require.NoError(t, err)
		assert.Empty(t, id)
	})
}
