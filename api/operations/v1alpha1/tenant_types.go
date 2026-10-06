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

// OIDCProvider defines the OIDC provider configuration for a tenant.
type OIDCProvider struct {
	// Issuer is the OIDC issuer URL.
	// +required
	Issuer string `json:"issuer"`
	// JWKSURI is the JWKS URI for token verification.
	// +required
	JWKSURI string `json:"jwks_uri"`
	// Audiences is the list of accepted token audiences.
	// +optional
	Audiences []string `json:"audiences,omitempty"`
}

// TenantSpec defines the desired state of Tenant. One Tenant per account
// workspace, named after the account (showroom#217).
type TenantSpec struct {
	// Region is the customer tenant region/location.
	// +optional
	Region string `json:"region,omitempty"`
	// OIDCProvider is the OIDC provider configuration. Optional for the MVP;
	// the operator falls back to operator-flag defaults when unset.
	// +optional
	OIDCProvider *OIDCProvider `json:"oidcProvider,omitempty"`
}

// TenantStatus defines the observed state of Tenant. Tenant is identity-only
// and does not carry a CryptoState.
type TenantStatus struct {
	// ObservedGeneration is the last observed generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions represent the latest available observations of the Tenant's state.
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
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.spec.region`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Tenant represents a Platform Mesh account-level identity for OpenKCM.
// Lives in the account workspace; metadata.name == account name.
type Tenant struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Tenant
	// +required
	Spec TenantSpec `json:"spec"`

	// status defines the observed state of Tenant
	// +optional
	Status TenantStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TenantList contains a list of Tenant.
type TenantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Tenant `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Tenant{}, &TenantList{})
}
