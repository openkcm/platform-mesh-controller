package kryptongrpc

import (
	"context"
	"testing"

	kryptonkeys "github.com/openkcm/krypton/pkg/api/v1/proto/admin/keys"
	"google.golang.org/grpc"

	"github.com/openkcm/openkcm-controller/api/shared"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
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

func newKeyClientAgainst(t *testing.T, stub *stubKeyService) *KeyClient {
	return NewKeyClient(bufconnDial(t, func(s *grpc.Server) {
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
func keyClientReturning(t *testing.T, key *kryptonkeys.Key) *KeyClient {
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
		{"completed maps to ready", kryptonProcessingCompleted, openkcmapi.ProcessingStateReady},
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
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.ID != testKeyID {
				t.Errorf("ID = %q, want %q", resp.ID, testKeyID)
			}
			if resp.ProcessingState != tc.want {
				t.Errorf("ProcessingState = %q, want %q", resp.ProcessingState, tc.want)
			}
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
		{"pre-activation", kryptonKeyLifecyclePreActivation, shared.LifecyclePreActive, false},
		{"active", kryptonKeyLifecycleActive, shared.LifecycleActive, false},
		{"deactivated", kryptonKeyLifecycleDeactivated, shared.LifecycleDeactivated, false},
		{"suspended", kryptonKeyLifecycleSuspended, shared.LifecycleSuspended, false},
		{"compromised", kryptonKeyLifecycleCompromised, shared.LifecycleCompromised, false},
		{"destroyed", kryptonKeyLifecycleDestroyed, shared.LifecycleDestroyed, false},
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
				if err == nil {
					t.Fatalf("want an error for unknown lifecycle state %q", tc.krypton)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.LifecycleState != string(tc.want) {
				t.Errorf("LifecycleState = %q, want %q", resp.LifecycleState, string(tc.want))
			}
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
				Key: &kryptonkeys.Key{Id: testKeyID, LifeCycleState: kryptonKeyLifecycleActive},
			}, nil
		},
	})

	// when
	resp, err := c.ActivateKey(t.Context(), testKeyID, testKeyTenantID)

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.LifecycleState != string(shared.LifecycleActive) {
		t.Errorf("LifecycleState = %q, want %q", resp.LifecycleState, string(shared.LifecycleActive))
	}
}
