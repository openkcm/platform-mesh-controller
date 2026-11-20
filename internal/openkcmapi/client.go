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
	"regexp"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
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
	TenantID string            `json:"tenantId"`
	Kind     string            `json:"kind"`
	Name     string            `json:"name"`
	ParentID string            `json:"parentId,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// CreateKeyResponse is the response body for creating a key.
type CreateKeyResponse struct {
	ID              string `json:"id"`
	ProcessingState string `json:"processingState"`
}

// GetKeyResponse is the response body for getting a key.
type GetKeyResponse struct {
	ID              string `json:"id"`
	ParentID        string `json:"parentId,omitempty"`
	ProcessingState string `json:"processingState"`
	LifecycleState  string `json:"lifecycleState,omitempty"`
	Version         int32  `json:"version,omitempty"`
}

// ActivateKeyResponse is the response body for activating a key.
type ActivateKeyResponse struct {
	ID              string `json:"id"`
	LifecycleState  string `json:"lifecycleState"`
	ProcessingState string `json:"processingState,omitempty"`
	Version         int32  `json:"version"`
}

// CreateRootKeyRequest is the request body for registering an L1 root key.
// Provider is one of: aws, azure, openbao, gcp, vault, hsm.
type CreateRootKeyRequest struct {
	TenantID string            `json:"tenantId"`
	Provider string            `json:"provider"`
	Name     string            `json:"name"`
	Config   map[string]string `json:"config,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// CreateRootKeyResponse is the response body for registering a root key.
type CreateRootKeyResponse struct {
	ID              string `json:"id"`
	ProcessingState string `json:"processingState"`
	LifecycleState  string `json:"lifecycleState,omitempty"`
}

// GetRootKeyResponse is the response body for getting a root key.
type GetRootKeyResponse struct {
	ID              string            `json:"id"`
	Provider        string            `json:"provider,omitempty"`
	Linked          bool              `json:"linked"`
	Config          map[string]string `json:"config,omitempty"`
	ProcessingState string            `json:"processingState"`
	LifecycleState  string            `json:"lifecycleState,omitempty"`
	Version         int32             `json:"version,omitempty"`
}

type CreateProviderRequest struct {
	Provider string            `json:"provider"`
	Config   map[string]string `json:"config,omitempty"`
	Auth     map[string]string `json:"auth,omitempty"`
}

const redactedCreateProviderRequest = "CreateProviderRequest{redacted}"

func (CreateProviderRequest) String() string   { return redactedCreateProviderRequest }
func (CreateProviderRequest) GoString() string { return redactedCreateProviderRequest }

// CreateDEKRequest is the request body for creating an L4 data encryption key.
type CreateDEKRequest struct {
	TenantID       string            `json:"tenantId"`
	ServiceKeyID   string            `json:"serviceKeyId"`
	Name           string            `json:"name"`
	KMIPAttributes map[string]string `json:"kmipAttributes,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
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

func kindForStatus(status int) Kind {
	switch status {
	case http.StatusNotFound:
		return KindNotFound
	case http.StatusConflict:
		return KindConflict
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return KindInvalid
	case http.StatusUnauthorized, http.StatusForbidden:
		return KindUnauthorized
	case http.StatusTooManyRequests:
		return KindTransient
	}
	if status >= http.StatusInternalServerError {
		return KindTransient
	}
	return KindUnknown
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

// --- Client methods ---

// CreateTenant creates a new tenant in the OpenKCM API.
func (c *Client) CreateTenant(ctx context.Context, req CreateTenantRequest) (*CreateTenantResponse, error) {
	var resp CreateTenantResponse
	if err := c.doRequest(ctx, "CreateTenant", http.MethodPost, "/tenants", req, &resp); err != nil {
		return nil, fmt.Errorf("create tenant: %w", err)
	}
	return &resp, nil
}

// GetTenant retrieves a tenant by ID from the OpenKCM API.
func (c *Client) GetTenant(ctx context.Context, id string) (*GetTenantResponse, error) {
	var resp GetTenantResponse
	if err := c.doRequest(ctx, "GetTenant", http.MethodGet, "/tenants/"+id, nil, &resp); err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}
	return &resp, nil
}

// CreateKey creates a new key in the OpenKCM API.
func (c *Client) CreateKey(ctx context.Context, req CreateKeyRequest) (*CreateKeyResponse, error) {
	var resp CreateKeyResponse
	if err := c.doRequest(ctx, "CreateKey", http.MethodPost, "/keys", req, &resp); err != nil {
		return nil, fmt.Errorf("create key: %w", err)
	}
	return &resp, nil
}

// GetKey retrieves a key by ID from the OpenKCM API.
func (c *Client) GetKey(ctx context.Context, id string) (*GetKeyResponse, error) {
	var resp GetKeyResponse
	if err := c.doRequest(ctx, "GetKey", http.MethodGet, "/keys/"+id, nil, &resp); err != nil {
		return nil, fmt.Errorf("get key: %w", err)
	}
	return &resp, nil
}

// ActivateKey activates a key by ID in the OpenKCM API.
func (c *Client) ActivateKey(ctx context.Context, id string) (*ActivateKeyResponse, error) {
	var resp ActivateKeyResponse
	if err := c.doRequest(ctx, "ActivateKey", http.MethodPost, "/keys/"+id+"/activate", nil, &resp); err != nil {
		return nil, fmt.Errorf("activate key: %w", err)
	}
	return &resp, nil
}

// DeactivateKey moves an Active key back to Deactivated without destroying
// its material, so it can be re-activated later.
func (c *Client) DeactivateKey(ctx context.Context, id string) (*ActivateKeyResponse, error) {
	var resp ActivateKeyResponse
	if err := c.doRequest(ctx, "DeactivateKey", http.MethodPost, "/keys/"+id+"/deactivate", nil, &resp); err != nil {
		return nil, fmt.Errorf("deactivate key: %w", err)
	}
	return &resp, nil
}

// DeleteTenant removes a tenant by ID from the OpenKCM API. Idempotent:
// deleting a non-existent tenant returns nil.
func (c *Client) DeleteTenant(ctx context.Context, id string) error {
	if err := c.doRequest(ctx, "DeleteTenant", http.MethodDelete, "/tenants/"+id, nil, nil); err != nil {
		return fmt.Errorf("delete tenant: %w", err)
	}
	return nil
}

// DeleteKey removes a key by ID from the OpenKCM API. Idempotent:
// deleting a non-existent key returns nil.
func (c *Client) DeleteKey(ctx context.Context, id string) error {
	if err := c.doRequest(ctx, "DeleteKey", http.MethodDelete, "/keys/"+id, nil, nil); err != nil {
		return fmt.Errorf("delete key: %w", err)
	}
	return nil
}

// SuspendKey transitions a key to lifecycleState=Suspended. Reversible
// via ActivateKey. Used for the broader v0.7.0 lifecycle.
func (c *Client) SuspendKey(ctx context.Context, id string) (*ActivateKeyResponse, error) {
	var resp ActivateKeyResponse
	if err := c.doRequest(ctx, "SuspendKey", http.MethodPost, "/keys/"+id+"/suspend", nil, &resp); err != nil {
		return nil, fmt.Errorf("suspend key: %w", err)
	}
	return &resp, nil
}

// DestroyKey transitions a key to lifecycleState=Destroyed. Terminal —
// material is shredded; not reversible.
func (c *Client) DestroyKey(ctx context.Context, id string) (*ActivateKeyResponse, error) {
	var resp ActivateKeyResponse
	if err := c.doRequest(ctx, "DestroyKey", http.MethodPost, "/keys/"+id+"/destroy", nil, &resp); err != nil {
		return nil, fmt.Errorf("destroy key: %w", err)
	}
	return &resp, nil
}

// CompromiseKey transitions a key to lifecycleState=Compromised. Admin-
// only operation; material is retained for forensics but unusable.
func (c *Client) CompromiseKey(ctx context.Context, id string) (*ActivateKeyResponse, error) {
	var resp ActivateKeyResponse
	if err := c.doRequest(ctx, "CompromiseKey", http.MethodPost, "/keys/"+id+"/compromise", nil, &resp); err != nil {
		return nil, fmt.Errorf("compromise key: %w", err)
	}
	return &resp, nil
}

// CreateRootKey registers a new L1 root key in the OpenKCM API.
func (c *Client) CreateRootKey(ctx context.Context, req CreateRootKeyRequest) (*CreateRootKeyResponse, error) {
	var resp CreateRootKeyResponse
	if err := c.doRequest(ctx, "CreateRootKey", http.MethodPost, "/rootkeys", req, &resp); err != nil {
		return nil, fmt.Errorf("create root key: %w", err)
	}
	return &resp, nil
}

// GetRootKey retrieves an L1 root key by ID from the OpenKCM API.
func (c *Client) GetRootKey(ctx context.Context, id string) (*GetRootKeyResponse, error) {
	var resp GetRootKeyResponse
	if err := c.doRequest(ctx, "GetRootKey", http.MethodGet, "/rootkeys/"+id, nil, &resp); err != nil {
		return nil, fmt.Errorf("get root key: %w", err)
	}
	return &resp, nil
}

// DeleteRootKey removes an L1 root key by ID from the OpenKCM API.
func (c *Client) DeleteRootKey(ctx context.Context, id string) error {
	if err := c.doRequest(ctx, "DeleteRootKey", http.MethodDelete, "/rootkeys/"+id, nil, nil); err != nil {
		return fmt.Errorf("delete root key: %w", err)
	}
	return nil
}

func (c *Client) CreateProvider(
	ctx context.Context, rootKeyID string, req CreateProviderRequest,
) (*GetRootKeyResponse, error) {
	var resp GetRootKeyResponse
	path := "/rootkeys/" + rootKeyID + "/provider"
	if err := c.doRequest(ctx, "CreateProvider", http.MethodPost, path, req, &resp); err != nil {
		return nil, fmt.Errorf("create provider: %w", err)
	}
	return &resp, nil
}

func (c *Client) RemoveProvider(ctx context.Context, rootKeyID string) error {
	path := "/rootkeys/" + rootKeyID + "/provider"
	if err := c.doRequest(ctx, "RemoveProvider", http.MethodDelete, path, nil, nil); err != nil {
		return fmt.Errorf("remove provider: %w", err)
	}
	return nil
}

// CreateDEK creates a new L4 data encryption key in the OpenKCM API.
func (c *Client) CreateDEK(ctx context.Context, req CreateDEKRequest) (*CreateDEKResponse, error) {
	var resp CreateDEKResponse
	if err := c.doRequest(ctx, "CreateDEK", http.MethodPost, "/deks", req, &resp); err != nil {
		return nil, fmt.Errorf("create dek: %w", err)
	}
	return &resp, nil
}

// GetDEK retrieves an L4 data encryption key by ID from the OpenKCM API.
func (c *Client) GetDEK(ctx context.Context, id string) (*GetDEKResponse, error) {
	var resp GetDEKResponse
	if err := c.doRequest(ctx, "GetDEK", http.MethodGet, "/deks/"+id, nil, &resp); err != nil {
		return nil, fmt.Errorf("get dek: %w", err)
	}
	return &resp, nil
}

// DeleteDEK removes an L4 data encryption key by ID from the OpenKCM API.
func (c *Client) DeleteDEK(ctx context.Context, id string) error {
	if err := c.doRequest(ctx, "DeleteDEK", http.MethodDelete, "/deks/"+id, nil, nil); err != nil {
		return fmt.Errorf("delete dek: %w", err)
	}
	return nil
}

func (c *Client) doRequest(ctx context.Context, call, method, path string, body any, result any) error {
	fields := []any{"method", method, "path", path}
	var request any
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
		request = loggableJSON(data)
		fields = append(fields, "request", request)
	}
	described := describeCall(call, path, request)

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	start := time.Now()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		logCall(ctx, described+" ✗ "+err.Error(), append(fields, "durationMs", time.Since(start).Milliseconds())...)
		return fmt.Errorf("execute request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(resp.Body)
	fields = append(fields, "status", resp.StatusCode, "durationMs", time.Since(start).Milliseconds())
	if err != nil {
		logCall(ctx, described+" ✗ "+err.Error(), fields...)
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		kind := kindForStatus(resp.StatusCode)
		// Try to parse the structured error body {code, message}.
		var apiErr struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if jerr := json.Unmarshal(respBody, &apiErr); jerr == nil && apiErr.Code != "" {
			logCall(ctx, fmt.Sprintf("%s ✗ %d %s: %s", described, resp.StatusCode, apiErr.Code, apiErr.Message),
				append(fields, "errorCode", apiErr.Code, "errorMessage", apiErr.Message)...)
			return NewAPIError(kind, resp.StatusCode, apiErr.Code, apiErr.Message)
		}
		logCall(ctx, fmt.Sprintf("%s ✗ %d %s", described, resp.StatusCode, respBody),
			append(fields, "errorMessage", string(respBody))...)
		return NewAPIError(kind, resp.StatusCode, http.StatusText(resp.StatusCode), string(respBody))
	}

	var response any
	if len(respBody) > 0 {
		response = loggableJSON(respBody)
		fields = append(fields, "response", response)
	}
	logCall(ctx, described+" → "+describeResult(path, response), fields...)

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}

const (
	callLogPrefix = "mock: "
	redactedValue = "<redacted>"
)

var (
	uuidPattern     = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	pathCollections = map[string]bool{"tenants": true, "keys": true, "rootkeys": true, "deks": true}
	leadingFields   = []string{"kind", "name", "parentId", "serviceKeyId", "provider"}
)

func logCall(ctx context.Context, line string, fields ...any) {
	log.FromContext(ctx).Info(callLogPrefix+shortenIDs(line), fields...)
}

func shortenIDs(text string) string {
	return uuidPattern.ReplaceAllStringFunc(text, func(id string) string { return id[:8] })
}

func pathIDs(path string) []string {
	var ids []string
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i+1 < len(segments); i++ {
		if pathCollections[segments[i]] {
			ids = append(ids, segments[i+1])
			i++
		}
	}
	return ids
}

func describeCall(call, path string, request any) string {
	args := pathIDs(path)
	if fields, ok := request.(map[string]any); ok {
		args = append(args, describeFields(fields)...)
	}
	return call + "(" + strings.Join(args, ", ") + ")"
}

func describeFields(fields map[string]any) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		if key != "tenantId" {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, rj := fieldRank(keys[i]), fieldRank(keys[j])
		if ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	described := make([]string, 0, len(keys))
	for _, key := range keys {
		switch value := fields[key].(type) {
		case string:
			if value != "" {
				described = append(described, key+"="+value)
			}
		case map[string]any:
			if allRedacted(value) {
				described = append(described, key+"="+redactedValue)
				continue
			}
			described = append(described, key+"{"+strings.Join(describeFields(value), " ")+"}")
		case nil:
		default:
			described = append(described, fmt.Sprintf("%s=%v", key, value))
		}
	}
	return described
}

func fieldRank(key string) int {
	for i, leading := range leadingFields {
		if key == leading {
			return i
		}
	}
	return len(leadingFields)
}

func allRedacted(fields map[string]any) bool {
	for _, value := range fields {
		if value != redactedValue {
			return false
		}
	}
	return len(fields) > 0
}

func describeResult(path string, response any) string {
	fields, ok := response.(map[string]any)
	if !ok || len(fields) == 0 {
		return "ok"
	}
	var parts []string
	if id, ok := fields["id"].(string); ok && len(pathIDs(path)) == 0 {
		parts = append(parts, "id="+id)
	}
	for _, key := range []string{"kind", "lifecycleState", "processingState"} {
		if value, ok := fields[key].(string); ok && value != "" {
			parts = append(parts, value)
		}
	}
	if linked, ok := fields["linked"].(bool); ok {
		if linked {
			parts = append(parts, fmt.Sprintf("linked to %v", fields["provider"]))
		} else {
			parts = append(parts, "not linked")
		}
	}
	if version, ok := fields["version"].(float64); ok && version > 0 {
		parts = append(parts, fmt.Sprintf("v%d", int(version)))
	}
	if len(parts) == 0 {
		return "ok"
	}
	return strings.Join(parts, ", ")
}

func loggableJSON(data []byte) any {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Sprintf("<%d bytes, not JSON>", len(data))
	}
	return redactSecrets(value)
}

func redactSecrets(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, inner := range typed {
			switch {
			case strings.EqualFold(key, "auth"):
				typed[key] = redactAll(inner)
			case isSecretKey(key):
				typed[key] = redactedValue
			default:
				typed[key] = redactSecrets(inner)
			}
		}
	case []any:
		for i := range typed {
			typed[i] = redactSecrets(typed[i])
		}
	}
	return value
}

func redactAll(value any) any {
	fields, ok := value.(map[string]any)
	if !ok {
		return redactedValue
	}
	for key := range fields {
		fields[key] = redactedValue
	}
	return fields
}

func isSecretKey(key string) bool {
	lower := strings.ToLower(key)
	return strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "token")
}
