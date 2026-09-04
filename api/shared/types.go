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

// Package shared holds cross-group types reused by every OpenKCM CRD
// (status surface, polymorphic refs, lifecycle enum).
// +kubebuilder:object:generate=true
package shared

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// TypedReference is a polymorphic reference to a resource identified by
// apiGroup + kind + namespace + name. Used where the referent kind varies
// (e.g. DomainKey.spec.primaryRootKeyRef accepts any L1 root-key kind).
type TypedReference struct {
	// APIGroup is the API group of the referenced resource.
	// +required
	APIGroup string `json:"apiGroup"`
	// Kind is the kind of the referenced resource.
	// +required
	Kind string `json:"kind"`
	// Namespace is the namespace of the referenced resource. Empty keeps the
	// historical same-namespace behavior.
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// Name is the name of the referenced resource.
	// +required
	Name string `json:"name"`
}

// LifecycleState is the Krypton key lifecycle state, as observed via the
// upstream KMS. Used in status.cryptoState.lifecycleState.
// +kubebuilder:validation:Enum=PreActive;Active;Suspended;Deactivated;Compromised;Destroyed
type LifecycleState string

const (
	LifecyclePreActive   LifecycleState = "PreActive"
	LifecycleActive      LifecycleState = "Active"
	LifecycleSuspended   LifecycleState = "Suspended"
	LifecycleDeactivated LifecycleState = "Deactivated"
	LifecycleCompromised LifecycleState = "Compromised"
	LifecycleDestroyed   LifecycleState = "Destroyed"
)

// DesiredLifecycle is the user-desired key lifecycle, settable on spec.
// Limited to the user-controllable subset of the full Krypton lifecycle —
// keys auto-progress PreActive→Active after creation, and the user can
// request Deactivated to disable them. Suspended / Compromised / Destroyed
// are reserved for KMS-driven transitions and are not user-settable.
// +kubebuilder:validation:Enum=Active;Deactivated
type DesiredLifecycle string

const (
	DesiredLifecycleActive      DesiredLifecycle = "Active"
	DesiredLifecycleDeactivated DesiredLifecycle = "Deactivated"
)

// CryptoState represents the Krypton cryptographic business state of a key.
// Tenant does not carry a CryptoState — only crypto-bearing kinds (L1/L2/L3/L4) do.
type CryptoState struct {
	// ID is the Krypton-assigned key UUID.
	// +optional
	ID string `json:"id,omitempty"`
	// Version is the key version number.
	// +optional
	Version int32 `json:"version,omitempty"`
	// LifecycleState is the key lifecycle state.
	// +optional
	LifecycleState LifecycleState `json:"lifecycleState,omitempty"`
	// LastRotatedAt is the last key rotation timestamp.
	// +optional
	LastRotatedAt *metav1.Time `json:"lastRotatedAt,omitempty"`
}

// SecretKeyReference points at a specific data key inside a Secret in any
// namespace. Distinct from corev1.SecretKeySelector (which is local-namespace)
// because identity material may live in a system namespace separate from
// the workspace where the OpenKCM CR is created.
type SecretKeyReference struct {
	// Name of the Secret.
	// +required
	Name string `json:"name"`
	// Namespace the Secret lives in.
	// +required
	Namespace string `json:"namespace"`
	// Key is the data-map key within the Secret.
	// +required
	Key string `json:"key"`
}

// IdentityInfo carries the upstream identity that OpenKCM established with
// the backing KMS or key store. Populated by L1 reconcilers once federation
// succeeds; relayed up the chain by L2/L3/L4 for diagnostics.
type IdentityInfo struct {
	// Subject is the certificate subject DN (or equivalent identity claim)
	// presented to the upstream provider.
	// +optional
	Subject string `json:"subject,omitempty"`
	// CertificateSecretRef points at the CA bundle backing the identity
	// chain (e.g. ca.crt used to validate the upstream KMS endpoint).
	// +optional
	CertificateSecretRef *SecretKeyReference `json:"certificateSecretRef,omitempty"`
}

// ReconciliationStatus is the common reconciliation outcome surface shared
// by every operations.openkcm.io kind. It complements (does not replace)
// the standard Kubernetes Conditions: Conditions report kubernetes-shaped
// observation predicates (Ready / ProviderSynced), while ReconciliationStatus
// carries the Krypton-side outcome (internal key id, error chain, identity).
type ReconciliationStatus struct {
	// Success is true when the last reconciliation cycle completed without error.
	// +optional
	Success bool `json:"success,omitempty"`
	// Message is a human-readable outcome description.
	// +optional
	Message string `json:"message,omitempty"`
	// InternalKeyID is the OpenKCM-internal identifier for the key bound by
	// this reconciliation. Distinct from CryptoState.ID, which is the
	// Krypton-side business UUID.
	// +optional
	InternalKeyID string `json:"internalKeyId,omitempty"`
	// LastTransitionTime is the timestamp of the most recent successful or
	// failed reconciliation transition.
	// +optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`
	// Errors collects diagnostic strings for the current cycle. Empty on success.
	// +optional
	Errors []string `json:"errors,omitempty"`
	// IdentityInfo carries the upstream-provider identity for L1 kinds.
	// Only populated when meaningful (i.e. L1 reconcilers).
	// +optional
	IdentityInfo *IdentityInfo `json:"identityInfo,omitempty"`
}
