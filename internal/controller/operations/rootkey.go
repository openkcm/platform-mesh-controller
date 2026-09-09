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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	"github.com/openkcm/openkcm-controller/api/shared"
)

// RootKey is the shared surface of every L1 kind. The six providers differ only
// in the spec block describing where the customer's key lives; the status is
// identical across all of them, so one reconciler can drive them all.
//
// It is declared here, in the consumer, and implemented by hand on the API
// types: controller-gen emits deepcopy only, and the alternative is a type
// switch in the reconciler for every field it touches.
type RootKey interface {
	client.Object

	// ProviderName is the value sent to the backend, e.g. "aws".
	ProviderName() string

	// ProviderConfig is the provider-specific part of the spec, flattened for
	// transport. Values are copied verbatim; nothing here is interpreted.
	ProviderConfig() map[string]string

	// TenantNameRef is advisory only. The reconciler derives tenant identity
	// from the workspace path, see path.go.
	TenantNameRef() string

	DesiredLifecycle() shared.DesiredLifecycle

	GetCryptoState() *shared.CryptoState
	SetCryptoState(*shared.CryptoState)

	GetReconciliationStatus() *shared.ReconciliationStatus
	SetReconciliationStatus(*shared.ReconciliationStatus)

	SetOperationID(string)

	GetObservedGeneration() int64
	SetObservedGeneration(int64)

	StatusConditions() *[]metav1.Condition
}

var (
	_ RootKey = (*operationsv1alpha1.AWSRootKey)(nil)
	_ RootKey = (*operationsv1alpha1.AzureRootKey)(nil)
	_ RootKey = (*operationsv1alpha1.OpenBaoRootKey)(nil)
	_ RootKey = (*operationsv1alpha1.GCPRootKey)(nil)
	_ RootKey = (*operationsv1alpha1.HSMRootKey)(nil)
	_ RootKey = (*operationsv1alpha1.VaultRootKey)(nil)
)

// rootKeyKinds is the one place that knows which L1 kinds exist. Both the
// reconcilers and the polymorphic reference lookup in the DomainKey controller
// read it, so a new provider cannot be watched but unresolvable, or the other
// way round.
var rootKeyKinds = []struct {
	Kind    string
	New     func() RootKey
	NewList func() client.ObjectList
}{
	{
		"AWSRootKey",
		func() RootKey { return &operationsv1alpha1.AWSRootKey{} },
		func() client.ObjectList { return &operationsv1alpha1.AWSRootKeyList{} },
	},
	{
		"AzureRootKey",
		func() RootKey { return &operationsv1alpha1.AzureRootKey{} },
		func() client.ObjectList { return &operationsv1alpha1.AzureRootKeyList{} },
	},
	{
		"OpenBaoRootKey",
		func() RootKey { return &operationsv1alpha1.OpenBaoRootKey{} },
		func() client.ObjectList { return &operationsv1alpha1.OpenBaoRootKeyList{} },
	},
	{
		"GCPRootKey",
		func() RootKey { return &operationsv1alpha1.GCPRootKey{} },
		func() client.ObjectList { return &operationsv1alpha1.GCPRootKeyList{} },
	},
	{
		"VaultRootKey",
		func() RootKey { return &operationsv1alpha1.VaultRootKey{} },
		func() client.ObjectList { return &operationsv1alpha1.VaultRootKeyList{} },
	},
	{
		"HSMRootKey",
		func() RootKey { return &operationsv1alpha1.HSMRootKey{} },
		func() client.ObjectList { return &operationsv1alpha1.HSMRootKeyList{} },
	},
}

// rootKeyItem carries the kind alongside the object: the typed client leaves
// TypeMeta empty on anything read back, so the kind has to come from the table
// that produced the list.
type rootKeyItem struct {
	Kind    string
	RootKey RootKey
}

// listRootKeys returns every L1 object of every kind in the namespace. Callers
// that only knew about three of the six silently ignored the rest.
func listRootKeys(ctx context.Context, cl client.Client, namespace string) ([]rootKeyItem, error) {
	var out []rootKeyItem
	for _, kind := range rootKeyKinds {
		list := kind.NewList()
		if err := cl.List(ctx, list, client.InNamespace(namespace)); err != nil {
			return nil, err
		}
		items, err := apimeta.ExtractList(list)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if rk, ok := item.(RootKey); ok {
				out = append(out, rootKeyItem{Kind: kind.Kind, RootKey: rk})
			}
		}
	}
	return out, nil
}

// rootKeyGVKEntry pairs a GroupVersionKind with a factory that returns a
// concrete typed client.Object for that kind. Used by reconcilers that
// resolve a polymorphic L1 reference (DomainKey.spec.primaryRootKeyRef).
type rootKeyGVKEntry struct {
	GVK      schema.GroupVersionKind
	newEmpty func() client.Object
}

// rootKeyGVK returns the entry matching the given Kind string, or nil
// for unsupported kinds. Used to drive polymorphic loads via the typed
// scheme (avoids spinning up an unstructured client just for ref resolution).
func rootKeyGVK(kind string) *rootKeyGVKEntry {
	for _, rk := range rootKeyKinds {
		if rk.Kind != kind {
			continue
		}
		newRootKey := rk.New
		return &rootKeyGVKEntry{
			GVK:      operationsv1alpha1.GroupVersion.WithKind(rk.Kind),
			newEmpty: func() client.Object { return newRootKey() },
		}
	}
	return nil
}

func rootKeyCryptoState(obj client.Object) *shared.CryptoState {
	rk, ok := obj.(RootKey)
	if !ok {
		return nil
	}
	return rk.GetCryptoState()
}

func rootKeyReferenceNamespace(ref *shared.TypedReference, fallbackNamespace string) string {
	if ref != nil && ref.Namespace != "" {
		return ref.Namespace
	}
	return fallbackNamespace
}

func allowedRootKeyReferenceNamespace(
	ref *shared.TypedReference,
	domainNamespace string,
	accountNamespace string,
) (string, bool) {
	refNamespace := rootKeyReferenceNamespace(ref, domainNamespace)
	if refNamespace == domainNamespace {
		return refNamespace, true
	}
	if refNamespace == defaultAccountNamespace(accountNamespace) {
		return refNamespace, true
	}
	return refNamespace, false
}

func ensureAutoDomainKeysForActiveAccountRoot(
	ctx context.Context,
	cl client.Client,
	rootNamespace string,
	accountNamespace string,
	state *shared.CryptoState,
) error {
	if state == nil || state.LifecycleState != shared.LifecycleActive {
		return nil
	}
	return ensureAutoDomainKeysForAccountRoot(ctx, cl, rootNamespace, accountNamespace)
}
