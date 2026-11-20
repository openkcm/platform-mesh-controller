package kryptongrpc

import (
	"context"
	"fmt"

	kryptonkeys "github.com/openkcm/krypton/pkg/api/v1/proto/admin/keys"
	"github.com/openkcm/platform-mesh-controller/api/shared"
	"github.com/openkcm/platform-mesh-controller/internal/openkcmapi"
	"google.golang.org/grpc"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	kryptonProcessingCompleted = "completed"

	kryptonKeyLifecyclePreActivation = "pre-activation"
	kryptonKeyLifecycleActive        = "active"
	kryptonKeyLifecycleDeactivated   = "deactivated"
	kryptonKeyLifecycleSuspended     = "suspended"
	kryptonKeyLifecycleCompromised   = "compromised"
	kryptonKeyLifecycleDestroyed     = "destroyed"
)

type KeyClient struct {
	keys kryptonkeys.KeyServiceClient
}

func NewKeyClient(conn grpc.ClientConnInterface) *KeyClient {
	return &KeyClient{keys: kryptonkeys.NewKeyServiceClient(conn)}
}

func (c *KeyClient) AnnounceKey(ctx context.Context, req openkcmapi.CreateKeyRequest) (*openkcmapi.CreateKeyResponse, error) {
	resp, err := c.keys.AnnounceKey(ctx, &kryptonkeys.AnnounceKeyRequest{
		TenantId: req.TenantID,
		Kind:     req.Kind,
		Name:     req.Name,
		ParentId: req.ParentID,
		Labels:   req.Labels,
	})
	if err != nil {
		return nil, translate("AnnounceKey", err)
	}
	if resp.GetKey() == nil {
		return nil, fmt.Errorf("AnnounceKey: krypton returned no key")
	}
	log.FromContext(ctx).Info("Krypton AnnounceKey",
		"tenantID", req.TenantID, "kind", req.Kind, "name", req.Name, "parentID", req.ParentID,
		"keyID", resp.GetKey().GetId(), "lifecycle", resp.GetKey().GetLifeCycleState(),
		"processing", processingState(resp.GetKey()))
	return &openkcmapi.CreateKeyResponse{
		ID:              resp.GetKey().GetId(),
		ProcessingState: processingState(resp.GetKey()),
	}, nil
}

func (c *KeyClient) GetKey(ctx context.Context, id string, tenantID string) (*openkcmapi.GetKeyResponse, error) {
	resp, err := c.keys.GetKey(ctx, &kryptonkeys.GetKeyRequest{
		Id:       id,
		TenantId: tenantID,
	})
	if err != nil {
		return nil, translate("GetKey", err)
	}

	if resp.GetKey() == nil {
		return nil, fmt.Errorf("GetKey %s: krypton returned no key", id)
	}
	ls, err := lifecycleState(resp.GetKey())
	if err != nil {
		return nil, err
	}

	return &openkcmapi.GetKeyResponse{
		ID:              resp.GetKey().GetId(),
		ProcessingState: processingState(resp.GetKey()),
		LifecycleState:  string(ls),
	}, nil
}

func (c *KeyClient) ActivateKey(ctx context.Context, id string, tenantID string) (*openkcmapi.ActivateKeyResponse, error) {
	resp, err := c.keys.ActivateKey(ctx, &kryptonkeys.ActivateKeyRequest{
		Id:       id,
		TenantId: tenantID,
	})
	if err != nil {
		log.FromContext(ctx).Error(err, "Krypton ActivateKey failed", "tenantID", tenantID, "keyID", id)
		return nil, translate("ActivateKey", err)
	}

	got, err := c.GetKey(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	log.FromContext(ctx).Info("Krypton ActivateKey accepted (an asynchronous job)",
		"tenantID", tenantID, "keyID", id, "jobGroupID", resp.GetJobGroupId(),
		"lifecycle", got.LifecycleState, "processing", got.ProcessingState)
	return &openkcmapi.ActivateKeyResponse{
		ID:              got.ID,
		LifecycleState:  got.LifecycleState,
		ProcessingState: got.ProcessingState,
	}, nil
}

func (c *KeyClient) DeactivateKey(ctx context.Context, id string, tenantID string) (*openkcmapi.ActivateKeyResponse, error) {
	_, err := c.keys.DeactivateKey(ctx, &kryptonkeys.DeactivateKeyRequest{
		Id:       id,
		TenantId: tenantID,
	})
	if err != nil {
		return nil, translate("DeactivateKey", err)
	}

	got, err := c.GetKey(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	return &openkcmapi.ActivateKeyResponse{
		ID:             got.ID,
		LifecycleState: got.LifecycleState,
	}, nil
}

func processingState(key *kryptonkeys.Key) string {
	status := key.GetKeyProcessingState().GetStatus()
	if status == kryptonProcessingCompleted {
		return openkcmapi.ProcessingStateReady
	}

	return status
}

func lifecycleState(key *kryptonkeys.Key) (shared.LifecycleState, error) {
	switch key.GetLifeCycleState() {
	case kryptonKeyLifecyclePreActivation:
		return shared.LifecyclePreActive, nil
	case kryptonKeyLifecycleActive:
		return shared.LifecycleActive, nil
	case kryptonKeyLifecycleDeactivated:
		return shared.LifecycleDeactivated, nil
	case kryptonKeyLifecycleSuspended:
		return shared.LifecycleSuspended, nil
	case kryptonKeyLifecycleCompromised:
		return shared.LifecycleCompromised, nil
	case kryptonKeyLifecycleDestroyed:
		return shared.LifecycleDestroyed, nil
	default:
		return "", fmt.Errorf("unknown krypton lifecycle state %q", key.GetLifeCycleState())
	}
}
