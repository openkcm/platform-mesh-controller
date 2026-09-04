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

// DataEncryptionKeyKMIP carries upstream KMIP attributes to attach to the
// data encryption key when it is provisioned.
type DataEncryptionKeyKMIP struct {
	// Attributes is a free-form string→string bag passed through to the
	// upstream KMIP server as object attributes.
	// +optional
	Attributes map[string]string `json:"attributes,omitempty"`
}

// DataEncryptionKeySpec defines the desired state of DataEncryptionKey (L4).
type DataEncryptionKeySpec struct {
	// TenantNameRef is the account-name of the owning Tenant.
	// +required
	TenantNameRef string `json:"tenantNameRef"`

	// ServiceKeyRef is the name of the parent ServiceKey in the same workspace.
	// +required
	ServiceKeyRef string `json:"serviceKeyRef"`

	// KMIP carries optional KMIP attributes to attach to the DEK.
	// +optional
	KMIP *DataEncryptionKeyKMIP `json:"kmip,omitempty"`

	// Lifecycle is the user-desired key lifecycle. Defaults to Active.
	// +optional
	// +kubebuilder:default=Active
	Lifecycle shared.DesiredLifecycle `json:"lifecycle,omitempty"`
}

// DataEncryptionKeyStatus defines the observed state of DataEncryptionKey.
type DataEncryptionKeyStatus struct {
	// ObservedGeneration is the last observed generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions represent the latest available observations of the DataEncryptionKey's state.
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
// +kubebuilder:printcolumn:name="ServiceKey",type=string,JSONPath=`.spec.serviceKeyRef`
// +kubebuilder:printcolumn:name="Lifecycle",type=string,JSONPath=`.status.cryptoState.lifecycleState`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DataEncryptionKey (L4) is the per-workload data encryption key. References
// a sibling ServiceKey by name; the reconciler waits until the parent
// ServiceKey reaches lifecycleState=Active before provisioning.
type DataEncryptionKey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DataEncryptionKey
	// +required
	Spec DataEncryptionKeySpec `json:"spec"`

	// status defines the observed state of DataEncryptionKey
	// +optional
	Status DataEncryptionKeyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DataEncryptionKeyList contains a list of DataEncryptionKey.
type DataEncryptionKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DataEncryptionKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DataEncryptionKey{}, &DataEncryptionKeyList{})
}
