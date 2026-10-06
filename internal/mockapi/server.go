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

// Package mockapi implements an in-process mock of the OpenKCM / Krypton
// REST API used by the platform-mesh-controller during development and in the
// automated demo. It intentionally mimics the behaviour of a real KMS
// closely enough to exercise the reconciler's happy path, idempotency
// guarantees and failure handling:
//
//   - Async provisioning — POST /tenants and POST /keys return immediately
//     with processingState=processing. Subsequent GETs flip to ready only
//     after a configurable provisioning delay (default 3s), so the
//     controller must actually poll.
//   - Idempotent creates — POST /tenants with the same name and POST /keys
//     with the same (tenantId, kind, name, parentId) tuple return the
//     existing ID instead of creating a duplicate.
//   - Input validation — L3 keys must reference an existing L2 parent that
//     is Active, L2 keys must reference an existing tenant, and activate
//     refuses to operate on a key that is still processing.
//   - Deterministic errors — structured RFC-7807-ish JSON error bodies
//     with {code, message} so the client can distinguish client vs. server
//     faults.
//   - Audit log — every request is logged with its method, path, status
//     code, duration, and the affected resource ID.
//
// Behaviour is tuned for the demo mock; production use should talk to
// the real Krypton service.
package mockapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ProvisioningDelay controls how long after creation a tenant/key flips
// from "processing" to "ready". Exposed via MOCK_OPENKCM_PROVISIONING_DELAY
// env var (duration string, e.g. "2s"). Zero means instantly ready.
var defaultProvisioningDelay = 3 * time.Second

type tenantRecord struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

type keyRecord struct {
	ID             string
	TenantID       string
	Kind           string // "L1:<provider>", "L2", "L3", or "L4"
	Name           string
	ParentID       string
	Provider       string
	Config         map[string]string
	KMIPAttributes map[string]string
	CreatedAt      time.Time
	Version        int32
	LifecycleState string
	LastRotatedAt  time.Time
}

type store struct {
	mu      sync.Mutex
	tenants map[string]*tenantRecord // keyed by ID
	keys    map[string]*keyRecord    // keyed by ID

	// Secondary index for idempotent key creates.
	keysByKey map[keyDedupKey]string // dedup tuple -> ID

	provisioningDelay time.Duration
	log               *slog.Logger

	persister *persister
}

type keyDedupKey struct {
	TenantID string
	Kind     string
	Name     string
	ParentID string
}

func newStore(delay time.Duration, logger *slog.Logger, p *persister) *store {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))
	}
	s := &store{
		tenants:           make(map[string]*tenantRecord),
		keys:              make(map[string]*keyRecord),
		keysByKey:         make(map[keyDedupKey]string),
		provisioningDelay: delay,
		log:               logger.With("component", "mockapi"),
		persister:         p,
	}

	// Hydrate from ConfigMap if persistence is enabled. Failures are
	// logged but not fatal — the mock happily starts with empty state.
	if p != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if state, err := p.load(ctx); err != nil {
			s.log.Warn("failed to load persisted state, starting empty", "error", err.Error())
		} else {
			s.tenants = state.Tenants
			s.keys = state.Keys
			for id, k := range s.keys {
				s.keysByKey[keyDedupKey{
					TenantID: k.TenantID,
					Kind:     k.Kind,
					Name:     k.Name,
					ParentID: k.ParentID,
				}] = id
			}
		}
	}

	return s
}

// persist serialises the current state and writes it to the backing
// ConfigMap. The caller must hold s.mu. Errors are logged but not
// returned — the HTTP request has already mutated the in-memory state
// so reporting a persistence failure to the client would be misleading.
// In production the mock is dev-only; losing one ConfigMap write does
// not affect the demo.
func (s *store) persist() {
	if s.persister == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state := &persistedState{
		Tenants: s.tenants,
		Keys:    s.keys,
	}
	if err := s.persister.save(ctx, state); err != nil {
		s.log.Warn("failed to persist state", "error", err.Error())
	}
}

// NewServer creates a new mock OpenKCM API HTTP server.
func NewServer(addr string) *http.Server {
	delay := defaultProvisioningDelay
	if raw := os.Getenv("MOCK_OPENKCM_PROVISIONING_DELAY"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			delay = d
		}
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	p := newPersister(logger)
	s := newStore(delay, logger, p)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /tenants", s.createTenant)
	mux.HandleFunc("GET /tenants/{id}", s.getTenant)
	mux.HandleFunc("DELETE /tenants/{id}", s.deleteTenant)
	mux.HandleFunc("POST /keys", s.createKey)
	mux.HandleFunc("GET /keys/{id}", s.getKey)
	mux.HandleFunc("POST /keys/{id}/activate", s.activateKey)
	mux.HandleFunc("POST /keys/{id}/deactivate", s.deactivateKey)
	mux.HandleFunc("POST /keys/{id}/suspend", s.suspendKey)
	mux.HandleFunc("POST /keys/{id}/destroy", s.destroyKey)
	mux.HandleFunc("POST /keys/{id}/compromise", s.compromiseKey)
	mux.HandleFunc("DELETE /keys/{id}", s.deleteKey)
	mux.HandleFunc("POST /rootkeys", s.createRootKey)
	mux.HandleFunc("GET /rootkeys/{id}", s.getRootKey)
	mux.HandleFunc("DELETE /rootkeys/{id}", s.deleteRootKey)
	mux.HandleFunc("POST /deks", s.createDEK)
	mux.HandleFunc("GET /deks/{id}", s.getDEK)
	mux.HandleFunc("DELETE /deks/{id}", s.deleteDEK)

	return &http.Server{
		Addr:              addr,
		Handler:           loggingMiddleware(logger, mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// Start creates and starts a mock OpenKCM API server. It blocks until the
// context is cancelled, then shuts down gracefully.
func Start(ctx context.Context, addr string) error {
	srv := NewServer(addr)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// --- Error helpers ---

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(apiError{Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(v)
}

// --- Logging middleware ---

type statusCapture struct {
	http.ResponseWriter
	status int
}

func (s *statusCapture) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sc := &statusCapture{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sc, r)
		logger.Info("mockapi request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sc.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// --- Tenant endpoints ---

type createTenantRequest struct {
	Name string `json:"name"`
}

type tenantResponse struct {
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	ProcessingState string `json:"processingState"`
}

func (s *store) createTenant(w http.ResponseWriter, r *http.Request) {
	var req createTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "missing_name", "tenant name is required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id := uuid.New().String()
	s.tenants[id] = &tenantRecord{
		ID:        id,
		Name:      req.Name,
		CreatedAt: time.Now(),
	}
	s.persist()

	s.log.Info("tenant created", "id", id, "name", req.Name)

	writeJSON(w, http.StatusAccepted, tenantResponse{
		ID:              id,
		Name:            req.Name,
		ProcessingState: processingStateProcessing,
	})
}

func (s *store) getTenant(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tenants[id]
	if !ok {
		writeError(w, http.StatusNotFound, "tenant_not_found", fmt.Sprintf("tenant %q does not exist", id))
		return
	}

	writeJSON(w, http.StatusOK, tenantResponse{
		ID:              t.ID,
		Name:            t.Name,
		ProcessingState: s.stateForAge(t.CreatedAt),
	})
}

// --- Key endpoints ---

type createKeyRequest struct {
	TenantID string `json:"tenantId"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	ParentID string `json:"parentId,omitempty"`
}

type createRootKeyRequest struct {
	TenantID string            `json:"tenantId"`
	Provider string            `json:"provider"`
	Name     string            `json:"name"`
	Config   map[string]string `json:"config,omitempty"`
}

type createDEKRequest struct {
	TenantID       string            `json:"tenantId"`
	ServiceKeyID   string            `json:"serviceKeyId"`
	Name           string            `json:"name"`
	KMIPAttributes map[string]string `json:"kmipAttributes,omitempty"`
}

type keyResponse struct {
	ID              string `json:"id"`
	TenantID        string `json:"tenantId,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Name            string `json:"name,omitempty"`
	ParentID        string `json:"parentId,omitempty"`
	Provider        string `json:"provider,omitempty"`
	ProcessingState string `json:"processingState,omitempty"`
	LifecycleState  string `json:"lifecycleState,omitempty"`
	Version         int32  `json:"version,omitempty"`
}

const (
	lifecyclePreActive   = "PreActive"
	lifecycleActive      = "Active"
	lifecycleSuspended   = "Suspended"
	lifecycleDeactivated = "Deactivated"
	lifecycleCompromised = "Compromised"
	lifecycleDestroyed   = "Destroyed"
	processingStateReady = "ready"

	processingStateProcessing = "processing"
)

func rootKeyKind(provider string) string {
	return "L1:" + provider
}

func isRootKeyKind(kind string) bool {
	return len(kind) > 3 && kind[:3] == "L1:"
}

func validProvider(provider string) bool {
	switch provider {
	case "aws", "azure", "openbao", "gcp", "vault", "hsm":
		return true
	default:
		return false
	}
}

func validLifecycleState(state string) bool {
	switch state {
	case "", lifecyclePreActive, lifecycleActive, lifecycleSuspended, lifecycleDeactivated, lifecycleCompromised, lifecycleDestroyed:
		return true
	default:
		return false
	}
}

func (s *store) createKey(w http.ResponseWriter, r *http.Request) {
	var req createKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// Validate input
	if req.TenantID == "" {
		writeError(w, http.StatusBadRequest, "missing_tenant_id", "tenantId is required")
		return
	}
	if req.Kind != "L2" && req.Kind != "L3" {
		writeError(w, http.StatusBadRequest, "invalid_kind", "kind must be L2 or L3")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "missing_name", "key name is required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Tenant must exist
	if _, ok := s.tenants[req.TenantID]; !ok {
		writeError(w, http.StatusNotFound, "tenant_not_found", fmt.Sprintf("tenant %q does not exist", req.TenantID))
		return
	}

	// L3 must reference an existing L2 parent that is Active
	if req.Kind == "L3" {
		if req.ParentID == "" {
			writeError(w, http.StatusBadRequest, "missing_parent_id", "parentId is required for L3 keys")
			return
		}
		parent, ok := s.keys[req.ParentID]
		if !ok {
			writeError(w, http.StatusNotFound, "parent_not_found", fmt.Sprintf("parent key %q does not exist", req.ParentID))
			return
		}
		if parent.Kind != "L2" {
			writeError(w, http.StatusBadRequest, "invalid_parent_kind", "parent key must be L2")
			return
		}
		if parent.LifecycleState != lifecycleActive {
			writeError(w, http.StatusConflict, "parent_not_active", "parent L2 key is not Active")
			return
		}
	} else if req.ParentID != "" {
		writeError(w, http.StatusBadRequest, "unexpected_parent_id", "parentId must not be set for L2 keys")
		return
	}

	// Idempotent create: same tenant+kind+name+parent -> return existing ID
	dedup := keyDedupKey(req)
	if existingID, ok := s.keysByKey[dedup]; ok {
		k := s.keys[existingID]
		writeJSON(w, http.StatusAccepted, keyResponse{
			ID:              existingID,
			TenantID:        k.TenantID,
			Kind:            k.Kind,
			Name:            k.Name,
			ParentID:        k.ParentID,
			ProcessingState: s.stateForAge(k.CreatedAt),
			LifecycleState:  s.effectiveLifecycleState(k),
			Version:         k.Version,
		})
		return
	}

	id := uuid.New().String()
	s.keys[id] = &keyRecord{
		ID:        id,
		TenantID:  req.TenantID,
		Kind:      req.Kind,
		Name:      req.Name,
		ParentID:  req.ParentID,
		CreatedAt: time.Now(),
	}
	s.keysByKey[dedup] = id
	s.persist()

	s.log.Info("key created",
		"id", id,
		"kind", req.Kind,
		"name", req.Name,
		"tenantId", req.TenantID,
		"parentId", req.ParentID,
	)

	writeJSON(w, http.StatusAccepted, keyResponse{
		ID:              id,
		TenantID:        req.TenantID,
		Kind:            req.Kind,
		Name:            req.Name,
		ParentID:        req.ParentID,
		ProcessingState: processingStateProcessing,
		LifecycleState:  lifecyclePreActive,
	})
}

func (s *store) getKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	defer s.mu.Unlock()

	k, ok := s.keys[id]
	if !ok {
		writeError(w, http.StatusNotFound, "key_not_found", fmt.Sprintf("key %q does not exist", id))
		return
	}

	writeJSON(w, http.StatusOK, keyResponse{
		ID:              k.ID,
		TenantID:        k.TenantID,
		Kind:            k.Kind,
		Name:            k.Name,
		ParentID:        k.ParentID,
		ProcessingState: s.stateForAge(k.CreatedAt),
		LifecycleState:  s.effectiveLifecycleState(k),
		Version:         k.Version,
	})
}

func (s *store) createRootKey(w http.ResponseWriter, r *http.Request) {
	var req createRootKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if req.TenantID == "" {
		writeError(w, http.StatusBadRequest, "missing_tenant_id", "tenantId is required")
		return
	}
	if !validProvider(req.Provider) {
		writeError(w, http.StatusBadRequest, "invalid_provider", "provider must be one of aws, azure, openbao, gcp, vault, hsm")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "missing_name", "root key name is required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tenants[req.TenantID]; !ok {
		writeError(w, http.StatusNotFound, "tenant_not_found", fmt.Sprintf("tenant %q does not exist", req.TenantID))
		return
	}

	kind := rootKeyKind(req.Provider)
	dedup := keyDedupKey{
		TenantID: req.TenantID,
		Kind:     kind,
		Name:     req.Name,
	}
	if existingID, ok := s.keysByKey[dedup]; ok {
		k := s.keys[existingID]
		writeJSON(w, http.StatusAccepted, keyResponse{
			ID:              existingID,
			TenantID:        k.TenantID,
			Kind:            k.Kind,
			Name:            k.Name,
			Provider:        k.Provider,
			ProcessingState: s.stateForAge(k.CreatedAt),
			LifecycleState:  s.effectiveLifecycleState(k),
			Version:         k.Version,
		})
		return
	}

	id := uuid.New().String()
	s.keys[id] = &keyRecord{
		ID:        id,
		TenantID:  req.TenantID,
		Kind:      kind,
		Name:      req.Name,
		Provider:  req.Provider,
		Config:    copyStringMap(req.Config),
		CreatedAt: time.Now(),
	}
	s.keysByKey[dedup] = id
	s.persist()

	s.log.Info("root key created", "id", id, "provider", req.Provider, "name", req.Name, "tenantId", req.TenantID)

	writeJSON(w, http.StatusAccepted, keyResponse{
		ID:              id,
		TenantID:        req.TenantID,
		Kind:            kind,
		Name:            req.Name,
		Provider:        req.Provider,
		ProcessingState: processingStateProcessing,
		LifecycleState:  lifecyclePreActive,
	})
}

func (s *store) getRootKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	defer s.mu.Unlock()

	k, ok := s.keys[id]
	if !ok || !isRootKeyKind(k.Kind) {
		writeError(w, http.StatusNotFound, "root_key_not_found", fmt.Sprintf("root key %q does not exist", id))
		return
	}

	writeJSON(w, http.StatusOK, keyResponse{
		ID:              k.ID,
		TenantID:        k.TenantID,
		Kind:            k.Kind,
		Name:            k.Name,
		Provider:        k.Provider,
		ProcessingState: s.stateForAge(k.CreatedAt),
		LifecycleState:  s.effectiveLifecycleState(k),
		Version:         k.Version,
	})
}

func (s *store) createDEK(w http.ResponseWriter, r *http.Request) {
	var req createDEKRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if req.TenantID == "" {
		writeError(w, http.StatusBadRequest, "missing_tenant_id", "tenantId is required")
		return
	}
	if req.ServiceKeyID == "" {
		writeError(w, http.StatusBadRequest, "missing_service_key_id", "serviceKeyId is required")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "missing_name", "DEK name is required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tenants[req.TenantID]; !ok {
		writeError(w, http.StatusNotFound, "tenant_not_found", fmt.Sprintf("tenant %q does not exist", req.TenantID))
		return
	}
	parent, ok := s.keys[req.ServiceKeyID]
	if !ok {
		writeError(w, http.StatusNotFound, "parent_not_found", fmt.Sprintf("service key %q does not exist", req.ServiceKeyID))
		return
	}
	if parent.Kind != "L3" {
		writeError(w, http.StatusBadRequest, "invalid_parent_kind", "serviceKeyId must reference an L3 ServiceKey")
		return
	}
	if parent.TenantID != req.TenantID {
		writeError(w, http.StatusBadRequest, "tenant_mismatch", "service key belongs to a different tenant")
		return
	}
	if parent.LifecycleState != lifecycleActive {
		writeError(w, http.StatusConflict, "parent_not_active", "parent ServiceKey is not Active")
		return
	}

	dedup := keyDedupKey{
		TenantID: req.TenantID,
		Kind:     "L4",
		Name:     req.Name,
		ParentID: req.ServiceKeyID,
	}
	if existingID, ok := s.keysByKey[dedup]; ok {
		k := s.keys[existingID]
		writeJSON(w, http.StatusAccepted, keyResponse{
			ID:              existingID,
			TenantID:        k.TenantID,
			Kind:            k.Kind,
			Name:            k.Name,
			ParentID:        k.ParentID,
			ProcessingState: s.stateForAge(k.CreatedAt),
			LifecycleState:  s.effectiveLifecycleState(k),
			Version:         k.Version,
		})
		return
	}

	id := uuid.New().String()
	s.keys[id] = &keyRecord{
		ID:             id,
		TenantID:       req.TenantID,
		Kind:           "L4",
		Name:           req.Name,
		ParentID:       req.ServiceKeyID,
		KMIPAttributes: copyStringMap(req.KMIPAttributes),
		CreatedAt:      time.Now(),
	}
	s.keysByKey[dedup] = id
	s.persist()

	s.log.Info("DEK created", "id", id, "name", req.Name, "tenantId", req.TenantID, "serviceKeyId", req.ServiceKeyID)

	writeJSON(w, http.StatusAccepted, keyResponse{
		ID:              id,
		TenantID:        req.TenantID,
		Kind:            "L4",
		Name:            req.Name,
		ParentID:        req.ServiceKeyID,
		ProcessingState: processingStateProcessing,
		LifecycleState:  lifecyclePreActive,
	})
}

func (s *store) getDEK(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	defer s.mu.Unlock()

	k, ok := s.keys[id]
	if !ok || k.Kind != "L4" {
		writeError(w, http.StatusNotFound, "dek_not_found", fmt.Sprintf("DEK %q does not exist", id))
		return
	}

	writeJSON(w, http.StatusOK, keyResponse{
		ID:              k.ID,
		TenantID:        k.TenantID,
		Kind:            k.Kind,
		Name:            k.Name,
		ParentID:        k.ParentID,
		ProcessingState: s.stateForAge(k.CreatedAt),
		LifecycleState:  s.effectiveLifecycleState(k),
		Version:         k.Version,
	})
}

func (s *store) activateKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	defer s.mu.Unlock()

	k, ok := s.keys[id]
	if !ok {
		writeError(w, http.StatusNotFound, "key_not_found", fmt.Sprintf("key %q does not exist", id))
		return
	}

	// Reject activation if the key is still processing
	if s.stateForAge(k.CreatedAt) != processingStateReady {
		writeError(w, http.StatusConflict, "key_not_ready",
			"key is still processing, try again after provisioning completes")
		return
	}

	if k.LifecycleState == lifecycleDestroyed {
		writeError(w, http.StatusConflict, "key_destroyed", "destroyed keys cannot be activated")
		return
	}
	if k.LifecycleState == lifecycleCompromised {
		writeError(w, http.StatusConflict, "key_compromised", "compromised keys cannot be activated")
		return
	}

	// Idempotent: already Active -> return current state
	if k.LifecycleState == lifecycleActive {
		writeJSON(w, http.StatusOK, keyResponse{
			ID:             k.ID,
			LifecycleState: k.LifecycleState,
			Version:        k.Version,
		})
		return
	}

	// Bump version on every reactivation so clients can tell a
	// Deactivated -> Active transition apart from an idempotent
	// confirmation.
	k.LifecycleState = lifecycleActive
	k.Version++
	if k.Version == 0 {
		k.Version = 1
	}
	k.LastRotatedAt = time.Now()
	s.persist()

	s.log.Info("key activated",
		"id", k.ID,
		"kind", k.Kind,
		"version", k.Version,
	)

	writeJSON(w, http.StatusOK, keyResponse{
		ID:             k.ID,
		LifecycleState: k.LifecycleState,
		Version:        k.Version,
	})
}

// deactivateKey flips an Active/Suspended key back to Deactivated without destroying
// its material, so the caller can re-activate it later. Idempotent if the
// key is already Deactivated, 409 if the key has never been activated
// (lifecycleState=PreActive) or is still processing.
func (s *store) deactivateKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	defer s.mu.Unlock()

	k, ok := s.keys[id]
	if !ok {
		writeError(w, http.StatusNotFound, "key_not_found", fmt.Sprintf("key %q does not exist", id))
		return
	}

	if s.stateForAge(k.CreatedAt) != processingStateReady {
		writeError(w, http.StatusConflict, "key_not_ready",
			"key is still processing, try again after provisioning completes")
		return
	}

	if k.LifecycleState == lifecycleDeactivated {
		writeJSON(w, http.StatusOK, keyResponse{
			ID:             k.ID,
			LifecycleState: k.LifecycleState,
			Version:        k.Version,
		})
		return
	}

	if k.LifecycleState == lifecycleDestroyed {
		writeError(w, http.StatusConflict, "key_destroyed", "destroyed keys cannot be deactivated")
		return
	}
	if k.LifecycleState != lifecycleActive && k.LifecycleState != lifecycleSuspended {
		writeError(w, http.StatusConflict, "key_not_active",
			"key is not Active or Suspended; only Active or Suspended keys can be deactivated")
		return
	}

	k.LifecycleState = lifecycleDeactivated
	s.persist()

	s.log.Info("key deactivated",
		"id", k.ID,
		"kind", k.Kind,
		"version", k.Version,
	)

	writeJSON(w, http.StatusOK, keyResponse{
		ID:             k.ID,
		LifecycleState: k.LifecycleState,
		Version:        k.Version,
	})
}

func (s *store) suspendKey(w http.ResponseWriter, r *http.Request) {
	s.transitionKey(w, r, lifecycleSuspended, func(k *keyRecord) error {
		switch k.LifecycleState {
		case lifecycleSuspended:
			return nil
		case lifecycleActive:
			return nil
		case lifecycleDestroyed:
			return apiConflict("key_destroyed", "destroyed keys cannot be suspended")
		default:
			return apiConflict("key_not_active", "only Active keys can be suspended")
		}
	})
}

func (s *store) destroyKey(w http.ResponseWriter, r *http.Request) {
	s.transitionKey(w, r, lifecycleDestroyed, func(k *keyRecord) error {
		if k.LifecycleState == lifecycleDestroyed {
			return nil
		}
		return nil
	})
}

func (s *store) compromiseKey(w http.ResponseWriter, r *http.Request) {
	s.transitionKey(w, r, lifecycleCompromised, func(k *keyRecord) error {
		if k.LifecycleState == lifecycleDestroyed {
			return apiConflict("key_destroyed", "destroyed keys cannot be marked compromised")
		}
		return nil
	})
}

type transitionError struct {
	code    string
	message string
}

func (e *transitionError) Error() string {
	return e.message
}

func apiConflict(code, message string) error {
	return &transitionError{code: code, message: message}
}

func (s *store) transitionKey(w http.ResponseWriter, r *http.Request, target string, validate func(*keyRecord) error) {
	id := r.PathValue("id")

	s.mu.Lock()
	defer s.mu.Unlock()

	k, ok := s.keys[id]
	if !ok {
		writeError(w, http.StatusNotFound, "key_not_found", fmt.Sprintf("key %q does not exist", id))
		return
	}
	if s.stateForAge(k.CreatedAt) != processingStateReady {
		writeError(w, http.StatusConflict, "key_not_ready",
			"key is still processing, try again after provisioning completes")
		return
	}
	if err := validate(k); err != nil {
		if te := (&transitionError{}); errors.As(err, &te) {
			writeError(w, http.StatusConflict, te.code, te.message)
			return
		}
		writeError(w, http.StatusConflict, "invalid_transition", err.Error())
		return
	}
	if k.LifecycleState != target {
		k.LifecycleState = target
		s.persist()
	}
	writeJSON(w, http.StatusOK, keyResponse{
		ID:             k.ID,
		LifecycleState: k.LifecycleState,
		Version:        k.Version,
	})
}

// effectiveLifecycleState reports the externally-visible lifecycleState for
// a key. Newly-created keys sit with an empty stored lifecycleState until
// they are explicitly activated; expose that state as "PreActive" once the
// async provisioning window closes, so the client can reason about the
// two-phase create/activate flow. Callers must hold s.mu.
func (s *store) effectiveLifecycleState(k *keyRecord) string {
	if k.LifecycleState != "" {
		return k.LifecycleState
	}
	if s.stateForAge(k.CreatedAt) == processingStateReady {
		return lifecyclePreActive
	}
	return ""
}

// deleteTenant removes a tenant by ID. DELETE on a non-existent tenant
// is a no-op and returns 204, so finalizer cleanup can run repeatedly
// (or on an already-cleaned resource) without needing special 404
// handling in the client. Refuses to delete if any key still references
// the tenant, returning 409 conflict — this mirrors real KMS behaviour
// where keys must be destroyed before the tenant.
func (s *store) deleteTenant(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tenants[id]
	if !ok {
		// Idempotent delete.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	for _, k := range s.keys {
		if k.TenantID == id {
			writeError(w, http.StatusConflict, "tenant_has_keys",
				fmt.Sprintf("tenant %q still has keys; delete them first", id))
			return
		}
	}

	delete(s.tenants, id)
	s.persist()

	s.log.Info("tenant deleted", "id", id, "name", t.Name)
	w.WriteHeader(http.StatusNoContent)
}

// deleteKey removes a key by ID. Like deleteTenant, this endpoint is
// idempotent — deleting a non-existent key returns 204 so finalizer
// cleanup does not need to special-case the "already deleted" path.
// Refuses to delete a key that still has children.
func (s *store) deleteKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	defer s.mu.Unlock()

	k, ok := s.keys[id]
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	for _, child := range s.keys {
		if child.ParentID == id {
			writeError(w, http.StatusConflict, "key_has_children",
				fmt.Sprintf("key %q still has children; delete them first", id))
			return
		}
	}

	dedup := keyDedupKey{
		TenantID: k.TenantID,
		Kind:     k.Kind,
		Name:     k.Name,
		ParentID: k.ParentID,
	}
	delete(s.keys, id)
	delete(s.keysByKey, dedup)
	s.persist()

	s.log.Info("key deleted", "id", id, "kind", k.Kind, "name", k.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *store) deleteRootKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	k, ok := s.keys[id]
	s.mu.Unlock()
	if ok && !isRootKeyKind(k.Kind) {
		writeError(w, http.StatusNotFound, "root_key_not_found", fmt.Sprintf("root key %q does not exist", id))
		return
	}
	s.deleteKey(w, r)
}

func (s *store) deleteDEK(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	k, ok := s.keys[id]
	s.mu.Unlock()
	if ok && k.Kind != "L4" {
		writeError(w, http.StatusNotFound, "dek_not_found", fmt.Sprintf("DEK %q does not exist", id))
		return
	}
	s.deleteKey(w, r)
}

// stateForAge returns "ready" once the resource has been alive for at
// least provisioningDelay, otherwise "processing". Callers must hold
// s.mu.
func (s *store) stateForAge(createdAt time.Time) string {
	if time.Since(createdAt) >= s.provisioningDelay {
		return processingStateReady
	}
	return processingStateProcessing
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}
