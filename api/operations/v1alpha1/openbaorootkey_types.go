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

// OpenBaoCertAuthConfig captures the OpenBao cert-auth backend
// configuration used to authenticate via mTLS X.509.
type OpenBaoCertAuthConfig struct {
	// AuthMountPath is the OpenBao auth-method mount path (e.g. "cert").
	// +required
	AuthMountPath string `json:"authMountPath"`
	// RoleName is the OpenBao cert-auth role name.
	// +required
	RoleName string `json:"roleName"`
}

// OpenBaoRootKeySpec defines the desired state of OpenBaoRootKey.
// +kubebuilder:validation:XValidation:rule="!self.configured || (self.tenantNameRef.size() > 0 && (has(self.enginePath) && self.enginePath.size() > 0) && (has(self.keyName) && self.keyName.size() > 0) && (has(self.serverAddress) && self.serverAddress.size() > 0) && (has(self.certAuth) && self.certAuth.authMountPath.size() > 0) && (has(self.certAuth) && self.certAuth.roleName.size() > 0))",message="Configured slots require non-empty provider parameters"
type OpenBaoRootKeySpec struct {
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

	// EnginePath is the OpenBao Transit engine mount path (e.g. "transit").
	// +optional
	EnginePath string `json:"enginePath,omitzero"`
	// KeyName is the OpenBao Transit key name.
	// +optional
	KeyName string `json:"keyName,omitzero"`
	// ServerAddress is the OpenBao API endpoint.
	// +optional
	ServerAddress string `json:"serverAddress,omitzero"`

	// CertAuth is the OpenBao cert-auth backend configuration.
	// +optional
	CertAuth OpenBaoCertAuthConfig `json:"certAuth,omitzero"`

	// Lifecycle is the user-desired key lifecycle. Defaults to Active.
	// +optional
	// +kubebuilder:default=Active
	Lifecycle shared.DesiredLifecycle `json:"lifecycle,omitempty"`
}

// OpenBaoRootKeyStatus defines the observed state of OpenBaoRootKey.
type OpenBaoRootKeyStatus struct {
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
	// Conditions represent the latest available observations of the OpenBaoRootKey's state.
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
// +kubebuilder:printcolumn:name="Server",type=string,JSONPath=`.spec.serverAddress`
// +kubebuilder:printcolumn:name="Lifecycle",type=string,JSONPath=`.status.cryptoState.lifecycleState`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OpenBaoRootKey is an L1 root-key binding to an OpenBao (Linux Foundation
// Vault fork) Transit Engine key, authenticated via mTLS X.509 cert auth.
type OpenBaoRootKey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of OpenBaoRootKey
	// +required
	Spec OpenBaoRootKeySpec `json:"spec"`

	// status defines the observed state of OpenBaoRootKey
	// +optional
	Status OpenBaoRootKeyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// OpenBaoRootKeyList contains a list of OpenBaoRootKey.
type OpenBaoRootKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OpenBaoRootKey `json:"items"`
}
