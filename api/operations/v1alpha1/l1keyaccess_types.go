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
)

// L1KeyAccessName is the name of the one L1KeyAccess of an account.
const L1KeyAccessName = "account-l1"

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=l1ka
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'account-l1'",message="The account marker must be named account-l1"

// L1KeyAccess is the IAM assignment target for all L1 keys in an account.
// The provider owns its lifecycle. Role assignments live in Platform Mesh IAM.
type L1KeyAccess struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`
}

// +kubebuilder:object:root=true

// L1KeyAccessList contains account authorization markers.
type L1KeyAccessList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []L1KeyAccess `json:"items"`
}
