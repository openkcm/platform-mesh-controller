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

package kryptongrpc

import (
	"fmt"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/openkcm/openkcm-controller/api/shared"
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
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenants := NewTenantClient(conn)
	keys := NewKeyClient(conn)
	ctx := t.Context()
	tag := fmt.Sprintf("live-%d", time.Now().UnixNano())

	// given a tenant
	tenant, err := tenants.CreateTenant(ctx, openkcmapi.CreateTenantRequest{Name: tag})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	// when the root (K0) is announced and activated
	root, err := keys.AnnounceKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: tenant.ID, Kind: "K0", Name: "root-" + tag,
	})
	if err != nil {
		t.Fatalf("AnnounceKey K0: %v", err)
	}
	if root.ProcessingState != openkcmapi.ProcessingStateReady {
		t.Fatalf("K0 processing = %q, want ready", root.ProcessingState)
	}
	if _, err := keys.ActivateKey(ctx, root.ID, tenant.ID); err != nil {
		t.Fatalf("ActivateKey K0: %v", err)
	}

	// and the domain key (K1) is announced under it and activated
	kek, err := keys.AnnounceKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: tenant.ID, Kind: "K1", Name: "kek-" + tag, ParentID: root.ID,
	})
	if err != nil {
		t.Fatalf("AnnounceKey K1: %v", err)
	}
	act, err := keys.ActivateKey(ctx, kek.ID, tenant.ID)
	if err != nil {
		t.Fatalf("ActivateKey K1: %v", err)
	}

	// then the read-back state is translated into the controller's vocabulary
	if act.LifecycleState != string(shared.LifecycleActive) {
		t.Errorf("activated K1 lifecycle = %q, want %q", act.LifecycleState, shared.LifecycleActive)
	}
	got, err := keys.GetKey(ctx, kek.ID, tenant.ID)
	if err != nil {
		t.Fatalf("GetKey K1: %v", err)
	}
	if got.LifecycleState != string(shared.LifecycleActive) {
		t.Errorf("K1 lifecycle = %q, want %q", got.LifecycleState, shared.LifecycleActive)
	}
	if got.ProcessingState != openkcmapi.ProcessingStateReady {
		t.Errorf("K1 processing = %q, want ready", got.ProcessingState)
	}

	// and a non-root key without a parent is rejected, not silently accepted
	if _, err := keys.AnnounceKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: tenant.ID, Kind: "K1", Name: "orphan-" + tag,
	}); err == nil {
		t.Error("AnnounceKey K1 without a parent must fail")
	} else if !openkcmapi.IsRetryable(err) && !openkcmapi.IsNotFound(err) {
		t.Logf("orphan K1 rejected as expected: %v", err)
	}
}
