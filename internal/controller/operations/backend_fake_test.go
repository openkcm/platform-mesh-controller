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
	"errors"
	"sync"

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

	// noDelete makes DeleteTenant answer like the Krypton client does.
	noDelete bool

	createTenantFn func(openkcmapi.CreateTenantRequest) (*openkcmapi.CreateTenantResponse, error)
	getTenantFn    func(string) (*openkcmapi.GetTenantResponse, error)
	deleteTenantFn func(string) error
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
	return &openkcmapi.CreateTenantResponse{ID: testTenantID, ProcessingState: "processing"}, nil
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

func (f *fakeBackend) counts() (create, get, del int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.createTenantCalls), len(f.getTenantCalls), len(f.deleteTenantCalls)
}

// The key operations are unused by the Tenant tests; they exist so fakeBackend
// satisfies Backend. Extend them when the key reconcilers get the same
// treatment.

func (f *fakeBackend) CreateKey(
	context.Context, openkcmapi.CreateKeyRequest,
) (*openkcmapi.CreateKeyResponse, error) {
	return &openkcmapi.CreateKeyResponse{}, nil
}
func (f *fakeBackend) GetKey(context.Context, string) (*openkcmapi.GetKeyResponse, error) {
	return &openkcmapi.GetKeyResponse{}, nil
}
func (f *fakeBackend) DeleteKey(context.Context, string) error { return nil }
func (f *fakeBackend) ActivateKey(context.Context, string) (*openkcmapi.ActivateKeyResponse, error) {
	return &openkcmapi.ActivateKeyResponse{}, nil
}
func (f *fakeBackend) DeactivateKey(context.Context, string) (*openkcmapi.ActivateKeyResponse, error) {
	return &openkcmapi.ActivateKeyResponse{}, nil
}
func (f *fakeBackend) CreateRootKey(
	context.Context, openkcmapi.CreateRootKeyRequest,
) (*openkcmapi.CreateRootKeyResponse, error) {
	return &openkcmapi.CreateRootKeyResponse{}, nil
}
func (f *fakeBackend) GetRootKey(context.Context, string) (*openkcmapi.GetRootKeyResponse, error) {
	return &openkcmapi.GetRootKeyResponse{}, nil
}
func (f *fakeBackend) DeleteRootKey(context.Context, string) error { return nil }
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
