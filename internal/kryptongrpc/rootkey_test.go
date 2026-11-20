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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	kryptonkeys "github.com/openkcm/krypton/pkg/api/v1/proto/admin/keys"
	kryptonrootkey "github.com/openkcm/krypton/pkg/api/v1/proto/admin/rootkey"

	"github.com/openkcm/platform-mesh-controller/api/shared"
	"github.com/openkcm/platform-mesh-controller/internal/kryptongrpc"
	"github.com/openkcm/platform-mesh-controller/internal/openkcmapi"
)

type testProviderServer struct {
	kryptonrootkey.UnimplementedServiceServer

	mu       sync.Mutex
	created  []*kryptonrootkey.CreateProviderRequest
	removed  []*kryptonrootkey.RemoveProviderRequest
	createFn func(*kryptonrootkey.CreateProviderRequest) error
}

func (s *testProviderServer) CreateProvider(
	_ context.Context, req *kryptonrootkey.CreateProviderRequest,
) (*kryptonrootkey.CreateProviderResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.created = append(s.created, req)
	if s.createFn != nil {
		if err := s.createFn(req); err != nil {
			return nil, err
		}
	}
	return &kryptonrootkey.CreateProviderResponse{}, nil
}

func (s *testProviderServer) RemoveProvider(
	_ context.Context, req *kryptonrootkey.RemoveProviderRequest,
) (*kryptonrootkey.RemoveProviderResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.removed = append(s.removed, req)
	return &kryptonrootkey.RemoveProviderResponse{}, nil
}

func newRootKeyBackendAgainst(
	t *testing.T, keys *testKeyServer, providers *testProviderServer,
) *kryptongrpc.RootKeyBackend {
	conn := bufconnDial(t, func(s *grpc.Server) {
		kryptonkeys.RegisterKeyServiceServer(s, keys)
		kryptonrootkey.RegisterServiceServer(s, providers)
	})
	return kryptongrpc.NewRootKeyBackend(conn)
}

func awsProviderRequest() openkcmapi.CreateProviderRequest {
	return openkcmapi.CreateProviderRequest{
		Provider: "aws",
		Config: map[string]string{
			"region":  "eu-central-1",
			"keyArn":  "arn:aws:kms:eu-central-1:111122223333:key/1234abcd",
			"roleArn": "arn:aws:iam::111122223333:role/openkcm-l1-access",
		},
		Auth: map[string]string{
			"accessKeyId":     "AKIAEXAMPLE",
			"secretAccessKey": "secret-value",
		},
	}
}

func announceL1(t *testing.T, backend *kryptongrpc.RootKeyBackend) string {
	t.Helper()
	created, err := backend.CreateRootKey(context.Background(), openkcmapi.CreateRootKeyRequest{
		TenantID: "tenant-1",
		Name:     "l1",
	})
	require.NoError(t, err)
	return created.ID
}

func TestRootKeyCreateProviderMapsAWSToKrypton(t *testing.T) {
	providers := &testProviderServer{}
	backend := newRootKeyBackendAgainst(t, newTestKeyServer(), providers)
	id := announceL1(t, backend)
	tenantID, keyID, err := kryptongrpc.UnpackKeyID(id)
	require.NoError(t, err)

	got, err := backend.CreateProvider(context.Background(), id, awsProviderRequest())

	require.NoError(t, err)
	require.Len(t, providers.created, 1)
	req := providers.created[0]
	assert.Equal(t, tenantID, req.GetTenantId())
	assert.Equal(t, keyID, req.GetKeyId())
	aws := req.GetProvider().GetAwsKms()
	require.NotNil(t, aws)
	assert.Equal(t, "eu-central-1", aws.GetConfig().GetRegion())
	assert.Equal(t, "arn:aws:kms:eu-central-1:111122223333:key/1234abcd", aws.GetProviderKey().GetKeyArn())
	assert.Equal(t, "arn:aws:iam::111122223333:role/openkcm-l1-access", aws.GetAuth().GetRoleArn())
	assert.Empty(t, aws.GetAuth().GetExternalId())
	static := aws.GetAuth().GetStatic()
	assert.Equal(t, "AKIAEXAMPLE", static.GetAccessKeyId())
	assert.Equal(t, "secret-value", static.GetSecretAccessKey())
	assert.Nil(t, aws.GetAuth().GetRuntime())

	assert.Equal(t, id, got.ID)
	assert.True(t, got.Linked)
	assert.Equal(t, "aws", got.Provider)
	assert.Equal(t, awsProviderRequest().Config, got.Config)
	assert.Equal(t, string(shared.LifecyclePreActive), got.LifecycleState)
	assert.Equal(t, openkcmapi.ProcessingStateReady, got.ProcessingState)
}

func TestRootKeyCreateProviderSendsNoRoleWhenNoneIsGiven(t *testing.T) {
	providers := &testProviderServer{}
	backend := newRootKeyBackendAgainst(t, newTestKeyServer(), providers)
	id := announceL1(t, backend)
	req := awsProviderRequest()
	delete(req.Config, "roleArn")

	_, err := backend.CreateProvider(context.Background(), id, req)

	require.NoError(t, err)
	require.Len(t, providers.created, 1)
	assert.Empty(t, providers.created[0].GetProvider().GetAwsKms().GetAuth().GetRoleArn())
}

func TestRootKeyCreateProviderRefusesOtherProviders(t *testing.T) {
	providers := &testProviderServer{}
	backend := newRootKeyBackendAgainst(t, newTestKeyServer(), providers)
	id := announceL1(t, backend)

	_, err := backend.CreateProvider(context.Background(), id, openkcmapi.CreateProviderRequest{Provider: "azure"})

	require.ErrorIs(t, err, errors.ErrUnsupported)
	assert.Empty(t, providers.created)
}

func TestRootKeyCreateProviderRejectsMalformedID(t *testing.T) {
	providers := &testProviderServer{}
	backend := newRootKeyBackendAgainst(t, newTestKeyServer(), providers)

	_, err := backend.CreateProvider(context.Background(), "no-separator", awsProviderRequest())

	require.Error(t, err)
	assert.Empty(t, providers.created)
}

func TestRootKeyCreateProviderTranslatesAPreconditionAsNotReady(t *testing.T) {
	providers := &testProviderServer{
		createFn: func(*kryptonrootkey.CreateProviderRequest) error {
			return status.Error(codes.FailedPrecondition, "key is not pre-activation")
		},
	}
	backend := newRootKeyBackendAgainst(t, newTestKeyServer(), providers)
	id := announceL1(t, backend)

	_, err := backend.CreateProvider(context.Background(), id, awsProviderRequest())

	require.Error(t, err)
	assert.True(t, openkcmapi.IsRetryable(err))
}

func TestRootKeyRemoveProviderIsUnsupportedAndCallsNothing(t *testing.T) {
	providers := &testProviderServer{}
	backend := newRootKeyBackendAgainst(t, newTestKeyServer(), providers)
	id := announceL1(t, backend)

	err := backend.RemoveProvider(context.Background(), id)

	require.ErrorIs(t, err, errors.ErrUnsupported)
	assert.Empty(t, providers.removed)
}

func TestRootKeyDeleteIsUnsupportedAndCallsNothing(t *testing.T) {
	providers := &testProviderServer{}
	backend := newRootKeyBackendAgainst(t, newTestKeyServer(), providers)
	id := announceL1(t, backend)

	err := backend.DeleteRootKey(context.Background(), id)

	require.ErrorIs(t, err, errors.ErrUnsupported)
	assert.Empty(t, providers.removed)
}
