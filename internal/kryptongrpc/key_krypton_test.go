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

package kryptongrpc_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/openkcm/openkcm-controller/api/shared"
	"github.com/openkcm/openkcm-controller/internal/kryptongrpc"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

// TestKeyClientAgainstLiveKrypton exercises the client over the real gRPC API.
// It is opt-in: set KRYPTON_TEST_ADDR to a running Krypton admin endpoint.
//
//	KRYPTON_TEST_ADDR=localhost:8080 go test ./internal/kryptongrpc/ -run Live
func TestKeyClientAgainstLiveKrypton(t *testing.T) {
	addr := os.Getenv("KRYPTON_TEST_ADDR")
	if addr == "" {
		t.Skip("set KRYPTON_TEST_ADDR to run against a live Krypton")
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "dial")
	t.Cleanup(func() { _ = conn.Close() })

	tenants := kryptongrpc.NewTenantClient(conn)
	keys := kryptongrpc.NewKeyClient(conn)
	ctx := t.Context()
	tag := fmt.Sprintf("live-%d", time.Now().UnixNano())

	// given a tenant
	tenant, err := tenants.CreateTenant(ctx, openkcmapi.CreateTenantRequest{Name: tag})
	require.NoError(t, err, "CreateTenant")

	// when the root (K0) is announced and activated
	root, err := keys.AnnounceKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: tenant.ID, Kind: "K0", Name: "root-" + tag,
	})
	require.NoError(t, err, "AnnounceKey K0")
	require.Equal(t, openkcmapi.ProcessingStateReady, root.ProcessingState, "K0 processing")
	_, err = keys.ActivateKey(ctx, root.ID, tenant.ID)
	require.NoError(t, err, "ActivateKey K0")

	// and the domain key (K1) is announced under it and activated
	kek, err := keys.AnnounceKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: tenant.ID, Kind: "K1", Name: "kek-" + tag, ParentID: root.ID,
	})
	require.NoError(t, err, "AnnounceKey K1")
	act, err := keys.ActivateKey(ctx, kek.ID, tenant.ID)
	require.NoError(t, err, "ActivateKey K1")

	// then the read-back state is translated into the controller's vocabulary
	assert.Equal(t, string(shared.LifecycleActive), act.LifecycleState, "activated K1 lifecycle")
	got, err := keys.GetKey(ctx, kek.ID, tenant.ID)
	require.NoError(t, err, "GetKey K1")
	assert.Equal(t, string(shared.LifecycleActive), got.LifecycleState, "K1 lifecycle")
	assert.Equal(t, openkcmapi.ProcessingStateReady, got.ProcessingState, "K1 processing")

	// and a non-root key without a parent is rejected, not silently accepted
	_, err = keys.AnnounceKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: tenant.ID, Kind: "K1", Name: "orphan-" + tag,
	})
	require.Error(t, err, "AnnounceKey K1 without a parent must fail")
	if !openkcmapi.IsRetryable(err) && !openkcmapi.IsNotFound(err) {
		t.Logf("orphan K1 rejected as expected: %v", err)
	}
}

// TestBackendAgainstLiveKrypton drives the adapter end to end: one CreateKey
// call must seed the tenant root and announce the domain key under it, then be
// readable and activatable by the opaque id alone.
func TestBackendAgainstLiveKrypton(t *testing.T) {
	addr := os.Getenv("KRYPTON_TEST_ADDR")
	if addr == "" {
		t.Skip("set KRYPTON_TEST_ADDR to run against a live Krypton")
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "dial")
	t.Cleanup(func() { _ = conn.Close() })

	b := kryptongrpc.NewBackend(conn, kryptongrpc.BackendOptions{})
	tenants := kryptongrpc.NewTenantClient(conn)
	ctx := t.Context()
	tag := fmt.Sprintf("be-%d", time.Now().UnixNano())

	// given a tenant
	tenant, err := tenants.CreateTenant(ctx, openkcmapi.CreateTenantRequest{Name: tag})
	require.NoError(t, err, "CreateTenant")

	// when a domain key is created with no explicit root or parent
	created, err := b.CreateKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: tenant.ID, Kind: "L2", Name: "domain-" + tag,
	})
	require.NoError(t, err, "CreateKey")

	// then the id carries the tenant and the key reads back and activates
	_, _, err = kryptongrpc.UnpackKeyID(created.ID)
	require.NoError(t, err, "returned id is not a packed handle")
	_, err = b.GetKey(ctx, created.ID)
	require.NoError(t, err, "GetKey")
	act, err := b.ActivateKey(ctx, created.ID)
	require.NoError(t, err, "ActivateKey")
	assert.Equal(t, string(shared.LifecycleActive), act.LifecycleState, "activated domain key lifecycle")
}
