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

// AWSRolesAnywhereConfig captures the IAM Roles Anywhere federation
// configuration used to authenticate to AWS KMS via X.509 client certs.
type AWSRolesAnywhereConfig struct {
	// TrustAnchorARN is the ARN of the AWS Roles Anywhere trust anchor.
	// +required
	TrustAnchorARN string `json:"trustAnchorArn"`
	// ProfileARN is the ARN of the AWS Roles Anywhere profile.
	// +required
	ProfileARN string `json:"profileArn"`
	// RoleARN is the ARN of the IAM role assumed via Roles Anywhere.
	// +required
	RoleARN string `json:"roleArn"`
}

// AWSRootKeySpec defines the desired state of AWSRootKey.
// +kubebuilder:validation:XValidation:rule="!self.configured || (self.tenantNameRef.size() > 0 && (has(self.region) && self.region.size() > 0) && (has(self.keyUri) && self.keyUri.size() > 0) && (has(self.rolesAnywhere) && self.rolesAnywhere.trustAnchorArn.size() > 0) && (has(self.rolesAnywhere) && self.rolesAnywhere.profileArn.size() > 0) && (has(self.rolesAnywhere) && self.rolesAnywhere.roleArn.size() > 0))",message="Configured slots require non-empty provider parameters"
type AWSRootKeySpec struct {
	// Configured is false for an empty provider slot. Legacy resources default to true.
	// +optional
	// +kubebuilder:default=true
	Configured *bool `json:"configured,omitempty"`
	// ResetRequest identifies an operator reset requested by the role holder.
	// +optional
	// +kubebuilder:validation:MaxLength=128
	ResetRequest string `json:"resetRequest,omitempty"`

	// TenantNameRef is the account-name of the owning Tenant (sibling in
	// the same workspace). Advisory; the reconciler derives the canonical
	// tenant identity from the workspace path (showroom#203).
	// +required
	TenantNameRef string `json:"tenantNameRef"`

	// Region is the AWS region hosting the KMS key.
	// +optional
	Region string `json:"region,omitzero"`
	// KeyURI is the ARN of the upstream AWS KMS key.
	// +optional
	KeyURI string `json:"keyUri,omitzero"`
	// EndpointURL optionally pins the AWS KMS endpoint (e.g. for VPC endpoints).
	// +optional
	EndpointURL string `json:"endpointUrl,omitempty"`

	// RolesAnywhere is the Roles Anywhere federation configuration.
	// +optional
	RolesAnywhere AWSRolesAnywhereConfig `json:"rolesAnywhere,omitzero"`

	// Lifecycle is the user-desired key lifecycle. Defaults to Active.
	// Setting to Deactivated triggers a deactivation roundtrip via the
	// upstream KMS and cascades to all dependent DomainKeys (and their
	// downstream ServiceKeys + Data Encryption Keys).
	// +optional
	// +kubebuilder:default=Active
	Lifecycle shared.DesiredLifecycle `json:"lifecycle,omitempty"`
}

// AWSRootKeyStatus defines the observed state of AWSRootKey.
type AWSRootKeyStatus struct {
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
	// Conditions represent the latest available observations of the AWSRootKey's state.
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
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.spec.region`
// +kubebuilder:printcolumn:name="Lifecycle",type=string,JSONPath=`.status.cryptoState.lifecycleState`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AWSRootKey is an L1 root-key binding to an AWS KMS key, federated via
// IAM Roles Anywhere. Referenced from DomainKey.spec.primaryRootKeyRef
// or fallbackRootKeyRefs.
type AWSRootKey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of AWSRootKey
	// +required
	Spec AWSRootKeySpec `json:"spec"`

	// status defines the observed state of AWSRootKey
	// +optional
	Status AWSRootKeyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// AWSRootKeyList contains a list of AWSRootKey.
type AWSRootKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AWSRootKey `json:"items"`
}
