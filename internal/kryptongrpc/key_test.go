package kryptongrpc_test

import (
	"context"
	"testing"

	kryptonkeys "github.com/openkcm/krypton/pkg/api/v1/proto/admin/keys"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/openkcm/platform-mesh-controller/api/shared"
	"github.com/openkcm/platform-mesh-controller/internal/kryptongrpc"
	"github.com/openkcm/platform-mesh-controller/internal/openkcmapi"
)

const (
	testKeyID       = "k1"
	testKeyTenantID = "tenant-1"
)

type stubKeyService struct {
	kryptonkeys.UnimplementedKeyServiceServer

	announceFn func(req *kryptonkeys.AnnounceKeyRequest) (*kryptonkeys.AnnounceKeyResponse, error)
	getFn      func(req *kryptonkeys.GetKeyRequest) (*kryptonkeys.GetKeyResponse, error)
	activateFn func(req *kryptonkeys.ActivateKeyRequest) (*kryptonkeys.ActivateKeyResponse, error)
}

func newKeyClientAgainst(t *testing.T, stub *stubKeyService) *kryptongrpc.KeyClient {
	return kryptongrpc.NewKeyClient(bufconnDial(t, func(s *grpc.Server) {
		kryptonkeys.RegisterKeyServiceServer(s, stub)
	}))
}

func (s *stubKeyService) AnnounceKey(_ context.Context, req *kryptonkeys.AnnounceKeyRequest) (*kryptonkeys.AnnounceKeyResponse, error) {
	return s.announceFn(req)
}
func (s *stubKeyService) GetKey(_ context.Context, req *kryptonkeys.GetKeyRequest) (*kryptonkeys.GetKeyResponse, error) {
	return s.getFn(req)
}
func (s *stubKeyService) ActivateKey(_ context.Context, req *kryptonkeys.ActivateKeyRequest) (*kryptonkeys.ActivateKeyResponse, error) {
	return s.activateFn(req)
}

// keyClientReturning wires a stub whose GetKey hands back the given key,
// which is all most of these tests need.
func keyClientReturning(t *testing.T, key *kryptonkeys.Key) *kryptongrpc.KeyClient {
	return newKeyClientAgainst(t, &stubKeyService{
		getFn: func(*kryptonkeys.GetKeyRequest) (*kryptonkeys.GetKeyResponse, error) {
			return &kryptonkeys.GetKeyResponse{Key: key}, nil
		},
	})
}

func TestAnnounceKeyTranslatesProcessingState(t *testing.T) {
	tests := []struct {
		name    string
		krypton string
		want    string
	}{
		{"completed maps to ready", kryptongrpc.KryptonProcessingCompleted, openkcmapi.ProcessingStateReady},
		{"anything else passes through", "pending", "pending"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// given
			c := newKeyClientAgainst(t, &stubKeyService{
				announceFn: func(*kryptonkeys.AnnounceKeyRequest) (*kryptonkeys.AnnounceKeyResponse, error) {
					return &kryptonkeys.AnnounceKeyResponse{
						Key: &kryptonkeys.Key{
							Id:                 testKeyID,
							KeyProcessingState: &kryptonkeys.KeyProcessingState{Status: tc.krypton},
						},
					}, nil
				},
			})

			// when
			resp, err := c.AnnounceKey(t.Context(), openkcmapi.CreateKeyRequest{Name: "domain-key", Kind: "K1"})

			// then
			require.NoError(t, err)
			assert.Equal(t, testKeyID, resp.ID, "ID")
			assert.Equal(t, tc.want, resp.ProcessingState, "ProcessingState")
		})
	}
}

func TestGetKeyTranslatesLifecycleState(t *testing.T) {
	tests := []struct {
		name    string
		krypton string
		want    shared.LifecycleState
		wantErr bool
	}{
		{"pre-activation", kryptongrpc.KryptonKeyLifecyclePreActivation, shared.LifecyclePreActive, false},
		{"active", kryptongrpc.KryptonKeyLifecycleActive, shared.LifecycleActive, false},
		{"deactivated", kryptongrpc.KryptonKeyLifecycleDeactivated, shared.LifecycleDeactivated, false},
		{"suspended", kryptongrpc.KryptonKeyLifecycleSuspended, shared.LifecycleSuspended, false},
		{"compromised", kryptongrpc.KryptonKeyLifecycleCompromised, shared.LifecycleCompromised, false},
		{"destroyed", kryptongrpc.KryptonKeyLifecycleDestroyed, shared.LifecycleDestroyed, false},
		{"unknown is rejected", "banana", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// given
			c := keyClientReturning(t, &kryptonkeys.Key{Id: testKeyID, LifeCycleState: tc.krypton})

			// when
			resp, err := c.GetKey(t.Context(), testKeyID, testKeyTenantID)

			// then
			if tc.wantErr {
				require.Errorf(t, err, "want an error for unknown lifecycle state %q", tc.krypton)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, string(tc.want), resp.LifecycleState, "LifecycleState")
		})
	}
}

func TestActivateKeyReadsBackStateAfterEmptyResponse(t *testing.T) {
	// given
	c := newKeyClientAgainst(t, &stubKeyService{
		activateFn: func(*kryptonkeys.ActivateKeyRequest) (*kryptonkeys.ActivateKeyResponse, error) {
			return &kryptonkeys.ActivateKeyResponse{}, nil
		},
		getFn: func(*kryptonkeys.GetKeyRequest) (*kryptonkeys.GetKeyResponse, error) {
			return &kryptonkeys.GetKeyResponse{
				Key: &kryptonkeys.Key{Id: testKeyID, LifeCycleState: kryptongrpc.KryptonKeyLifecycleActive},
			}, nil
		},
	})

	// when
	resp, err := c.ActivateKey(t.Context(), testKeyID, testKeyTenantID)

	// then
	require.NoError(t, err)
	assert.Equal(t, string(shared.LifecycleActive), resp.LifecycleState, "LifecycleState")
}
