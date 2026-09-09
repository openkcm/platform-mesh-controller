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
	"sync"

	"github.com/openkcm/openkcm-controller/api/shared"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

// fakeBackend is a hand-written Backend used by the reconciler tests. It
// records what it was asked to do and lets a test decide what each call
// returns, which is what makes error paths reachable at all.
type fakeBackend struct {
	mu sync.Mutex

	createTenantCalls []string
	getTenantCalls    []string
	deleteTenantCalls []string

	// noDelete makes the fake behave like the Krypton client, which has no
	// delete RPC at all.
	noDelete bool

	createRootKeyCalls []openkcmapi.CreateRootKeyRequest
	getRootKeyCalls    []string
	deleteRootKeyCalls []string
	activateKeyCalls   []string
	deactivateKeyCalls []string

	createTenantFn func(openkcmapi.CreateTenantRequest) (*openkcmapi.CreateTenantResponse, error)
	getTenantFn    func(string) (*openkcmapi.GetTenantResponse, error)
	deleteTenantFn func(string) error

	createRootKeyFn func(openkcmapi.CreateRootKeyRequest) (*openkcmapi.CreateRootKeyResponse, error)
	getRootKeyFn    func(string) (*openkcmapi.GetRootKeyResponse, error)
	activateKeyFn   func(string) (*openkcmapi.ActivateKeyResponse, error)
	deactivateKeyFn func(string) (*openkcmapi.ActivateKeyResponse, error)
	deleteRootKeyFn func(string) error
}

func (f *fakeBackend) CreateTenant(
	_ context.Context,
	req openkcmapi.CreateTenantRequest,
) (*openkcmapi.CreateTenantResponse, error) {
	f.mu.Lock()
	f.createTenantCalls = append(f.createTenantCalls, req.Name)
	fn := f.createTenantFn
	f.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return &openkcmapi.CreateTenantResponse{ID: "tenant-uuid", ProcessingState: "processing"}, nil
}

func (f *fakeBackend) GetTenant(_ context.Context, id string) (*openkcmapi.GetTenantResponse, error) {
	f.mu.Lock()
	f.getTenantCalls = append(f.getTenantCalls, id)
	fn := f.getTenantFn
	f.mu.Unlock()
	if fn != nil {
		return fn(id)
	}
	return &openkcmapi.GetTenantResponse{ID: id, ProcessingState: processingStateReady}, nil
}

func (f *fakeBackend) DeleteTenant(_ context.Context, id string) error {
	f.mu.Lock()
	f.deleteTenantCalls = append(f.deleteTenantCalls, id)
	fn := f.deleteTenantFn
	f.mu.Unlock()
	if fn != nil {
		return fn(id)
	}
	return nil
}

func (f *fakeBackend) SupportsTenantDeletion() bool { return !f.noDelete }

func (f *fakeBackend) counts() (create, get, del int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.createTenantCalls), len(f.getTenantCalls), len(f.deleteTenantCalls)
}

// rootKeyID is what the fake hands back for a registered root key. Tests match
// on it, so it must not collide with the tenant id.
const rootKeyID = "rootkey-uuid"

func (f *fakeBackend) CreateRootKey(
	_ context.Context, req openkcmapi.CreateRootKeyRequest,
) (*openkcmapi.CreateRootKeyResponse, error) {
	f.mu.Lock()
	f.createRootKeyCalls = append(f.createRootKeyCalls, req)
	fn := f.createRootKeyFn
	f.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return &openkcmapi.CreateRootKeyResponse{ID: rootKeyID, ProcessingState: processingStateReady}, nil
}

func (f *fakeBackend) GetRootKey(_ context.Context, id string) (*openkcmapi.GetRootKeyResponse, error) {
	f.mu.Lock()
	f.getRootKeyCalls = append(f.getRootKeyCalls, id)
	fn := f.getRootKeyFn
	f.mu.Unlock()
	if fn != nil {
		return fn(id)
	}
	return &openkcmapi.GetRootKeyResponse{ID: id, ProcessingState: processingStateReady}, nil
}

func (f *fakeBackend) DeleteRootKey(_ context.Context, id string) error {
	f.mu.Lock()
	f.deleteRootKeyCalls = append(f.deleteRootKeyCalls, id)
	fn := f.deleteRootKeyFn
	f.mu.Unlock()
	if fn != nil {
		return fn(id)
	}
	return nil
}

func (f *fakeBackend) ActivateKey(_ context.Context, id string) (*openkcmapi.ActivateKeyResponse, error) {
	f.mu.Lock()
	f.activateKeyCalls = append(f.activateKeyCalls, id)
	fn := f.activateKeyFn
	f.mu.Unlock()
	if fn != nil {
		return fn(id)
	}
	return &openkcmapi.ActivateKeyResponse{
		ID:             id,
		LifecycleState: string(shared.LifecycleActive),
		Version:        1,
	}, nil
}

func (f *fakeBackend) DeactivateKey(_ context.Context, id string) (*openkcmapi.ActivateKeyResponse, error) {
	f.mu.Lock()
	f.deactivateKeyCalls = append(f.deactivateKeyCalls, id)
	fn := f.deactivateKeyFn
	f.mu.Unlock()
	if fn != nil {
		return fn(id)
	}
	return &openkcmapi.ActivateKeyResponse{
		ID:             id,
		LifecycleState: string(shared.LifecycleDeactivated),
		Version:        1,
	}, nil
}

// The L2-L4 key operations are unused by these tests; they exist so
// fakeBackend satisfies Backend.

func (f *fakeBackend) CreateKey(
	context.Context, openkcmapi.CreateKeyRequest,
) (*openkcmapi.CreateKeyResponse, error) {
	return &openkcmapi.CreateKeyResponse{}, nil
}
func (f *fakeBackend) GetKey(context.Context, string) (*openkcmapi.GetKeyResponse, error) {
	return &openkcmapi.GetKeyResponse{}, nil
}
func (f *fakeBackend) DeleteKey(context.Context, string) error { return nil }
func (f *fakeBackend) CreateDEK(
	context.Context, openkcmapi.CreateDEKRequest,
) (*openkcmapi.CreateDEKResponse, error) {
	return &openkcmapi.CreateDEKResponse{}, nil
}
func (f *fakeBackend) GetDEK(context.Context, string) (*openkcmapi.GetDEKResponse, error) {
	return &openkcmapi.GetDEKResponse{}, nil
}
func (f *fakeBackend) DeleteDEK(context.Context, string) error { return nil }

var _ Backend = (*fakeBackend)(nil)
