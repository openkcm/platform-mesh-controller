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

	"google.golang.org/grpc"

	kryptonrootkey "github.com/openkcm/krypton/pkg/api/v1/proto/admin/rootkey"
	"github.com/openkcm/platform-mesh-controller/internal/openkcmapi"
)

const defaultRootKeyKind = "L1"

type RootKeyBackend struct {
	keys      *KeyClient
	providers kryptonrootkey.ServiceClient
	kind      string
}

func NewRootKeyBackend(conn grpc.ClientConnInterface) *RootKeyBackend {
	return &RootKeyBackend{
		keys:      NewKeyClient(conn),
		providers: kryptonrootkey.NewServiceClient(conn),
		kind:      defaultRootKeyKind,
	}
}

func (b *RootKeyBackend) CreateRootKey(
	ctx context.Context, req openkcmapi.CreateRootKeyRequest,
) (*openkcmapi.CreateRootKeyResponse, error) {
	key, err := b.keys.AnnounceKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: req.TenantID,
		Kind:     b.kind,
		Name:     req.Name,
	})
	if err != nil {
		return nil, err
	}
	return &openkcmapi.CreateRootKeyResponse{
		ID:              packKeyID(req.TenantID, key.ID),
		ProcessingState: key.ProcessingState,
	}, nil
}

func (b *RootKeyBackend) GetRootKey(ctx context.Context, id string) (*openkcmapi.GetRootKeyResponse, error) {
	tenantID, keyID, err := unpackKeyID(id)
	if err != nil {
		return nil, err
	}
	key, err := b.keys.GetKey(ctx, keyID, tenantID)
	if err != nil {
		return nil, err
	}
	return &openkcmapi.GetRootKeyResponse{
		ID:              id,
		ProcessingState: key.ProcessingState,
		LifecycleState:  key.LifecycleState,
	}, nil
}

func (b *RootKeyBackend) ActivateKey(ctx context.Context, id string) (*openkcmapi.ActivateKeyResponse, error) {
	tenantID, keyID, err := unpackKeyID(id)
	if err != nil {
		return nil, err
	}
	resp, err := b.keys.ActivateKey(ctx, keyID, tenantID)
	if err != nil {
		return nil, err
	}
	resp.ID = id
	return resp, nil
}

func (b *RootKeyBackend) DeactivateKey(ctx context.Context, id string) (*openkcmapi.ActivateKeyResponse, error) {
	tenantID, keyID, err := unpackKeyID(id)
	if err != nil {
		return nil, err
	}
	resp, err := b.keys.DeactivateKey(ctx, keyID, tenantID)
	if err != nil {
		return nil, err
	}
	resp.ID = id
	return resp, nil
}

func (b *RootKeyBackend) DeleteRootKey(ctx context.Context, id string) error {
	tenantID, keyID, err := unpackKeyID(id)
	if err != nil {
		return err
	}
	if _, err := b.providers.RemoveProvider(ctx, &kryptonrootkey.RemoveProviderRequest{
		TenantId: tenantID,
		KeyId:    keyID,
	}); err != nil {
		return translate("RemoveProvider", err)
	}
	return nil
}
