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

// Package kryptongrpc talks to Krypton over its admin gRPC API.
//
// It replaces the invented HTTP surface in internal/openkcmapi one operation
// at a time. Only tenants are covered so far; the key operations need
// decisions that are still open (see knowledge base: krypton-local-setup.md).
package kryptongrpc

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	kryptonadmin "github.com/openkcm/krypton/pkg/api/v1/proto/admin"

	"github.com/openkcm/platform-mesh-controller/internal/openkcmapi"
)

// TenantClient implements the tenant operations against Krypton's
// TenantService.
type TenantClient struct {
	tenants kryptonadmin.TenantServiceClient
}

// NewTenantClient wraps an established gRPC connection. The caller owns the
// connection and its credentials; this package deliberately does not dial, so
// transport security stays a deployment decision.
func NewTenantClient(conn grpc.ClientConnInterface) *TenantClient {
	return &TenantClient{tenants: kryptonadmin.NewTenantServiceClient(conn)}
}

// CreateTenant registers the account with Krypton.
//
// Krypton's Tenant carries no provisioning state: a successful response means
// the tenant exists. ProcessingState is reported ready so the caller's
// create-then-poll loop settles on the next pass instead of spinning.
func (c *TenantClient) CreateTenant(
	ctx context.Context,
	req openkcmapi.CreateTenantRequest,
) (*openkcmapi.CreateTenantResponse, error) {
	resp, err := c.tenants.CreateTenant(ctx, &kryptonadmin.CreateTenantRequest{Name: req.Name})
	if err != nil {
		return nil, translate("CreateTenant", err)
	}
	if resp.GetTenant() == nil {
		return nil, fmt.Errorf("CreateTenant: krypton returned no tenant")
	}
	return &openkcmapi.CreateTenantResponse{
		ID:              resp.GetTenant().GetId(),
		ProcessingState: openkcmapi.ProcessingStateReady,
	}, nil
}

// GetTenant reads the tenant back by its Krypton id.
func (c *TenantClient) GetTenant(ctx context.Context, id string) (*openkcmapi.GetTenantResponse, error) {
	resp, err := c.tenants.GetTenant(ctx, &kryptonadmin.GetTenantRequest{Id: id})
	if err != nil {
		return nil, translate("GetTenant", err)
	}
	if resp.GetTenant() == nil {
		return nil, fmt.Errorf("GetTenant %s: krypton returned no tenant", id)
	}
	return &openkcmapi.GetTenantResponse{
		ID:              resp.GetTenant().GetId(),
		ProcessingState: openkcmapi.ProcessingStateReady,
	}, nil
}

// DeleteTenant always fails with errors.ErrUnsupported until Krypton has a delete RPC.
func (c *TenantClient) DeleteTenant(_ context.Context, id string) error {
	return fmt.Errorf("DeleteTenant %s: %w", id, errors.ErrUnsupported)
}

// translate maps a gRPC status onto a classified openkcmapi.APIError.
//
// The mapping was taken from a live Krypton (main, 2026-09-07), not from the
// proto: several codes are not what the names suggest. GetTenant with a
// malformed id answers Internal rather than InvalidArgument, and ActivateKey
// is not idempotent — a second call answers FailedPrecondition
// "cannot transition from active to active".
func translate(op string, err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("%s: %w", op, err)
	}
	kind, httpStatus := classifyCode(st.Code())
	return fmt.Errorf("%s: %w", op,
		openkcmapi.NewAPIError(kind, httpStatus, st.Code().String(), st.Message()))
}

// classifyCode turns a gRPC code into a handling decision plus the nearest HTTP
// status. The status is only kept because APIError still exposes it; nothing
// branches on it.
func classifyCode(c codes.Code) (openkcmapi.Kind, int) {
	switch c {
	case codes.NotFound:
		// GetTenant, GetKey and ActivateKey on a missing id.
		return openkcmapi.KindNotFound, 404
	case codes.AlreadyExists:
		return openkcmapi.KindConflict, 409
	case codes.FailedPrecondition:
		// Parent key not active yet, or a rejected lifecycle transition.
		// Both are worth retrying: the parent may activate, and a repeated
		// activation means the key already reached the state we wanted.
		return openkcmapi.KindNotReady, 409
	case codes.InvalidArgument, codes.OutOfRange:
		return openkcmapi.KindInvalid, 400
	case codes.Unauthenticated:
		return openkcmapi.KindUnauthorized, 401
	case codes.PermissionDenied:
		return openkcmapi.KindUnauthorized, 403
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Aborted:
		return openkcmapi.KindTransient, 503
	case codes.Internal, codes.Unknown, codes.DataLoss:
		// Krypton answers Internal for a malformed tenant id, so this is not
		// always a server fault. Retrying is still the safer default.
		return openkcmapi.KindTransient, 500
	case codes.Unimplemented:
		return openkcmapi.KindInvalid, 501
	default:
		return openkcmapi.KindUnknown, 500
	}
}
