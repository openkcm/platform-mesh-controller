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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openkcm/openkcm-controller/api/shared"
)

// DomainKeyScope says which ServiceKeys a DomainKey serves.
// +kubebuilder:validation:Enum=Instance;Namespace;System
type DomainKeyScope string

const (
	DomainKeyScopeInstance  DomainKeyScope = "Instance"
	DomainKeyScopeNamespace DomainKeyScope = "Namespace"
	DomainKeyScopeSystem    DomainKeyScope = "System"
)

// DomainKeySpec defines the desired state of DomainKey (L2).
//
// DomainKey establishes the hierarchical link from a Tenant down to the
// upstream KMS via a polymorphic primaryRootKeyRef pointing at an L1
// root-key kind (AWS/Azure/OpenBao/GCP/Vault/HSM).
type DomainKeySpec struct {
	// Type categorises the DomainKey scope.
	// +kubebuilder:validation:Enum=Team;BusinessUnit
	// +required
	Type string `json:"type"`

	// Scope limits the ServiceKeys under this DomainKey: an Instance DomainKey
	// serves exactly one ServiceKey, a Namespace DomainKey any number.
	// +kubebuilder:default=Namespace
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="scope is immutable"
	// +optional
	Scope DomainKeyScope `json:"scope,omitempty"`

	// TenantNameRef is the account-name of the owning Tenant. Advisory;
	// the reconciler derives the canonical tenant identity from the
	// workspace path (showroom#203).
	// +required
	TenantNameRef string `json:"tenantNameRef"`

	// PrimaryRootKeyRef points at the L1 root key that backs this
	// DomainKey. Polymorphic across the L1 kinds. Optional so the
	// AccountBootstrapReconciler can auto-create the singleton DomainKey
	// at APIBinding creation before any L1 has been registered. The
	// DomainKey reconciler surfaces Ready=False with reason
	// AwaitingPrimaryRootKey while the field is unset; once the user
	// links an Active L1 via Edit (or kubectl), reconciliation resumes.
	// +optional
	PrimaryRootKeyRef *shared.TypedReference `json:"primaryRootKeyRef,omitempty"`

	// FallbackRootKeyRefs is an optional list of fallback L1 references
	// for multi-root scenarios (multi-cloud / multi-region / multi-AZ).
	// +optional
	FallbackRootKeyRefs []shared.TypedReference `json:"fallbackRootKeyRefs,omitempty"`

	// Lifecycle is the user-desired key lifecycle. Defaults to Active.
	// Setting to Deactivated cascades to all ServiceKeys under this domain
	// and their Data Encryption Keys.
	// +optional
	// +kubebuilder:default=Active
	Lifecycle shared.DesiredLifecycle `json:"lifecycle,omitempty"`
}

// DomainKeyStatus defines the observed state of DomainKey.
type DomainKeyStatus struct {
	// ObservedGeneration is the last observed generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions represent the latest available observations of the DomainKey's state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// OperationID is the Krypton-side correlation UUID for the most recent operation.
	// +optional
	OperationID string `json:"operationId,omitempty"`
	// ReconciliationStatus carries the most recent reconciliation outcome.
	// +optional
	ReconciliationStatus *shared.ReconciliationStatus `json:"reconciliationStatus,omitempty"`
	// CryptoState represents the Krypton cryptographic business state.
	// +optional
	CryptoState *shared.CryptoState `json:"cryptoState,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantNameRef`
// +kubebuilder:printcolumn:name="RootKey",type=string,JSONPath=`.spec.primaryRootKeyRef.name`,priority=0
// +kubebuilder:printcolumn:name="Lifecycle",type=string,JSONPath=`.status.cryptoState.lifecycleState`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DomainKey enables OpenKCM encryption for a specific Team or BusinessUnit
// under a Tenant. Links upwards to an L1 root key via primaryRootKeyRef.
type DomainKey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DomainKey
	// +required
	Spec DomainKeySpec `json:"spec"`

	// status defines the observed state of DomainKey
	// +optional
	Status DomainKeyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DomainKeyList contains a list of DomainKey.
type DomainKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DomainKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DomainKey{}, &DomainKeyList{})
}
