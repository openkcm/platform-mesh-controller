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

package kryptongrpc_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	kryptonkeys "github.com/openkcm/krypton/pkg/api/v1/proto/admin/keys"

	"github.com/openkcm/openkcm-controller/api/shared"
	"github.com/openkcm/openkcm-controller/internal/kryptongrpc"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

// testKeyServer mimics Krypton's KeyService closely enough to exercise the
// adapter: announce is idempotent on (tenant, name), a fresh key is
// pre-activation, and activate flips it to active.
type testKeyServer struct {
	kryptonkeys.UnimplementedKeyServiceServer

	mu           sync.Mutex
	byName       map[string]*kryptonkeys.Key
	byID         map[string]*kryptonkeys.Key
	seq          int
	announceLog  []*kryptonkeys.AnnounceKeyRequest
	activateSeen []string
}

func newTestKeyServer() *testKeyServer {
	return &testKeyServer{
		byName: map[string]*kryptonkeys.Key{},
		byID:   map[string]*kryptonkeys.Key{},
	}
}

func (s *testKeyServer) AnnounceKey(
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
		LifeCycleState:     kryptongrpc.KryptonKeyLifecyclePreActivation,
		KeyProcessingState: &kryptonkeys.KeyProcessingState{Status: kryptongrpc.KryptonProcessingCompleted},
	}
	s.byName[nameKey] = key
	s.byID[req.GetTenantId()+"|"+key.Id] = key
	return &kryptonkeys.AnnounceKeyResponse{Key: key}, nil
}

func (s *testKeyServer) GetKey(
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

func (s *testKeyServer) ActivateKey(
	_ context.Context, req *kryptonkeys.ActivateKeyRequest,
) (*kryptonkeys.ActivateKeyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.activateSeen = append(s.activateSeen, req.GetId())
	if key, ok := s.byID[req.GetTenantId()+"|"+req.GetId()]; ok {
		key.LifeCycleState = kryptongrpc.KryptonKeyLifecycleActive
	}
	return &kryptonkeys.ActivateKeyResponse{}, nil
}

func newBackendAgainst(t *testing.T, srv *testKeyServer) *kryptongrpc.Backend {
	conn := bufconnDial(t, func(s *grpc.Server) {
		kryptonkeys.RegisterKeyServiceServer(s, srv)
	})
	return kryptongrpc.NewBackend(conn, kryptongrpc.BackendOptions{})
}

func TestBackendCreateKeySeedsRootAndPacksTenant(t *testing.T) {
	// given
	srv := newTestKeyServer()
	b := newBackendAgainst(t, srv)

	// when
	resp, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{
		TenantID: "tenant-1", Kind: "L2", Name: testDomainKeyName,
	})

	// then
	require.NoError(t, err, "CreateKey")
	assert.Equal(t, "tenant-1/key-2", resp.ID, "packed id")

	// the root (K0) was announced and activated before the domain key (K1)
	require.Len(t, srv.announceLog, 2, "announced keys, want 2 (root + domain)")
	assert.Equal(t, "K0", srv.announceLog[0].GetKind(), "first announce kind, want K0 with no parent")
	assert.Empty(t, srv.announceLog[0].GetParentId(), "first announce parent, want K0 with no parent")
	assert.Equal(t, "K1", srv.announceLog[1].GetKind(), "second announce kind, want K1 under key-1")
	assert.Equal(t, "key-1", srv.announceLog[1].GetParentId(), "second announce parent, want K1 under key-1")
	assert.Equal(t, []string{"key-1"}, srv.activateSeen, "activated, want only the root key-1")
}

const (
	testDomainKeyName  = "acme.domain"
	testServiceKeyName = "acme.service"
)

func TestBackendCreateKeyUnderParent(t *testing.T) {
	// given
	srv := newTestKeyServer()
	b := newBackendAgainst(t, srv)
	domain, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{
		TenantID: testKeyTenantID, Kind: "L2", Name: testDomainKeyName,
	})
	require.NoError(t, err, "CreateKey domain key")

	// when
	service, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{
		TenantID: testKeyTenantID, Kind: "L3", Name: testServiceKeyName, ParentID: domain.ID,
	})

	// then
	require.NoError(t, err, "CreateKey service key")
	assert.Equal(t, testKeyTenantID+"/key-3", service.ID, "packed id")
	require.Len(t, srv.announceLog, 3, "announced keys, want root, domain and service")
	assert.Equal(t, "K2", srv.announceLog[2].GetKind(), "service key kind")
	assert.Equal(t, "key-2", srv.announceLog[2].GetParentId(), "service key parent")
}

func TestBackendCreateDEKUnderServiceKey(t *testing.T) {
	// given
	srv := newTestKeyServer()
	b := newBackendAgainst(t, srv)
	domain, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{
		TenantID: testKeyTenantID, Kind: "L2", Name: testDomainKeyName,
	})
	require.NoError(t, err, "CreateKey domain key")
	service, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{
		TenantID: testKeyTenantID, Kind: "L3", Name: testServiceKeyName, ParentID: domain.ID,
	})
	require.NoError(t, err, "CreateKey service key")

	// when
	dek, err := b.CreateDEK(t.Context(), openkcmapi.CreateDEKRequest{
		TenantID: testKeyTenantID, ServiceKeyID: service.ID, Name: "acme.dek",
	})

	// then
	require.NoError(t, err, "CreateDEK")
	assert.Equal(t, testKeyTenantID+"/key-4", dek.ID, "packed id")
	require.Len(t, srv.announceLog, 4, "announced keys, want root, domain, service and data encryption key")
	assert.Equal(t, "K3", srv.announceLog[3].GetKind(), "data encryption key kind")
	assert.Equal(t, "key-3", srv.announceLog[3].GetParentId(), "data encryption key parent")
	got, err := b.GetDEK(t.Context(), dek.ID)
	require.NoError(t, err, "GetDEK")
	assert.Equal(t, dek.ID, got.ID, "GetDEK id")
	assert.Equal(t, string(shared.LifecyclePreActive), got.LifecycleState, "GetDEK lifecycle")
}

func TestBackendRejectsMalformedParent(t *testing.T) {
	// given
	srv := newTestKeyServer()
	b := newBackendAgainst(t, srv)

	// when
	_, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{
		TenantID: testKeyTenantID, Kind: "L3", Name: testServiceKeyName, ParentID: "no-separator",
	})

	// then
	require.Error(t, err, "a parent id without a tenant must be rejected")
	assert.Empty(t, srv.announceLog, "announced keys")
}

func TestBackendRejectsParentOfAnotherTenant(t *testing.T) {
	// given
	srv := newTestKeyServer()
	b := newBackendAgainst(t, srv)

	// when
	_, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{
		TenantID: testKeyTenantID, Kind: "L3", Name: testServiceKeyName, ParentID: "tenant-2/key-2",
	})

	// then
	require.Error(t, err, "a parent from another tenant must be rejected")
	assert.Empty(t, srv.announceLog, "announced keys")
}

func TestBackendReusesRootAcrossKeys(t *testing.T) {
	// given a backend that already made one key
	srv := newTestKeyServer()
	b := newBackendAgainst(t, srv)
	_, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{TenantID: "t", Kind: "L2", Name: "a"})
	require.NoError(t, err, "first CreateKey")

	// when a second key is created for the same tenant
	_, err = b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{TenantID: "t", Kind: "L2", Name: "b"})
	require.NoError(t, err, "second CreateKey")

	// then the root is announced again by name but not re-activated
	roots := 0
	for _, a := range srv.announceLog {
		if a.GetKind() == "K0" {
			roots++
		}
	}
	assert.Equal(t, 2, roots, "root announcements, want 2 idempotent calls")
	assert.Len(t, srv.activateSeen, 1, "root activations")
}

func TestBackendGetAndActivateUnpackTenant(t *testing.T) {
	// given a created key
	srv := newTestKeyServer()
	b := newBackendAgainst(t, srv)
	created, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{TenantID: "tid", Kind: "L2", Name: "k"})
	require.NoError(t, err, "CreateKey")

	// when reading and activating it by the opaque id
	got, err := b.GetKey(t.Context(), created.ID)
	require.NoError(t, err, "GetKey")
	assert.Equal(t, created.ID, got.ID, "GetKey id, want the opaque id")
	act, err := b.ActivateKey(t.Context(), created.ID)
	require.NoError(t, err, "ActivateKey")
	assert.Equal(t, string(shared.LifecycleActive), act.LifecycleState, "activated lifecycle")
}

func TestBackendRejectsUnknownLevel(t *testing.T) {
	b := newBackendAgainst(t, newTestKeyServer())
	_, err := b.CreateKey(t.Context(), openkcmapi.CreateKeyRequest{TenantID: "t", Kind: "L9", Name: "x"})
	require.Error(t, err, "an unconfigured level must be rejected")
}

func TestBackendRejectsMalformedID(t *testing.T) {
	b := newBackendAgainst(t, newTestKeyServer())
	_, err := b.GetKey(t.Context(), "no-separator")
	require.Error(t, err, "a key id without a tenant must be rejected")
}

func TestBackendKeyOpsUnsupportedByKrypton(t *testing.T) {
	b := newBackendAgainst(t, newTestKeyServer())
	_, err := b.DeactivateKey(t.Context(), "t/k")
	assert.ErrorIs(t, err, errors.ErrUnsupported, "DeactivateKey error")
	assert.ErrorIs(t, b.DeleteKey(t.Context(), "t/k"), errors.ErrUnsupported, "DeleteKey error")
	assert.ErrorIs(t, b.DeleteDEK(t.Context(), "t/k"), errors.ErrUnsupported, "DeleteDEK error")
}
