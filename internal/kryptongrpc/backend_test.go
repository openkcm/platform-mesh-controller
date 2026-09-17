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
	"sync"
	"testing"

	"google.golang.org/grpc"

	kryptonkeys "github.com/openkcm/krypton/pkg/api/v1/proto/admin/keys"

	"github.com/openkcm/openkcm-controller/api/shared"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

// fakeKeyServer mimics Krypton's KeyService closely enough to exercise the
// adapter: announce is idempotent on (tenant, name), a fresh key is
// pre-activation, and activate flips it to active.
type fakeKeyServer struct {
	kryptonkeys.UnimplementedKeyServiceServer

	mu           sync.Mutex
	byName       map[string]*kryptonkeys.Key
	byID         map[string]*kryptonkeys.Key
	seq          int
	announceLog  []*kryptonkeys.AnnounceKeyRequest
	activateSeen []string
}

func newFakeKeyServer() *fakeKeyServer {
	return &fakeKeyServer{
		byName: map[string]*kryptonkeys.Key{},
		byID:   map[string]*kryptonkeys.Key{},
	}
}

func (s *fakeKeyServer) AnnounceKey(
	_ context.Context, req *kryptonkeys.AnnounceKeyRequest,
) (*kryptonkeys.AnnounceKeyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.announceLog = append(s.announceLog, req)
	nameKey := req.GetTenantId() + "|" + req.GetName()
	if existing, ok := s.byName[nameKey]; ok {
		return &kryptonkeys.AnnounceKeyResponse{Key: existing}, nil
	}
	s.seq++
	key := &kryptonkeys.Key{
		Id:                 fmt.Sprintf("key-%d", s.seq),
		Name:               req.GetName(),
		TenantId:           req.GetTenantId(),
		Kind:               req.GetKind(),
		ParentId:           req.GetParentId(),
		LifeCycleState:     kryptonKeyLifecyclePreActivation,
		KeyProcessingState: &kryptonkeys.KeyProcessingState{Status: kryptonProcessingCompleted},
	}
	s.byName[nameKey] = key
	s.byID[req.GetTenantId()+"|"+key.Id] = key
	return &kryptonkeys.AnnounceKeyResponse{Key: key}, nil
}

func (s *fakeKeyServer) GetKey(
	_ context.Context, req *kryptonkeys.GetKeyRequest,
) (*kryptonkeys.GetKeyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, ok := s.byID[req.GetTenantId()+"|"+req.GetId()]
	if !ok {
		return &kryptonkeys.GetKeyResponse{}, nil
	}
	return &kryptonkeys.GetKeyResponse{Key: key}, nil
}

func (s *fakeKeyServer) ActivateKey(
	_ context.Context, req *kryptonkeys.ActivateKeyRequest,
) (*kryptonkeys.ActivateKeyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.activateSeen = append(s.activateSeen, req.GetId())
	if key, ok := s.byID[req.GetTenantId()+"|"+req.GetId()]; ok {
		key.LifeCycleState = kryptonKeyLifecycleActive
	}
	return &kryptonkeys.ActivateKeyResponse{}, nil
}

func newBackendAgainst(t *testing.T, srv *fakeKeyServer) *Backend {
	conn := bufconnDial(t, func(s *grpc.Server) {
		kryptonkeys.RegisterKeyServiceServer(s, srv)
	})
	return NewBackend(conn, BackendOptions{})
}

func TestBackendCreateKeySeedsRootAndPacksTenant(t *testing.T) {
	// given
	srv := newFakeKeyServer()
	b := newBackendAgainst(t, srv)

	// when
	resp, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{
		TenantID: "tenant-1", Kind: "L2", Name: "acme.domain",
	})

	// then
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if resp.ID != "tenant-1/key-2" {
		t.Errorf("packed id = %q, want %q", resp.ID, "tenant-1/key-2")
	}

	// the root (K0) was announced and activated before the domain key (K1)
	if len(srv.announceLog) != 2 {
		t.Fatalf("announced %d keys, want 2 (root + domain)", len(srv.announceLog))
	}
	if srv.announceLog[0].GetKind() != "K0" || srv.announceLog[0].GetParentId() != "" {
		t.Errorf("first announce = kind %q parent %q, want K0 with no parent",
			srv.announceLog[0].GetKind(), srv.announceLog[0].GetParentId())
	}
	if srv.announceLog[1].GetKind() != "K1" || srv.announceLog[1].GetParentId() != "key-1" {
		t.Errorf("second announce = kind %q parent %q, want K1 under key-1",
			srv.announceLog[1].GetKind(), srv.announceLog[1].GetParentId())
	}
	if len(srv.activateSeen) != 1 || srv.activateSeen[0] != "key-1" {
		t.Errorf("activated %v, want only the root key-1", srv.activateSeen)
	}
}

func TestBackendReusesRootAcrossKeys(t *testing.T) {
	// given a backend that already made one key
	srv := newFakeKeyServer()
	b := newBackendAgainst(t, srv)
	if _, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{TenantID: "t", Kind: "L2", Name: "a"}); err != nil {
		t.Fatalf("first CreateKey: %v", err)
	}

	// when a second key is created for the same tenant
	if _, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{TenantID: "t", Kind: "L2", Name: "b"}); err != nil {
		t.Fatalf("second CreateKey: %v", err)
	}

	// then the root is announced again by name but not re-activated
	roots := 0
	for _, a := range srv.announceLog {
		if a.GetKind() == "K0" {
			roots++
		}
	}
	if roots != 2 {
		t.Errorf("root announced %d times, want 2 idempotent calls", roots)
	}
	if len(srv.activateSeen) != 1 {
		t.Errorf("root activated %d times, want 1", len(srv.activateSeen))
	}
}

func TestBackendGetAndActivateUnpackTenant(t *testing.T) {
	// given a created key
	srv := newFakeKeyServer()
	b := newBackendAgainst(t, srv)
	created, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{TenantID: "tid", Kind: "L2", Name: "k"})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	// when reading and activating it by the opaque id
	got, err := b.GetKey(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("GetKey id = %q, want the opaque id %q", got.ID, created.ID)
	}
	act, err := b.ActivateKey(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("ActivateKey: %v", err)
	}
	if act.LifecycleState != string(shared.LifecycleActive) {
		t.Errorf("activated lifecycle = %q, want Active", act.LifecycleState)
	}
}

func TestBackendRejectsUnknownLevel(t *testing.T) {
	b := newBackendAgainst(t, newFakeKeyServer())
	if _, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{TenantID: "t", Kind: "L9", Name: "x"}); err == nil {
		t.Fatal("an unconfigured level must be rejected")
	}
}

func TestBackendRejectsMalformedID(t *testing.T) {
	b := newBackendAgainst(t, newFakeKeyServer())
	if _, err := b.GetKey(t.Context(), "no-separator"); err == nil {
		t.Fatal("a key id without a tenant must be rejected")
	}
}

func TestBackendKeyOpsUnsupportedByKrypton(t *testing.T) {
	b := newBackendAgainst(t, newFakeKeyServer())
	if _, err := b.DeactivateKey(t.Context(), "t/k"); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("DeactivateKey error = %v, want ErrUnsupported", err)
	}
	if err := b.DeleteKey(t.Context(), "t/k"); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("DeleteKey error = %v, want ErrUnsupported", err)
	}
}
