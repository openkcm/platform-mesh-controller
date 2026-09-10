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
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestErrorsAreClassifiedByStatus(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		notFound  bool
		conflict  bool
		retryable bool
	}{
		{"not found", http.StatusNotFound, `{"code":"tenant_not_found","message":"gone"}`, true, false, false},
		{"not found, plain body", http.StatusNotFound, "gone", true, false, false},
		{"conflict", http.StatusConflict, `{"code":"tenant_has_keys","message":"keys left"}`, false, true, false},
		{"bad request", http.StatusBadRequest, `{"code":"invalid_request","message":"bad"}`, false, false, false},
		{"unavailable", http.StatusServiceUnavailable, `{"code":"down","message":"later"}`, false, false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// given
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)

			// when
			_, err := NewClient(srv.URL).GetTenant(t.Context(), "tenant-id")

			// then
			if IsNotFound(err) != tc.notFound {
				t.Errorf("IsNotFound = %v, want %v (%v)", IsNotFound(err), tc.notFound, err)
			}
			if IsConflict(err) != tc.conflict {
				t.Errorf("IsConflict = %v, want %v (%v)", IsConflict(err), tc.conflict, err)
			}
			if IsRetryable(err) != tc.retryable {
				t.Errorf("IsRetryable = %v, want %v (%v)", IsRetryable(err), tc.retryable, err)
			}
		})
	}
}
