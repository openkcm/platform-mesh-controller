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

// AzureFederatedIdentityConfig captures the Azure AD workload-identity
// federation configuration used to authenticate to Azure Key Vault.
type AzureFederatedIdentityConfig struct {
	// TenantID is the Azure AD tenant ID.
	// +required
	TenantID string `json:"tenantId"`
	// ClientID is the Azure AD application (client) ID.
	// +required
	ClientID string `json:"clientId"`
}

// AzureRootKeySpec defines the desired state of AzureRootKey.
type AzureRootKeySpec struct {
	// TenantNameRef is the account-name of the owning Tenant.
	// +required
	TenantNameRef string `json:"tenantNameRef"`

	// VaultURL is the Azure Key Vault endpoint.
	// +required
	VaultURL string `json:"vaultUrl"`
	// KeyName is the Azure Key Vault key name.
	// +required
	KeyName string `json:"keyName"`
	// KeyVersion optionally pins a specific key version; empty pulls latest.
	// +optional
	KeyVersion string `json:"keyVersion,omitempty"`

	// FederatedIdentity is the Azure AD workload-identity federation config.
	// +required
	FederatedIdentity AzureFederatedIdentityConfig `json:"federatedIdentity"`

	// Lifecycle is the user-desired key lifecycle. Defaults to Active.
	// +optional
	// +kubebuilder:default=Active
	Lifecycle shared.DesiredLifecycle `json:"lifecycle,omitempty"`
}

// AzureRootKeyStatus defines the observed state of AzureRootKey.
type AzureRootKeyStatus struct {
	// ObservedGeneration is the last observed generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions represent the latest available observations of the AzureRootKey's state.
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
// +kubebuilder:printcolumn:name="Vault",type=string,JSONPath=`.spec.vaultUrl`
// +kubebuilder:printcolumn:name="Lifecycle",type=string,JSONPath=`.status.cryptoState.lifecycleState`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AzureRootKey is an L1 root-key binding to an Azure Key Vault key,
// federated via Azure AD workload identity.
type AzureRootKey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of AzureRootKey
	// +required
	Spec AzureRootKeySpec `json:"spec"`

	// status defines the observed state of AzureRootKey
	// +optional
	Status AzureRootKeyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// AzureRootKeyList contains a list of AzureRootKey.
type AzureRootKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AzureRootKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AzureRootKey{}, &AzureRootKeyList{})
}
