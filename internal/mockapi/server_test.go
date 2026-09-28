package mockapi_test

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/openkcm/openkcm-controller/internal/mockapi"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

func startTestServer(t *testing.T) *openkcmapi.Client {
	t.Helper()
	t.Setenv("MOCK_OPENKCM_PROVISIONING_DELAY", "0s")
	t.Setenv("POD_NAMESPACE", "")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen")
	t.Cleanup(func() { _ = listener.Close() })

	server := mockapi.NewServer(listener.Addr().String())
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	return openkcmapi.NewClient("http://" + listener.Addr().String())
}

func TestCreateTenantAllowsDuplicateNames(t *testing.T) {
	client := startTestServer(t)

	first, err := client.CreateTenant(t.Context(), openkcmapi.CreateTenantRequest{Name: "acme"})
	require.NoError(t, err, "create first tenant")
	second, err := client.CreateTenant(t.Context(), openkcmapi.CreateTenantRequest{Name: "acme"})
	require.NoError(t, err, "create second tenant")
	require.NotEqual(t, first.ID, second.ID, "duplicate tenant creation returned the same ID")
}

func TestRootKeyLifecycleEndpoints(t *testing.T) {
	client := startTestServer(t)
	ctx := t.Context()

	tenant, err := client.CreateTenant(ctx, openkcmapi.CreateTenantRequest{Name: "ig-clean-account"})
	require.NoError(t, err, "create tenant")

	root, err := client.CreateRootKey(ctx, openkcmapi.CreateRootKeyRequest{
		TenantID: tenant.ID,
		Provider: "openbao",
		Name:     "ig-clean-account-root",
		Config:   map[string]string{"enginePath": "transit"},
	})
	require.NoError(t, err, "create root key")
	require.Equal(t, mockapi.LifecyclePreActive, root.LifecycleState, "new root key lifecycle")

	activated, err := client.ActivateKey(ctx, root.ID)
	require.NoError(t, err, "activate root key")
	require.Equal(t, mockapi.LifecycleActive, activated.LifecycleState, "activated lifecycle")

	suspended, err := client.SuspendKey(ctx, root.ID)
	require.NoError(t, err, "suspend root key")
	require.Equal(t, mockapi.LifecycleSuspended, suspended.LifecycleState, "suspended lifecycle")

	destroyed, err := client.DestroyKey(ctx, root.ID)
	require.NoError(t, err, "destroy root key")
	require.Equal(t, mockapi.LifecycleDestroyed, destroyed.LifecycleState, "destroyed lifecycle")

	_, err = client.ActivateKey(ctx, root.ID)
	require.Error(t, err, "activate destroyed key")
}

func TestDEKRequiresActiveServiceKey(t *testing.T) {
	client := startTestServer(t)
	ctx := t.Context()

	tenant, err := client.CreateTenant(ctx, openkcmapi.CreateTenantRequest{Name: "ig-clean-account"})
	require.NoError(t, err, "create tenant")
	domain, err := client.CreateKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: tenant.ID,
		Kind:     "L2",
		Name:     "domain",
	})
	require.NoError(t, err, "create L2")
	_, err = client.ActivateKey(ctx, domain.ID)
	require.NoError(t, err, "activate L2")
	service, err := client.CreateKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: tenant.ID,
		Kind:     "L3",
		Name:     "service",
		ParentID: domain.ID,
	})
	require.NoError(t, err, "create L3")

	_, err = client.CreateDEK(ctx, openkcmapi.CreateDEKRequest{
		TenantID:     tenant.ID,
		ServiceKeyID: service.ID,
		Name:         "workload",
	})
	require.Error(t, err, "create DEK under inactive L3")
	var apiErr *openkcmapi.APIError
	require.ErrorAs(t, err, &apiErr, "create DEK under inactive L3, want parent_not_active APIError")
	require.Equal(t, "parent_not_active", apiErr.Code, "create DEK under inactive L3 error code")

	_, err = client.ActivateKey(ctx, service.ID)
	require.NoError(t, err, "activate L3")
	dek, err := client.CreateDEK(ctx, openkcmapi.CreateDEKRequest{
		TenantID:       tenant.ID,
		ServiceKeyID:   service.ID,
		Name:           "workload",
		KMIPAttributes: map[string]string{"purpose": "test"},
	})
	require.NoError(t, err, "create DEK")
	require.Equal(t, mockapi.LifecyclePreActive, dek.LifecycleState, "new DEK lifecycle")
}
