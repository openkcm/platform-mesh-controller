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

package openkcmapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Client is an HTTP client for the OpenKCM/Krypton API.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient creates a new OpenKCM API client.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL:    baseURL,
		HTTPClient: http.DefaultClient,
	}
}

// --- Request/Response types ---

// CreateTenantRequest is the request body for creating a tenant.
type CreateTenantRequest struct {
	Name string `json:"name"`
}

// CreateTenantResponse is the response body for creating a tenant.
type CreateTenantResponse struct {
	ID              string `json:"id"`
	ProcessingState string `json:"processingState"`
}

// GetTenantResponse is the response body for getting a tenant.
type GetTenantResponse struct {
	ID              string `json:"id"`
	ProcessingState string `json:"processingState"`
}

// CreateKeyRequest is the request body for creating a key.
type CreateKeyRequest struct {
	TenantID string `json:"tenantId"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	ParentID string `json:"parentId,omitempty"`
}

// CreateKeyResponse is the response body for creating a key.
type CreateKeyResponse struct {
	ID              string `json:"id"`
	ProcessingState string `json:"processingState"`
}

// GetKeyResponse is the response body for getting a key.
type GetKeyResponse struct {
	ID              string `json:"id"`
	ProcessingState string `json:"processingState"`
	LifecycleState  string `json:"lifecycleState,omitempty"`
	Version         int32  `json:"version,omitempty"`
}

// ActivateKeyResponse is the response body for activating a key.
type ActivateKeyResponse struct {
	ID             string `json:"id"`
	LifecycleState string `json:"lifecycleState"`
	Version        int32  `json:"version"`
}

// CreateRootKeyRequest is the request body for registering an L1 root key.
// Provider is one of: aws, azure, openbao, gcp, vault, hsm.
type CreateRootKeyRequest struct {
	TenantID string            `json:"tenantId"`
	Provider string            `json:"provider"`
	Name     string            `json:"name"`
	Config   map[string]string `json:"config,omitempty"`
}

// CreateRootKeyResponse is the response body for registering a root key.
type CreateRootKeyResponse struct {
	ID              string `json:"id"`
	ProcessingState string `json:"processingState"`
	LifecycleState  string `json:"lifecycleState,omitempty"`
}

// GetRootKeyResponse is the response body for getting a root key.
type GetRootKeyResponse struct {
	ID              string `json:"id"`
	ProcessingState string `json:"processingState"`
	LifecycleState  string `json:"lifecycleState,omitempty"`
	Version         int32  `json:"version,omitempty"`
}

// CreateDEKRequest is the request body for creating an L4 data encryption key.
type CreateDEKRequest struct {
	TenantID       string            `json:"tenantId"`
	ServiceKeyID   string            `json:"serviceKeyId"`
	Name           string            `json:"name"`
	KMIPAttributes map[string]string `json:"kmipAttributes,omitempty"`
}

// CreateDEKResponse is the response body for creating an L4 data encryption key.
type CreateDEKResponse struct {
	ID              string `json:"id"`
	ProcessingState string `json:"processingState"`
	LifecycleState  string `json:"lifecycleState,omitempty"`
}

// GetDEKResponse is the response body for getting an L4 data encryption key.
type GetDEKResponse struct {
	ID              string `json:"id"`
	ProcessingState string `json:"processingState"`
	LifecycleState  string `json:"lifecycleState,omitempty"`
	Version         int32  `json:"version,omitempty"`
}

// ProcessingStateReady is the value a backend reports once a resource has
// finished provisioning. It lives with the response types so every
// implementation and every caller compares against one literal.
const ProcessingStateReady = "ready"

// APIError is returned when the OpenKCM API responds with a structured
// error body (code + message). Callers can use errors.As to inspect the
// machine-readable code or the HTTP status.
type APIError struct {
	StatusCode int
	Code       string
	Message    string

	// kind is set by the transport that produced the error. Zero value is
	// KindUnknown, which callers treat as transient.
	kind Kind
}

// NewAPIError builds a classified backend error. Transports use it so callers
// can branch on Kind rather than on a status code.
func NewAPIError(kind Kind, statusCode int, code, message string) *APIError {
	return &APIError{StatusCode: statusCode, Code: code, Message: message, kind: kind}
}

// Error implements the error interface.
func (e *APIError) Error() string {
	return fmt.Sprintf("openkcm api error %d %s: %s", e.StatusCode, e.Code, e.Message)
}

// Kind classifies a backend failure so callers can decide what to do without
// knowing whether the transport was HTTP or gRPC.
type Kind int

const (
	// KindUnknown means the failure could not be classified. Treat as transient.
	KindUnknown Kind = iota
	// KindNotFound: the resource is gone. Retrying the same call will not help;
	// the caller may need to recreate it.
	KindNotFound
	// KindConflict: the resource already exists, or the requested transition is
	// not allowed from the current state.
	KindConflict
	// KindNotReady: a precondition is not met yet, typically a parent key that
	// has not been activated. Retrying later is the correct response.
	KindNotReady
	// KindInvalid: the request itself is wrong. Retrying never helps.
	KindInvalid
	// KindUnauthorized: credentials are missing or rejected.
	KindUnauthorized
	// KindTransient: the backend is unreachable or failed internally.
	KindTransient
)

// Kind reports how the failure should be handled.
func (e *APIError) Kind() Kind { return e.kind }

// Retryable reports whether repeating the call can succeed without the caller
// changing anything.
func (e *APIError) Retryable() bool {
	return e.kind == KindNotReady || e.kind == KindTransient || e.kind == KindUnknown
}

// classify returns the failure kind for err, or KindUnknown when err is not an
// APIError at all.
func classify(err error) Kind {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return KindUnknown
	}
	return apiErr.kind
}

// IsNotFound reports whether err came back as a missing resource.
func IsNotFound(err error) bool { return classify(err) == KindNotFound }

// IsConflict reports whether err came back as an already-existing resource or a
// rejected state transition.
func IsConflict(err error) bool { return classify(err) == KindConflict }

// IsRetryable reports whether repeating the call can succeed unchanged.
func IsRetryable(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		// An unclassified error is most likely a transport failure.
		return true
	}
	return apiErr.Retryable()
}

// SupportsTenantDeletion reports true: the mock serves DELETE /tenants/{id}.
func (c *Client) SupportsTenantDeletion() bool { return true }

// --- Client methods ---

// CreateTenant creates a new tenant in the OpenKCM API.
func (c *Client) CreateTenant(ctx context.Context, req CreateTenantRequest) (*CreateTenantResponse, error) {
	var resp CreateTenantResponse
	if err := c.doRequest(ctx, http.MethodPost, "/tenants", req, &resp); err != nil {
		return nil, fmt.Errorf("create tenant: %w", err)
	}
	return &resp, nil
}

// GetTenant retrieves a tenant by ID from the OpenKCM API.
func (c *Client) GetTenant(ctx context.Context, id string) (*GetTenantResponse, error) {
	var resp GetTenantResponse
	if err := c.doRequest(ctx, http.MethodGet, "/tenants/"+id, nil, &resp); err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}
	return &resp, nil
}

// CreateKey creates a new key in the OpenKCM API.
func (c *Client) CreateKey(ctx context.Context, req CreateKeyRequest) (*CreateKeyResponse, error) {
	var resp CreateKeyResponse
	if err := c.doRequest(ctx, http.MethodPost, "/keys", req, &resp); err != nil {
		return nil, fmt.Errorf("create key: %w", err)
	}
	return &resp, nil
}

// GetKey retrieves a key by ID from the OpenKCM API.
func (c *Client) GetKey(ctx context.Context, id string) (*GetKeyResponse, error) {
	var resp GetKeyResponse
	if err := c.doRequest(ctx, http.MethodGet, "/keys/"+id, nil, &resp); err != nil {
		return nil, fmt.Errorf("get key: %w", err)
	}
	return &resp, nil
}

// ActivateKey activates a key by ID in the OpenKCM API.
func (c *Client) ActivateKey(ctx context.Context, id string) (*ActivateKeyResponse, error) {
	var resp ActivateKeyResponse
	if err := c.doRequest(ctx, http.MethodPost, "/keys/"+id+"/activate", nil, &resp); err != nil {
		return nil, fmt.Errorf("activate key: %w", err)
	}
	return &resp, nil
}

// DeactivateKey moves an Active key back to Deactivated without destroying
// its material, so it can be re-activated later.
func (c *Client) DeactivateKey(ctx context.Context, id string) (*ActivateKeyResponse, error) {
	var resp ActivateKeyResponse
	if err := c.doRequest(ctx, http.MethodPost, "/keys/"+id+"/deactivate", nil, &resp); err != nil {
		return nil, fmt.Errorf("deactivate key: %w", err)
	}
	return &resp, nil
}

// DeleteTenant removes a tenant by ID from the OpenKCM API. Idempotent:
// deleting a non-existent tenant returns nil.
func (c *Client) DeleteTenant(ctx context.Context, id string) error {
	if err := c.doRequest(ctx, http.MethodDelete, "/tenants/"+id, nil, nil); err != nil {
		return fmt.Errorf("delete tenant: %w", err)
	}
	return nil
}

// DeleteKey removes a key by ID from the OpenKCM API. Idempotent:
// deleting a non-existent key returns nil.
func (c *Client) DeleteKey(ctx context.Context, id string) error {
	if err := c.doRequest(ctx, http.MethodDelete, "/keys/"+id, nil, nil); err != nil {
		return fmt.Errorf("delete key: %w", err)
	}
	return nil
}

// SuspendKey transitions a key to lifecycleState=Suspended. Reversible
// via ActivateKey. Used for the broader v0.7.0 lifecycle.
func (c *Client) SuspendKey(ctx context.Context, id string) (*ActivateKeyResponse, error) {
	var resp ActivateKeyResponse
	if err := c.doRequest(ctx, http.MethodPost, "/keys/"+id+"/suspend", nil, &resp); err != nil {
		return nil, fmt.Errorf("suspend key: %w", err)
	}
	return &resp, nil
}

// DestroyKey transitions a key to lifecycleState=Destroyed. Terminal —
// material is shredded; not reversible.
func (c *Client) DestroyKey(ctx context.Context, id string) (*ActivateKeyResponse, error) {
	var resp ActivateKeyResponse
	if err := c.doRequest(ctx, http.MethodPost, "/keys/"+id+"/destroy", nil, &resp); err != nil {
		return nil, fmt.Errorf("destroy key: %w", err)
	}
	return &resp, nil
}

// CompromiseKey transitions a key to lifecycleState=Compromised. Admin-
// only operation; material is retained for forensics but unusable.
func (c *Client) CompromiseKey(ctx context.Context, id string) (*ActivateKeyResponse, error) {
	var resp ActivateKeyResponse
	if err := c.doRequest(ctx, http.MethodPost, "/keys/"+id+"/compromise", nil, &resp); err != nil {
		return nil, fmt.Errorf("compromise key: %w", err)
	}
	return &resp, nil
}

// CreateRootKey registers a new L1 root key in the OpenKCM API.
func (c *Client) CreateRootKey(ctx context.Context, req CreateRootKeyRequest) (*CreateRootKeyResponse, error) {
	var resp CreateRootKeyResponse
	if err := c.doRequest(ctx, http.MethodPost, "/rootkeys", req, &resp); err != nil {
		return nil, fmt.Errorf("create root key: %w", err)
	}
	return &resp, nil
}

// GetRootKey retrieves an L1 root key by ID from the OpenKCM API.
func (c *Client) GetRootKey(ctx context.Context, id string) (*GetRootKeyResponse, error) {
	var resp GetRootKeyResponse
	if err := c.doRequest(ctx, http.MethodGet, "/rootkeys/"+id, nil, &resp); err != nil {
		return nil, fmt.Errorf("get root key: %w", err)
	}
	return &resp, nil
}

// DeleteRootKey removes an L1 root key by ID from the OpenKCM API.
func (c *Client) DeleteRootKey(ctx context.Context, id string) error {
	if err := c.doRequest(ctx, http.MethodDelete, "/rootkeys/"+id, nil, nil); err != nil {
		return fmt.Errorf("delete root key: %w", err)
	}
	return nil
}

// CreateDEK creates a new L4 data encryption key in the OpenKCM API.
func (c *Client) CreateDEK(ctx context.Context, req CreateDEKRequest) (*CreateDEKResponse, error) {
	var resp CreateDEKResponse
	if err := c.doRequest(ctx, http.MethodPost, "/deks", req, &resp); err != nil {
		return nil, fmt.Errorf("create dek: %w", err)
	}
	return &resp, nil
}

// GetDEK retrieves an L4 data encryption key by ID from the OpenKCM API.
func (c *Client) GetDEK(ctx context.Context, id string) (*GetDEKResponse, error) {
	var resp GetDEKResponse
	if err := c.doRequest(ctx, http.MethodGet, "/deks/"+id, nil, &resp); err != nil {
		return nil, fmt.Errorf("get dek: %w", err)
	}
	return &resp, nil
}

// DeleteDEK removes an L4 data encryption key by ID from the OpenKCM API.
func (c *Client) DeleteDEK(ctx context.Context, id string) error {
	if err := c.doRequest(ctx, http.MethodDelete, "/deks/"+id, nil, nil); err != nil {
		return fmt.Errorf("delete dek: %w", err)
	}
	return nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body any, result any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		// Try to parse the structured error body {code, message}.
		var apiErr struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if jerr := json.Unmarshal(respBody, &apiErr); jerr == nil && apiErr.Code != "" {
			return &APIError{
				StatusCode: resp.StatusCode,
				Code:       apiErr.Code,
				Message:    apiErr.Message,
			}
		}
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}
