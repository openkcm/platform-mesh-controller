package mockapi

import (
	"errors"
	"net"
	"testing"

	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

func startTestServer(t *testing.T) *openkcmapi.Client {
	t.Helper()
	t.Setenv("MOCK_OPENKCM_PROVISIONING_DELAY", "0s")
	t.Setenv("POD_NAMESPACE", "")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	server := NewServer(listener.Addr().String())
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	return openkcmapi.NewClient("http://" + listener.Addr().String())
}

func TestCreateTenantAllowsDuplicateNames(t *testing.T) {
	client := startTestServer(t)

	first, err := client.CreateTenant(t.Context(), openkcmapi.CreateTenantRequest{Name: "acme"})
	if err != nil {
		t.Fatalf("create first tenant: %v", err)
	}
	second, err := client.CreateTenant(t.Context(), openkcmapi.CreateTenantRequest{Name: "acme"})
	if err != nil {
		t.Fatalf("create second tenant: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("duplicate tenant creation returned the same ID")
	}
}

func TestRootKeyLifecycleEndpoints(t *testing.T) {
	client := startTestServer(t)
	ctx := t.Context()

	tenant, err := client.CreateTenant(ctx, openkcmapi.CreateTenantRequest{Name: "ig-clean-account"})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	root, err := client.CreateRootKey(ctx, openkcmapi.CreateRootKeyRequest{
		TenantID: tenant.ID,
		Provider: "openbao",
		Name:     "ig-clean-account-root",
		Config:   map[string]string{"enginePath": "transit"},
	})
	if err != nil {
		t.Fatalf("create root key: %v", err)
	}
	if root.LifecycleState != lifecyclePreActive {
		t.Fatalf("new root key lifecycle = %q, want %q", root.LifecycleState, lifecyclePreActive)
	}

	activated, err := client.ActivateKey(ctx, root.ID)
	if err != nil {
		t.Fatalf("activate root key: %v", err)
	}
	if activated.LifecycleState != lifecycleActive {
		t.Fatalf("activated lifecycle = %q, want %q", activated.LifecycleState, lifecycleActive)
	}

	suspended, err := client.SuspendKey(ctx, root.ID)
	if err != nil {
		t.Fatalf("suspend root key: %v", err)
	}
	if suspended.LifecycleState != lifecycleSuspended {
		t.Fatalf("suspended lifecycle = %q, want %q", suspended.LifecycleState, lifecycleSuspended)
	}

	destroyed, err := client.DestroyKey(ctx, root.ID)
	if err != nil {
		t.Fatalf("destroy root key: %v", err)
	}
	if destroyed.LifecycleState != lifecycleDestroyed {
		t.Fatalf("destroyed lifecycle = %q, want %q", destroyed.LifecycleState, lifecycleDestroyed)
	}

	if _, err := client.ActivateKey(ctx, root.ID); err == nil {
		t.Fatal("activate destroyed key: got nil error")
	}
}

func TestDEKRequiresActiveServiceKey(t *testing.T) {
	client := startTestServer(t)
	ctx := t.Context()

	tenant, err := client.CreateTenant(ctx, openkcmapi.CreateTenantRequest{Name: "ig-clean-account"})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	domain, err := client.CreateKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: tenant.ID,
		Kind:     "L2",
		Name:     "domain",
	})
	if err != nil {
		t.Fatalf("create L2: %v", err)
	}
	if _, err := client.ActivateKey(ctx, domain.ID); err != nil {
		t.Fatalf("activate L2: %v", err)
	}
	service, err := client.CreateKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: tenant.ID,
		Kind:     "L3",
		Name:     "service",
		ParentID: domain.ID,
	})
	if err != nil {
		t.Fatalf("create L3: %v", err)
	}

	if _, err := client.CreateDEK(ctx, openkcmapi.CreateDEKRequest{
		TenantID:     tenant.ID,
		ServiceKeyID: service.ID,
		Name:         "workload",
	}); err == nil {
		t.Fatal("create DEK under inactive L3: got nil error")
	} else {
		var apiErr *openkcmapi.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != "parent_not_active" {
			t.Fatalf("create DEK under inactive L3 error = %v, want parent_not_active APIError", err)
		}
	}

	if _, err := client.ActivateKey(ctx, service.ID); err != nil {
		t.Fatalf("activate L3: %v", err)
	}
	dek, err := client.CreateDEK(ctx, openkcmapi.CreateDEKRequest{
		TenantID:       tenant.ID,
		ServiceKeyID:   service.ID,
		Name:           "workload",
		KMIPAttributes: map[string]string{"purpose": "test"},
	})
	if err != nil {
		t.Fatalf("create DEK: %v", err)
	}
	if dek.LifecycleState != lifecyclePreActive {
		t.Fatalf("new DEK lifecycle = %q, want %q", dek.LifecycleState, lifecyclePreActive)
	}
}
