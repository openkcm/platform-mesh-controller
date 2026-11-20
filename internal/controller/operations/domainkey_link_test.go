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
	"testing"

	operationsv1alpha1 "github.com/openkcm/platform-mesh-controller/api/operations/v1alpha1"
	"github.com/openkcm/platform-mesh-controller/internal/kryptongrpc"
	"github.com/openkcm/platform-mesh-controller/internal/openkcmapi"
)

type unsupportedL1Backend struct {
	removeErr error
	deleteErr error
}

func (unsupportedL1Backend) CreateRootKey(context.Context, openkcmapi.CreateRootKeyRequest) (*openkcmapi.CreateRootKeyResponse, error) {
	return nil, errors.ErrUnsupported
}

func (unsupportedL1Backend) GetRootKey(context.Context, string) (*openkcmapi.GetRootKeyResponse, error) {
	return nil, errors.ErrUnsupported
}

func (unsupportedL1Backend) CreateProvider(context.Context, string, openkcmapi.CreateProviderRequest) (*openkcmapi.GetRootKeyResponse, error) {
	return nil, errors.ErrUnsupported
}

func (b unsupportedL1Backend) RemoveProvider(context.Context, string) error { return b.removeErr }

func (unsupportedL1Backend) ActivateKey(context.Context, string) (*openkcmapi.ActivateKeyResponse, error) {
	return nil, errors.ErrUnsupported
}

func (b unsupportedL1Backend) DeleteRootKey(context.Context, string) error { return b.deleteErr }

func TestKryptonRootKeyBackendCanLinkRootKeys(t *testing.T) {
	var backend L1KeyBackend = (*kryptongrpc.RootKeyBackend)(nil)

	if _, ok := backend.(l1ProviderBackend); !ok {
		t.Fatal("the Krypton root key backend does not satisfy l1ProviderBackend, so syncRootKeyLink would skip every link")
	}
}

func TestDeleteL1KeySkipsWhatTheBackendCannotRemove(t *testing.T) {
	dk := &operationsv1alpha1.DomainKey{}
	dk.Status.L1KeyID = "tenant/l1"
	r := &DomainKeyReconciler{L1Keys: unsupportedL1Backend{
		removeErr: errors.ErrUnsupported,
		deleteErr: errors.ErrUnsupported,
	}}

	if err := r.deleteL1Key(context.Background(), dk); err != nil {
		t.Fatalf("an unsupported removal must not block the DomainKey deletion: %v", err)
	}
}

func TestDeleteL1KeyStillFailsOnARealError(t *testing.T) {
	dk := &operationsv1alpha1.DomainKey{}
	dk.Status.L1KeyID = "tenant/l1"
	boom := errors.New("backend down")
	r := &DomainKeyReconciler{L1Keys: unsupportedL1Backend{removeErr: boom}}

	if err := r.deleteL1Key(context.Background(), dk); !errors.Is(err, boom) {
		t.Fatalf("a real error must be returned for a retry, got %v", err)
	}
}
