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

	"github.com/openkcm/platform-mesh-controller/api/shared"
)

// HSMRootKeySpec is a stub schema for a PKCS#11 HSM-backed L1 root key.
// Full provider wiring is a follow-up; no reconciler in v0.7.0.
// +kubebuilder:validation:XValidation:rule="!self.configured || (self.tenantNameRef.size() > 0 && (has(self.slotUri) && self.slotUri.size() > 0))",message="Configured slots require non-empty provider parameters"
type HSMRootKeySpec struct {
	// Configured is false for an empty provider slot. Legacy resources default to true.
	// +optional
	// +kubebuilder:default=true
	Configured *bool `json:"configured,omitempty"`
	// ResetRequest identifies an operator reset requested by the role holder.
	// +optional
	// +kubebuilder:validation:MaxLength=128
	ResetRequest string `json:"resetRequest,omitempty"`

	// TenantNameRef is the account-name of the owning Tenant.
	// +required
	TenantNameRef string `json:"tenantNameRef"`

	// SlotURI is the PKCS#11 URI identifying the HSM slot + token + key.
	// +optional
	SlotURI string `json:"slotUri,omitempty"`

	// Lifecycle is the user-desired key lifecycle. Defaults to Active.
	// +optional
	// +kubebuilder:default=Active
	Lifecycle shared.DesiredLifecycle `json:"lifecycle,omitempty"`
}

// HSMRootKeyStatus defines the observed state of HSMRootKey.
type HSMRootKeyStatus struct {
	// RegistrationID identifies the durable backend registration and cancellation fence.
	// +optional
	RegistrationID string `json:"registrationId,omitempty"`

	// SlotState is the state of the provider slot.
	// +optional
	// +kubebuilder:validation:Enum=Unconfigured;Configuring;Configured;Resetting;ResetFailed
	SlotState string `json:"slotState,omitempty"`
	// LastResetRequest identifies the most recent completed reset.
	// +optional
	LastResetRequest string `json:"lastResetRequest,omitempty"`

	// ObservedGeneration is the last observed generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions represent the latest available observations of the HSMRootKey's state.
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

// HSMRootKey is a stub L1 root-key for a PKCS#11 HSM. No controller in v0.7.0.
type HSMRootKey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HSMRootKey
	// +required
	Spec HSMRootKeySpec `json:"spec"`

	// status defines the observed state of HSMRootKey
	// +optional
	Status HSMRootKeyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HSMRootKeyList contains a list of HSMRootKey.
type HSMRootKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HSMRootKey `json:"items"`
}
