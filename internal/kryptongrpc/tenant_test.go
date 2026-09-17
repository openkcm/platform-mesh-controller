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
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	kryptonadmin "github.com/openkcm/krypton/pkg/api/v1/proto/admin"

	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

const (
	testTenantID   = "139d6656"
	testTenantName = "acme-prod"
)

// stubTenantService stands in for Krypton's TenantService over a real gRPC
// connection, so the wire encoding and the status codes are exercised rather
// than mocked away.
type stubTenantService struct {
	kryptonadmin.UnimplementedTenantServiceServer

	createFn func(*kryptonadmin.CreateTenantRequest) (*kryptonadmin.CreateTenantResponse, error)
	getFn    func(*kryptonadmin.GetTenantRequest) (*kryptonadmin.GetTenantResponse, error)
}

func (s *stubTenantService) CreateTenant(
	_ context.Context, req *kryptonadmin.CreateTenantRequest,
) (*kryptonadmin.CreateTenantResponse, error) {
	return s.createFn(req)
}

func (s *stubTenantService) GetTenant(
	_ context.Context, req *kryptonadmin.GetTenantRequest,
) (*kryptonadmin.GetTenantResponse, error) {
	return s.getFn(req)
}

func newClientAgainst(t *testing.T, stub *stubTenantService) *TenantClient {
	return NewTenantClient(bufconnDial(t, func(s *grpc.Server) {
		kryptonadmin.RegisterTenantServiceServer(s, stub)
	}))
}

func TestCreateTenantReturnsKryptonID(t *testing.T) {
	// given
	var seen string
	c := newClientAgainst(t, &stubTenantService{
		createFn: func(req *kryptonadmin.CreateTenantRequest) (*kryptonadmin.CreateTenantResponse, error) {
			seen = req.GetName()
			return &kryptonadmin.CreateTenantResponse{
				Tenant: &kryptonadmin.Tenant{Id: testTenantID, Name: req.GetName()},
			}, nil
		},
	})

	// when
	resp, err := c.CreateTenant(t.Context(), openkcmapi.CreateTenantRequest{Name: testTenantName})

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen != testTenantName {
		t.Errorf("name sent to krypton = %q, want %q", seen, testTenantName)
	}
	if resp.ID != testTenantID {
		t.Errorf("ID = %q, want %q", resp.ID, testTenantID)
	}
	// Krypton has no provisioning state, so a created tenant is ready at once.
	if resp.ProcessingState != openkcmapi.ProcessingStateReady {
		t.Errorf("ProcessingState = %q, want %q", resp.ProcessingState, openkcmapi.ProcessingStateReady)
	}
}

func TestCreateTenantRejectsEmptyResponse(t *testing.T) {
	// given
	c := newClientAgainst(t, &stubTenantService{
		createFn: func(*kryptonadmin.CreateTenantRequest) (*kryptonadmin.CreateTenantResponse, error) {
			return &kryptonadmin.CreateTenantResponse{}, nil
		},
	})

	// when
	_, err := c.CreateTenant(t.Context(), openkcmapi.CreateTenantRequest{Name: "acme"})

	// then
	if err == nil {
		t.Fatal("a response without a tenant must be an error, not a zero ID")
	}
}

func TestCreateTenantTranslatesAlreadyExists(t *testing.T) {
	// given
	c := newClientAgainst(t, &stubTenantService{
		createFn: func(*kryptonadmin.CreateTenantRequest) (*kryptonadmin.CreateTenantResponse, error) {
			return nil, status.Error(codes.AlreadyExists, "tenant acme already registered")
		},
	})

	// when
	_, err := c.CreateTenant(t.Context(), openkcmapi.CreateTenantRequest{Name: "acme"})

	// then
	var apiErr *openkcmapi.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error must be classifiable as *openkcmapi.APIError, got %T: %v", err, err)
	}
	if apiErr.Code != codes.AlreadyExists.String() {
		t.Errorf("Code = %q, want %q", apiErr.Code, codes.AlreadyExists.String())
	}
	if apiErr.StatusCode != 409 {
		t.Errorf("StatusCode = %d, want 409", apiErr.StatusCode)
	}
}

func TestGetTenantTranslatesNotFound(t *testing.T) {
	// given
	c := newClientAgainst(t, &stubTenantService{
		getFn: func(*kryptonadmin.GetTenantRequest) (*kryptonadmin.GetTenantResponse, error) {
			return nil, status.Error(codes.NotFound, "no such tenant")
		},
	})

	// when
	_, err := c.GetTenant(t.Context(), "missing")

	// then
	var apiErr *openkcmapi.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error must be classifiable as *openkcmapi.APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

func TestGetTenantReturnsID(t *testing.T) {
	// given
	c := newClientAgainst(t, &stubTenantService{
		getFn: func(req *kryptonadmin.GetTenantRequest) (*kryptonadmin.GetTenantResponse, error) {
			return &kryptonadmin.GetTenantResponse{
				Tenant: &kryptonadmin.Tenant{Id: req.GetId(), Name: testTenantName},
			}, nil
		},
	})

	// when
	resp, err := c.GetTenant(t.Context(), testTenantID)

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != testTenantID {
		t.Errorf("ID = %q, want %q", resp.ID, testTenantID)
	}
}

// TestClassifyCodeMatchesLiveKrypton pins the mapping to what a live Krypton
// actually returned on 2026-09-07 (main). Each case is an observed response,
// not a guess from the proto.
func TestClassifyCodeMatchesLiveKrypton(t *testing.T) {
	tests := []struct {
		name      string
		observed  codes.Code
		wantKind  openkcmapi.Kind
		wantRetry bool
	}{
		// GetTenant / GetKey / ActivateKey against an id that does not exist.
		{"missing resource", codes.NotFound, openkcmapi.KindNotFound, false},
		// AnnounceKey while the parent key is still pre-activation, and
		// ActivateKey called twice ("cannot transition from active to active").
		{"precondition not met", codes.FailedPrecondition, openkcmapi.KindNotReady, true},
		// AnnounceKey with an unknown key kind.
		{"bad request", codes.InvalidArgument, openkcmapi.KindInvalid, false},
		// GetTenant with a malformed id answers Internal, not InvalidArgument.
		{"malformed id", codes.Internal, openkcmapi.KindTransient, true},
		{"backend down", codes.Unavailable, openkcmapi.KindTransient, true},
		{"no credentials", codes.Unauthenticated, openkcmapi.KindUnauthorized, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// given
			c := newClientAgainst(t, &stubTenantService{
				getFn: func(*kryptonadmin.GetTenantRequest) (*kryptonadmin.GetTenantResponse, error) {
					return nil, status.Error(tc.observed, "from krypton")
				},
			})

			// when
			_, err := c.GetTenant(t.Context(), "any")

			// then
			var apiErr *openkcmapi.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("not classifiable: %v", err)
			}
			if apiErr.Kind() != tc.wantKind {
				t.Errorf("Kind() = %v, want %v", apiErr.Kind(), tc.wantKind)
			}
			if openkcmapi.IsRetryable(err) != tc.wantRetry {
				t.Errorf("IsRetryable = %v, want %v", openkcmapi.IsRetryable(err), tc.wantRetry)
			}
		})
	}
}

func TestNotFoundIsRecognisedByHelper(t *testing.T) {
	// given
	c := newClientAgainst(t, &stubTenantService{
		getFn: func(*kryptonadmin.GetTenantRequest) (*kryptonadmin.GetTenantResponse, error) {
			return nil, status.Error(codes.NotFound, "tenant not found")
		},
	})

	// when
	_, err := c.GetTenant(t.Context(), "gone")

	// then
	if !openkcmapi.IsNotFound(err) {
		t.Fatalf("IsNotFound must recognise a missing tenant, got %v", err)
	}
	if openkcmapi.IsRetryable(err) {
		t.Error("a missing tenant must not be retried unchanged")
	}
}

func TestDeleteTenantIsUnsupported(t *testing.T) {
	// given
	c := newClientAgainst(t, &stubTenantService{})

	// when
	err := c.DeleteTenant(t.Context(), testTenantID)

	// then
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("want errors.ErrUnsupported, got %v", err)
	}
}
