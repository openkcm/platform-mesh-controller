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

package operations_test

import (
	"context"
	"errors"
	"sync"

	"github.com/openkcm/openkcm-controller/api/shared"
	operations "github.com/openkcm/openkcm-controller/internal/controller/operations"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

// testBackend is a hand-written Backend used by the reconciler tests. It
// records what it was asked to do and lets a test decide what each call
// returns, which is what makes error paths reachable at all.
type testBackend struct {
	mu sync.Mutex

	createTenantCalls []string
	getTenantCalls    []string
	deleteTenantCalls []string

	// noDelete makes DeleteTenant and DeleteKey answer like the Krypton client does.
	noDelete     bool
	noDeactivate bool
	deleteKeyErr error
	keyLifecycle shared.LifecycleState

	createKeyCalls   []openkcmapi.CreateKeyRequest
	activateKeyCalls []string
	deleteKeyCalls   []string

	createTenantFn func(openkcmapi.CreateTenantRequest) (*openkcmapi.CreateTenantResponse, error)
	getTenantFn    func(string) (*openkcmapi.GetTenantResponse, error)
	deleteTenantFn func(string) error
}

// testKeyID is what the test backend hands back for a created key.
const testKeyID = "key-uuid"

func (f *testBackend) CreateTenant(
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
	return &openkcmapi.CreateTenantResponse{ID: testTenantID, ProcessingState: "processing"}, nil
}

func (f *testBackend) GetTenant(_ context.Context, id string) (*openkcmapi.GetTenantResponse, error) {
	f.mu.Lock()
	f.getTenantCalls = append(f.getTenantCalls, id)
	fn := f.getTenantFn
	f.mu.Unlock()
	if fn != nil {
		return fn(id)
	}
	return &openkcmapi.GetTenantResponse{ID: id, ProcessingState: openkcmapi.ProcessingStateReady}, nil
}

func (f *testBackend) DeleteTenant(_ context.Context, id string) error {
	f.mu.Lock()
	f.deleteTenantCalls = append(f.deleteTenantCalls, id)
	fn := f.deleteTenantFn
	noDelete := f.noDelete
	f.mu.Unlock()
	if noDelete {
		return errors.ErrUnsupported
	}
	if fn != nil {
		return fn(id)
	}
	return nil
}

func (f *testBackend) counts() (create, get, del int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.createTenantCalls), len(f.getTenantCalls), len(f.deleteTenantCalls)
}

func (f *testBackend) CreateKey(
	_ context.Context, req openkcmapi.CreateKeyRequest,
) (*openkcmapi.CreateKeyResponse, error) {
	f.mu.Lock()
	f.createKeyCalls = append(f.createKeyCalls, req)
	f.mu.Unlock()
	return &openkcmapi.CreateKeyResponse{ID: testKeyID, ProcessingState: openkcmapi.ProcessingStateReady}, nil
}
func (f *testBackend) GetKey(_ context.Context, id string) (*openkcmapi.GetKeyResponse, error) {
	f.mu.Lock()
	lifecycle := f.keyLifecycle
	f.mu.Unlock()
	return &openkcmapi.GetKeyResponse{
		ID:              id,
		ProcessingState: openkcmapi.ProcessingStateReady,
		LifecycleState:  string(lifecycle),
	}, nil
}
func (f *testBackend) DeleteKey(_ context.Context, id string) error {
	f.mu.Lock()
	f.deleteKeyCalls = append(f.deleteKeyCalls, id)
	noDelete := f.noDelete
	deleteKeyErr := f.deleteKeyErr
	f.mu.Unlock()
	if noDelete {
		return errors.ErrUnsupported
	}
	return deleteKeyErr
}
func (f *testBackend) ActivateKey(_ context.Context, id string) (*openkcmapi.ActivateKeyResponse, error) {
	f.mu.Lock()
	f.activateKeyCalls = append(f.activateKeyCalls, id)
	f.mu.Unlock()
	return &openkcmapi.ActivateKeyResponse{
		ID:             id,
		LifecycleState: string(shared.LifecycleActive),
		Version:        1,
	}, nil
}
func (f *testBackend) DeactivateKey(_ context.Context, id string) (*openkcmapi.ActivateKeyResponse, error) {
	f.mu.Lock()
	noDeactivate := f.noDeactivate
	f.mu.Unlock()
	if noDeactivate {
		return nil, errors.ErrUnsupported
	}
	return &openkcmapi.ActivateKeyResponse{
		ID:             id,
		LifecycleState: string(shared.LifecycleDeactivated),
		Version:        1,
	}, nil
}
func (f *testBackend) CreateRootKey(
	context.Context, openkcmapi.CreateRootKeyRequest,
) (*openkcmapi.CreateRootKeyResponse, error) {
	return &openkcmapi.CreateRootKeyResponse{}, nil
}
func (f *testBackend) GetRootKey(context.Context, string) (*openkcmapi.GetRootKeyResponse, error) {
	return &openkcmapi.GetRootKeyResponse{}, nil
}
func (f *testBackend) DeleteRootKey(context.Context, string) error { return nil }
func (f *testBackend) CreateDEK(
	context.Context, openkcmapi.CreateDEKRequest,
) (*openkcmapi.CreateDEKResponse, error) {
	return &openkcmapi.CreateDEKResponse{}, nil
}
func (f *testBackend) GetDEK(ctx context.Context, id string) (*openkcmapi.GetDEKResponse, error) {
	key, err := f.GetKey(ctx, id)
	if err != nil {
		return nil, err
	}
	return &openkcmapi.GetDEKResponse{
		ID:              key.ID,
		ProcessingState: key.ProcessingState,
		LifecycleState:  key.LifecycleState,
	}, nil
}
func (f *testBackend) DeleteDEK(ctx context.Context, id string) error { return f.DeleteKey(ctx, id) }

var _ operations.Backend = (*testBackend)(nil)
