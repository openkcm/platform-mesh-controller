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

import "testing"

// accountNameFromPath is the security boundary for tenant identity: the
// account is taken from where the workspace lives, never from a field a user
// can set. Every shape that is not exactly root:orgs:<org>:<account> must be
// rejected, so each rejecting branch gets its own case.
func TestAccountNameFromPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
		ok   bool
	}{
		{
			name: "account workspace",
			path: "root:orgs:showroom:ig-clean-account",
			want: "ig-clean-account",
			ok:   true,
		},
		{
			name: "org workspace is not an account",
			path: "root:orgs:showroom",
		},
		{
			name: "provider workspace is not an account",
			path: "root:providers:openkcm-provider",
		},
		{
			name: "nested account child is not the account",
			path: "root:orgs:showroom:ig-clean-account:system",
		},

		// The four segments are right, but the first two are not root:orgs.
		{
			name: "four segments not rooted at root",
			path: "other:orgs:showroom:account",
		},
		{
			name: "four segments not under orgs",
			path: "root:providers:showroom:account",
		},

		// The shape is right and the prefix is right, but a segment is empty.
		{
			name: "empty org segment",
			path: "root:orgs::account",
		},
		{
			name: "empty account segment",
			path: "root:orgs:showroom:",
		},

		{
			name: "empty path",
			path: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := accountNameFromPath(tt.path)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("accountName = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultAccountNamespace(t *testing.T) {
	tests := []struct {
		name  string
		given string
		want  string
	}{
		{name: "empty falls back", given: "", want: defaultTenantNamespace},
		{name: "explicit value is kept", given: "team-a", want: "team-a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultAccountNamespace(tt.given); got != tt.want {
				t.Errorf("defaultAccountNamespace(%q) = %q, want %q", tt.given, got, tt.want)
			}
		})
	}
}
