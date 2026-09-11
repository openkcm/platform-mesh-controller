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

package mockapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// persister reads and writes the mock state to a single ConfigMap so
// the mock survives pod restarts. The ConfigMap has one data key,
// "state.json", holding the serialized tenants and keys. If no k8s
// config is available the persister falls back to no-op behaviour.
type persister struct {
	client    kubernetes.Interface
	namespace string
	name      string
	log       *slog.Logger
}

// persistedState is the JSON-serialised shape stored in the ConfigMap.
// It is intentionally flat and stable so the ConfigMap can be
// inspected or edited by hand during development.
type persistedState struct {
	Tenants map[string]*tenantRecord `json:"tenants"`
	Keys    map[string]*keyRecord    `json:"keys"`
}

const (
	stateDataKey     = "state.json"
	defaultStateCM   = "openkcm-mock-state"
	podNamespaceEnv  = "POD_NAMESPACE"
	stateCMEnv       = "MOCK_OPENKCM_STATE_CONFIGMAP"
	mockLabelKey     = "app.kubernetes.io/managed-by"
	mockLabelValue   = "openkcm-mockapi"
	mockPurposeLabel = "openkcm.io/purpose"
	mockPurposeValue = "mock-state"
)

// newPersister constructs a ConfigMap-backed persister using the pod's
// in-cluster credentials. If in-cluster config is not available (local
// dev) or any required env var is missing, it returns a nil persister
// which the store treats as "persistence disabled".
func newPersister(logger *slog.Logger) *persister {
	namespace := os.Getenv(podNamespaceEnv)
	if namespace == "" {
		logger.Info("persistence disabled: POD_NAMESPACE not set (running outside a pod?)")
		return nil
	}

	cfg, err := rest.InClusterConfig()
	if err != nil {
		logger.Info("persistence disabled: no in-cluster config", "error", err.Error())
		return nil
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logger.Warn("persistence disabled: failed to build kubernetes client", "error", err.Error())
		return nil
	}

	name := os.Getenv(stateCMEnv)
	if name == "" {
		name = defaultStateCM
	}

	logger.Info("mockapi persistence enabled",
		"namespace", namespace,
		"configmap", name,
	)

	return &persister{
		client:    client,
		namespace: namespace,
		name:      name,
		log:       logger.With("component", "mockapi-persister"),
	}
}

// load reads the state ConfigMap and returns the decoded payload. If
// the ConfigMap does not exist yet, an empty state is returned so the
// first call to save() will create it.
func (p *persister) load(ctx context.Context) (*persistedState, error) {
	if p == nil {
		return &persistedState{
			Tenants: map[string]*tenantRecord{},
			Keys:    map[string]*keyRecord{},
		}, nil
	}

	cm, err := p.client.CoreV1().ConfigMaps(p.namespace).Get(ctx, p.name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			p.log.Info("no existing state ConfigMap, starting empty")
			return &persistedState{
				Tenants: map[string]*tenantRecord{},
				Keys:    map[string]*keyRecord{},
			}, nil
		}
		return nil, fmt.Errorf("load state: %w", err)
	}

	raw, ok := cm.Data[stateDataKey]
	if !ok || raw == "" {
		return &persistedState{
			Tenants: map[string]*tenantRecord{},
			Keys:    map[string]*keyRecord{},
		}, nil
	}

	var state persistedState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}
	if state.Tenants == nil {
		state.Tenants = map[string]*tenantRecord{}
	}
	if state.Keys == nil {
		state.Keys = map[string]*keyRecord{}
	}
	for id, key := range state.Keys {
		if key == nil {
			delete(state.Keys, id)
			continue
		}
		if !validLifecycleState(key.LifecycleState) {
			return nil, fmt.Errorf("decode state: key %q has invalid lifecycleState %q", id, key.LifecycleState)
		}
	}

	p.log.Info("loaded mock state",
		"tenants", len(state.Tenants),
		"keys", len(state.Keys),
	)

	return &state, nil
}

// save writes the given state to the ConfigMap, creating it if needed.
// Safe to call concurrently: the caller is expected to hold the store
// mutex, so save() itself does not re-lock.
func (p *persister) save(ctx context.Context, state *persistedState) error {
	if p == nil {
		return nil
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}

	cm, err := p.client.CoreV1().ConfigMaps(p.namespace).Get(ctx, p.name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("get state configmap: %w", err)
		}
		// Create
		newCM := &corev1.ConfigMap{
			Name:      p.name,
			Namespace: p.namespace,
			Labels: map[string]string{
				mockLabelKey:     mockLabelValue,
				mockPurposeLabel: mockPurposeValue,
			},
			Annotations: map[string]string{
				"openkcm.io/description": "In-memory state of the mock OpenKCM API, persisted so pod restarts do not lose key material references.",
			},
			Data: map[string]string{stateDataKey: string(data)},
		}
		if _, err := p.client.CoreV1().ConfigMaps(p.namespace).Create(ctx, newCM, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create state configmap: %w", err)
		}
		return nil
	}

	// Update
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[stateDataKey] = string(data)
	if cm.Labels == nil {
		cm.Labels = map[string]string{}
	}
	cm.Labels[mockLabelKey] = mockLabelValue
	cm.Labels[mockPurposeLabel] = mockPurposeValue

	if _, err := p.client.CoreV1().ConfigMaps(p.namespace).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		// Optimistic-concurrency collisions are uncommon for a
		// single-pod mock but worth logging at debug level.
		return fmt.Errorf("update state configmap: %w", err)
	}

	return nil
}
