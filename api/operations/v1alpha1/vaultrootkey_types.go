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

// VaultRootKeySpec is a stub schema for HashiCorp Vault Transit. Full
// provider wiring is a follow-up; no reconciler in v0.7.0.
type VaultRootKeySpec struct {
	// TenantNameRef is the account-name of the owning Tenant.
	// +required
	TenantNameRef string `json:"tenantNameRef"`

	// ServerAddress is the Vault API endpoint.
	// +optional
	ServerAddress string `json:"serverAddress,omitempty"`
	// KeyName is the Vault Transit key name.
	// +optional
	KeyName string `json:"keyName,omitempty"`

	// Lifecycle is the user-desired key lifecycle. Defaults to Active.
	// +optional
	// +kubebuilder:default=Active
	Lifecycle shared.DesiredLifecycle `json:"lifecycle,omitempty"`
}

// VaultRootKeyStatus defines the observed state of VaultRootKey.
type VaultRootKeyStatus struct {
	// ObservedGeneration is the last observed generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions represent the latest available observations of the VaultRootKey's state.
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
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantNameRef`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// VaultRootKey is a stub L1 root-key for HashiCorp Vault Transit.
// No controller wiring in v0.7.0.
type VaultRootKey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of VaultRootKey
	// +required
	Spec VaultRootKeySpec `json:"spec"`

	// status defines the observed state of VaultRootKey
	// +optional
	Status VaultRootKeyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// VaultRootKeyList contains a list of VaultRootKey.
type VaultRootKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []VaultRootKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VaultRootKey{}, &VaultRootKeyList{})
}
