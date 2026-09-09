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

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chartRBACFiles are hand-maintained: config/rbac/role.yaml is generated from
// the kubebuilder markers, the charts are not. Three of the six L1 kinds were
// missing from both of these, so a chart-installed manager could not watch
// them at all while the generated role said it could.
var chartRBACFiles = []string{
	filepath.Join("..", "..", "..", "charts", "operator", "templates", "rbac.yaml"),
	filepath.Join("..", "..", "..", "charts", "pm-integration", "templates", "syncagent-rbac.yaml"),
}

// TestChartRBACCoversEveryRootKeyKind fails when a provider is added to
// rootKeyKinds without the RBAC that lets the deployed manager reconcile it.
func TestChartRBACCoversEveryRootKeyKind(t *testing.T) {
	for _, path := range chartRBACFiles {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rules := string(content)

		for _, kind := range rootKeyKinds {
			plural := strings.ToLower(kind.Kind) + "s"
			for _, subresource := range []string{"", "/status", "/finalizers"} {
				want := "- " + plural + subresource + "\n"
				if !strings.Contains(rules, want) {
					t.Errorf("%s does not grant %s%s", path, plural, subresource)
				}
			}
		}
	}
}
