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

package operations

import (
	"context"

	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

// Backend is the key management service the reconcilers drive. It is declared
// here, in the consumer, so the set of operations reflects what the
// reconcilers actually need rather than everything a client happens to offer.
//
// openkcmapi.Client speaks HTTP to the in-process mock. A gRPC client against
// Krypton satisfies the same set and drops in without touching a reconciler.
//
// SuspendKey, DestroyKey and CompromiseKey exist on openkcmapi.Client but are
// deliberately absent: nothing calls them, and the operations they front are
// still backlog on the Krypton side (krypton-workspace#77, #79, #81).
type Backend interface {
	DomainKeyBackend

	CreateRootKey(ctx context.Context, req openkcmapi.CreateRootKeyRequest) (*openkcmapi.CreateRootKeyResponse, error)
	GetRootKey(ctx context.Context, id string) (*openkcmapi.GetRootKeyResponse, error)
	DeleteRootKey(ctx context.Context, id string) error

	CreateDEK(ctx context.Context, req openkcmapi.CreateDEKRequest) (*openkcmapi.CreateDEKResponse, error)
	GetDEK(ctx context.Context, id string) (*openkcmapi.GetDEKResponse, error)
	DeleteDEK(ctx context.Context, id string) error
}

// DomainKeyBackend is the slice the DomainKey reconciler drives: a tenant plus
// the key operations Krypton actually offers. It is separate so the DomainKey
// reconciler can bind to a Krypton-backed client that has no root-key or DEK
// support yet.
type DomainKeyBackend interface {
	TenantBackend

	CreateKey(ctx context.Context, req openkcmapi.CreateKeyRequest) (*openkcmapi.CreateKeyResponse, error)
	GetKey(ctx context.Context, id string) (*openkcmapi.GetKeyResponse, error)
	DeleteKey(ctx context.Context, id string) error

	ActivateKey(ctx context.Context, id string) (*openkcmapi.ActivateKeyResponse, error)
	DeactivateKey(ctx context.Context, id string) (*openkcmapi.ActivateKeyResponse, error)
}

// keyLifecycler is the minimal surface reconcileLifecycle drives.
type keyLifecycler interface {
	ActivateKey(ctx context.Context, id string) (*openkcmapi.ActivateKeyResponse, error)
	DeactivateKey(ctx context.Context, id string) (*openkcmapi.ActivateKeyResponse, error)
}

// TenantBackend is the slice of the backend the Tenant reconciler drives. It
// is separate because it is the only part with a counterpart in Krypton today:
// TenantService offers CreateTenant, GetTenant and ListTenants, and nothing
// else here maps one to one.
type TenantBackend interface {
	CreateTenant(ctx context.Context, req openkcmapi.CreateTenantRequest) (*openkcmapi.CreateTenantResponse, error)
	GetTenant(ctx context.Context, id string) (*openkcmapi.GetTenantResponse, error)
	// DeleteTenant fails with errors.ErrUnsupported when the backend cannot delete tenants.
	DeleteTenant(ctx context.Context, id string) error
}

var (
	_ Backend          = (*openkcmapi.Client)(nil)
	_ DomainKeyBackend = (*openkcmapi.Client)(nil)
)
