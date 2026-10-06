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
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"

	"github.com/openkcm/platform-mesh-controller/internal/openkcmapi"
)

const (
	// pathAnnotation is the canonical annotation KCP writes on every
	// LogicalCluster to record its workspace path (e.g. "root:orgs:acme:dev").
	pathAnnotation = "kcp.io/path"

	processingStateReady = openkcmapi.ProcessingStateReady
)

type accountIdentity struct {
	Name      string
	Namespace string
}

func resolveAccount(ctx context.Context, cl client.Client, accountNamespace string) (accountIdentity, error) {
	name, err := resolveAccountName(ctx, cl)
	if err != nil {
		return accountIdentity{}, err
	}
	return accountIdentity{
		Name:      name,
		Namespace: defaultAccountNamespace(accountNamespace),
	}, nil
}

// workspacePath looks up the LogicalCluster in the given workspace and
// returns its KCP path annotation (for example root:orgs:acme:dev).
func workspacePath(ctx context.Context, cl client.Client) (string, error) {
	lc := &kcpcorev1alpha1.LogicalCluster{}
	if err := cl.Get(ctx, types.NamespacedName{Name: "cluster"}, lc); err != nil {
		return "", fmt.Errorf("get LogicalCluster: %w", err)
	}
	path := lc.Annotations[pathAnnotation]
	if path == "" {
		return "", fmt.Errorf("LogicalCluster has no %s annotation", pathAnnotation)
	}
	return path, nil
}

// resolveAccountName looks up the LogicalCluster in the given workspace and
// extracts the account name (4th path segment) from its kcp.io/path annotation.
// Returns an error if the path is not exactly root:orgs:<org>:<account>.
//
// This is the security-critical lookup for tenant resolution: tenant
// identity must be derived from where the workspace lives, not from a
// user-controlled spec field. See showroom#203.
func resolveAccountName(ctx context.Context, cl client.Client) (string, error) {
	path, err := workspacePath(ctx, cl)
	if err != nil {
		return "", err
	}
	accountName, ok := accountNameFromPath(path)
	if !ok {
		return "", fmt.Errorf("workspace path %q is not an account workspace root:orgs:<org>:<account>", path)
	}
	return accountName, nil
}

// accountNameFromPath returns the account-level leaf of a 4-segment KCP
// workspace path (root:orgs:<org>:<account>). Other shapes (org-only,
// org-system, provider workspaces) return (_, false) so callers can skip them.
func accountNameFromPath(path string) (string, bool) {
	parts := strings.Split(path, ":")
	if len(parts) != 4 {
		return "", false
	}
	if parts[0] != "root" || parts[1] != "orgs" {
		return "", false
	}
	if parts[2] == "" || parts[3] == "" {
		return "", false
	}
	return parts[3], true
}
