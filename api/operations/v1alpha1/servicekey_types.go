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

// ServiceKeySpec defines the desired state of ServiceKey (L3).
//
// ServiceKey is a per-service encryption key request that hangs off a
// sibling DomainKey by name. Lifecycle (PreActive→Active→…→Destroyed) is
// driven by mock-API endpoints rather than a user-controlled activation flag.
type ServiceKeySpec struct {
	// TenantNameRef is the account-name of the owning Tenant.
	// +required
	TenantNameRef string `json:"tenantNameRef"`
	// DomainKeyRef is the name of the parent DomainKey in the same workspace.
	// +required
	DomainKeyRef string `json:"domainKeyRef"`

	// Lifecycle is the user-desired key lifecycle. Defaults to Active.
	// Setting to Deactivated cascades to all Data Encryption Keys under
	// this Service Key.
	// +optional
	// +kubebuilder:default=Active
	Lifecycle shared.DesiredLifecycle `json:"lifecycle,omitempty"`
}

// ServiceKeyStatus defines the observed state of ServiceKey.
type ServiceKeyStatus struct {
	// ObservedGeneration is the last observed generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions represent the latest available observations of the ServiceKey's state.
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
// +kubebuilder:printcolumn:name="DomainKey",type=string,JSONPath=`.spec.domainKeyRef`
// +kubebuilder:printcolumn:name="Lifecycle",type=string,JSONPath=`.status.cryptoState.lifecycleState`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ServiceKey defines an encryption key request for a service consumer.
type ServiceKey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ServiceKey
	// +required
	Spec ServiceKeySpec `json:"spec"`

	// status defines the observed state of ServiceKey
	// +optional
	Status ServiceKeyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ServiceKeyList contains a list of ServiceKey.
type ServiceKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ServiceKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServiceKey{}, &ServiceKeyList{})
}
