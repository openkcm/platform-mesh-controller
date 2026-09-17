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
	"fmt"
	"strings"

	"google.golang.org/grpc"

	"github.com/openkcm/openkcm-controller/api/shared"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

// keyIDSeparator joins the tenant and key id; both are UUIDs, so it never collides.
const keyIDSeparator = "/"

// Backend adapts Krypton to the key operations the DomainKey reconciler drives.
type Backend struct {
	keys *KeyClient

	kindMap  map[string]string
	rootKind string
	rootName string
}

// BackendOptions translates a controller key level ("L2") to a Krypton kind
// ("K1") and names the tenant root that parents domain keys.
type BackendOptions struct {
	KindMap  map[string]string
	RootKind string
	RootName string
}

// NewBackend wraps an established gRPC connection. Empty options fall back to
// the kinds Krypton ships in examples/root.config.yaml.
func NewBackend(conn grpc.ClientConnInterface, opts BackendOptions) *Backend {
	kindMap := opts.KindMap
	if len(kindMap) == 0 {
		kindMap = map[string]string{"L2": "K1"}
	}
	rootKind := opts.RootKind
	if rootKind == "" {
		rootKind = "K0"
	}
	rootName := opts.RootName
	if rootName == "" {
		rootName = "openkcm-root"
	}
	return &Backend{
		keys:     NewKeyClient(conn),
		kindMap:  kindMap,
		rootKind: rootKind,
		rootName: rootName,
	}
}

// CreateKey seeds the tenant root, announces the key under it, and returns an
// opaque id carrying the tenant so later reads need no tenant of their own.
func (b *Backend) CreateKey(
	ctx context.Context, req openkcmapi.CreateKeyRequest,
) (*openkcmapi.CreateKeyResponse, error) {
	kind, ok := b.kindMap[req.Kind]
	if !ok {
		return nil, fmt.Errorf("no krypton kind configured for level %q", req.Kind)
	}

	rootID, err := b.ensureRoot(ctx, req.TenantID)
	if err != nil {
		return nil, err
	}

	child, err := b.keys.AnnounceKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: req.TenantID,
		Kind:     kind,
		Name:     req.Name,
		ParentID: rootID,
	})
	if err != nil {
		return nil, err
	}
	return &openkcmapi.CreateKeyResponse{
		ID:              packKeyID(req.TenantID, child.ID),
		ProcessingState: child.ProcessingState,
	}, nil
}

func (b *Backend) GetKey(ctx context.Context, id string) (*openkcmapi.GetKeyResponse, error) {
	tenantID, keyID, err := unpackKeyID(id)
	if err != nil {
		return nil, err
	}
	resp, err := b.keys.GetKey(ctx, keyID, tenantID)
	if err != nil {
		return nil, err
	}
	resp.ID = id
	return resp, nil
}

func (b *Backend) ActivateKey(ctx context.Context, id string) (*openkcmapi.ActivateKeyResponse, error) {
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

// DeactivateKey is not offered by Krypton yet.
func (b *Backend) DeactivateKey(_ context.Context, id string) (*openkcmapi.ActivateKeyResponse, error) {
	return nil, fmt.Errorf("DeactivateKey %s: %w", id, errors.ErrUnsupported)
}

// DeleteKey is not offered by Krypton.
func (b *Backend) DeleteKey(_ context.Context, id string) error {
	return fmt.Errorf("DeleteKey %s: %w", id, errors.ErrUnsupported)
}

// ensureRoot returns the tenant's active root, announcing and activating the
// static default one if absent. Announce is idempotent on (tenant, name).
func (b *Backend) ensureRoot(ctx context.Context, tenantID string) (string, error) {
	root, err := b.keys.AnnounceKey(ctx, openkcmapi.CreateKeyRequest{
		TenantID: tenantID,
		Kind:     b.rootKind,
		Name:     b.rootName,
	})
	if err != nil {
		return "", err
	}

	got, err := b.keys.GetKey(ctx, root.ID, tenantID)
	if err != nil {
		return "", err
	}
	if got.LifecycleState == string(shared.LifecycleActive) {
		return root.ID, nil
	}

	if _, err := b.keys.ActivateKey(ctx, root.ID, tenantID); err != nil {
		return "", err
	}
	return root.ID, nil
}

func packKeyID(tenantID, keyID string) string {
	return tenantID + keyIDSeparator + keyID
}

func unpackKeyID(id string) (tenantID, keyID string, err error) {
	tenantID, keyID, ok := strings.Cut(id, keyIDSeparator)
	if !ok || tenantID == "" || keyID == "" {
		return "", "", fmt.Errorf("malformed krypton key id %q", id)
	}
	return tenantID, keyID, nil
}
