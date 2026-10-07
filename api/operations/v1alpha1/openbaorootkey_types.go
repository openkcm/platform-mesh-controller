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
type OpenBaoRootKeySpec struct {
	// TenantNameRef is the account-name of the owning Tenant.
	// +required
	TenantNameRef string `json:"tenantNameRef"`

	// EnginePath is the OpenBao Transit engine mount path (e.g. "transit").
	// +required
	EnginePath string `json:"enginePath"`
	// KeyName is the OpenBao Transit key name.
	// +required
	KeyName string `json:"keyName"`
	// ServerAddress is the OpenBao API endpoint.
	// +required
	ServerAddress string `json:"serverAddress"`

	// CertAuth is the OpenBao cert-auth backend configuration.
	// +required
	CertAuth OpenBaoCertAuthConfig `json:"certAuth"`

	// Lifecycle is the user-desired key lifecycle. Defaults to Active.
	// +optional
	// +kubebuilder:default=Active
	Lifecycle shared.DesiredLifecycle `json:"lifecycle,omitempty"`
}

// OpenBaoRootKeyStatus defines the observed state of OpenBaoRootKey.
type OpenBaoRootKeyStatus struct {
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
