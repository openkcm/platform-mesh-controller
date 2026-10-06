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

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operationsv1alpha1 "github.com/openkcm/platform-mesh-controller/api/operations/v1alpha1"
	"github.com/openkcm/platform-mesh-controller/api/shared"
)

const (
	openkcmSystemNamespace   = "openkcm-system"
	caCertKey                = "ca.crt"
	rootKeyRegisteredMessage = "Root key registered, awaiting activation"
)

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
	switch kind {
	case "AWSRootKey":
		return &rootKeyGVKEntry{
			GVK:      operationsv1alpha1.GroupVersion.WithKind("AWSRootKey"),
			newEmpty: func() client.Object { return &operationsv1alpha1.AWSRootKey{} },
		}
	case "AzureRootKey":
		return &rootKeyGVKEntry{
			GVK:      operationsv1alpha1.GroupVersion.WithKind("AzureRootKey"),
			newEmpty: func() client.Object { return &operationsv1alpha1.AzureRootKey{} },
		}
	case "OpenBaoRootKey":
		return &rootKeyGVKEntry{
			GVK:      operationsv1alpha1.GroupVersion.WithKind("OpenBaoRootKey"),
			newEmpty: func() client.Object { return &operationsv1alpha1.OpenBaoRootKey{} },
		}
	case "GCPRootKey":
		return &rootKeyGVKEntry{
			GVK:      operationsv1alpha1.GroupVersion.WithKind("GCPRootKey"),
			newEmpty: func() client.Object { return &operationsv1alpha1.GCPRootKey{} },
		}
	case "VaultRootKey":
		return &rootKeyGVKEntry{
			GVK:      operationsv1alpha1.GroupVersion.WithKind("VaultRootKey"),
			newEmpty: func() client.Object { return &operationsv1alpha1.VaultRootKey{} },
		}
	case "HSMRootKey":
		return &rootKeyGVKEntry{
			GVK:      operationsv1alpha1.GroupVersion.WithKind("HSMRootKey"),
			newEmpty: func() client.Object { return &operationsv1alpha1.HSMRootKey{} },
		}
	default:
		return nil
	}
}

func rootKeyCryptoState(obj client.Object) *shared.CryptoState {
	switch rootKey := obj.(type) {
	case *operationsv1alpha1.AWSRootKey:
		return rootKey.Status.CryptoState
	case *operationsv1alpha1.AzureRootKey:
		return rootKey.Status.CryptoState
	case *operationsv1alpha1.OpenBaoRootKey:
		return rootKey.Status.CryptoState
	case *operationsv1alpha1.GCPRootKey:
		return rootKey.Status.CryptoState
	case *operationsv1alpha1.VaultRootKey:
		return rootKey.Status.CryptoState
	case *operationsv1alpha1.HSMRootKey:
		return rootKey.Status.CryptoState
	default:
		return nil
	}
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
