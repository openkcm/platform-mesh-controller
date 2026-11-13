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

// AWSAuthMethod names how Krypton authenticates to the customer's AWS account.
// +kubebuilder:validation:Enum=AccessKey
type AWSAuthMethod string

// AWSAuthMethodAccessKey is a long-lived access key (ID and secret) of an IAM
// user that may only assume AWSAccessKeyAuth.RoleARN.
const AWSAuthMethodAccessKey AWSAuthMethod = "AccessKey"

// AWSAccessKeyAuth holds what the AccessKey method needs besides the key
// itself. The access key ID and the secret access key are never part of the
// resource: they live in the Secret that SecretRef names.
type AWSAccessKeyAuth struct {
	// RoleARN is the ARN of the IAM role the access key's user assumes to
	// reach the KMS key.
	// +required
	// +kubebuilder:validation:MinLength=1
	RoleARN string `json:"roleArn"`
	// SecretRef points at the Secret awsrootkey-<metadata.name>, in the same
	// namespace, with the entries accessKeyId and secretAccessKey. The Secret is
	// created after this resource and carries an ownerReference to it.
	// +required
	SecretRef shared.SecretReference `json:"secretRef"`
}

// AWSAuth selects the authentication method and carries the block of that
// method.
// +kubebuilder:validation:XValidation:rule="self.method != 'AccessKey' || has(self.accessKey)",message="accessKey is required when method is AccessKey"
type AWSAuth struct {
	// Method is the authentication method. AccessKey is the only one.
	// +required
	Method AWSAuthMethod `json:"method"`
	// AccessKey is set when Method is AccessKey.
	// +optional
	AccessKey *AWSAccessKeyAuth `json:"accessKey,omitempty"`
}

// AWSRootKeySpec defines the desired state of AWSRootKey.
// +kubebuilder:validation:XValidation:rule="!self.configured || (self.tenantNameRef.size() > 0 && (has(self.region) && self.region.size() > 0) && (has(self.keyUri) && self.keyUri.size() > 0) && has(self.auth))",message="A configured root key requires tenantNameRef, region, keyUri and auth"
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

	// Auth is how Krypton authenticates to the customer's AWS account. It is
	// required while the root key is configured (see the validation on the
	// spec) and empty only for an empty provider slot.
	// +optional
	Auth *AWSAuth `json:"auth,omitempty"`

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

// AWSRootKey is an L1 root-key binding to an AWS KMS key, reached with the
// customer's IAM access key. Referenced from DomainKey.spec.primaryRootKeyRef
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
