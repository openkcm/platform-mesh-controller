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
package kryptongrpc

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	kryptonadmin "github.com/openkcm/krypton/pkg/api/v1/proto/admin"

	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

type TenantClient struct {
	tenants kryptonadmin.TenantServiceClient
}

func NewTenantClient(conn grpc.ClientConnInterface) *TenantClient {
	return &TenantClient{tenants: kryptonadmin.NewTenantServiceClient(conn)}
}

// CreateTenant registers the account. Krypton has no provisioning state, so the
// response reports ready and the caller's poll loop settles on the next pass.
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

// translate maps a gRPC status onto a classified openkcmapi.APIError. The codes
// were checked against a live Krypton, not the proto: several are not what their
// names suggest.
func translate(op string, err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("%s: %w", op, err)
	}
	kind, httpStatus := classifyCode(st.Code())
	return fmt.Errorf("%s: %w", op,
		openkcmapi.NewAPIError(kind, httpStatus, st.Code().String(), st.Message()))
}

// classifyCode turns a gRPC code into a handling decision plus the nearest HTTP status.
func classifyCode(c codes.Code) (openkcmapi.Kind, int) {
	switch c {
	case codes.NotFound:
		return openkcmapi.KindNotFound, 404
	case codes.AlreadyExists:
		return openkcmapi.KindConflict, 409
	case codes.FailedPrecondition:
		// Parent not active yet, or a repeated lifecycle transition; both are worth retrying.
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
		// Krypton answers Internal for a malformed id, so retrying is the safer default.
		return openkcmapi.KindTransient, 500
	case codes.Unimplemented:
		return openkcmapi.KindInvalid, 501
	default:
		return openkcmapi.KindUnknown, 500
	}
}
