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

// The methods below give every L1 kind one shape for the reconciler to drive.
// They are hand-written: controller-gen emits deepcopy only, and the
// alternative is a type switch in the reconciler for every field it touches.
// The interface they satisfy is declared by its consumer, in
// internal/controller/operations.

// --- AWSRootKey ---

func (rk *AWSRootKey) ProviderName() string { return "aws" }

func (rk *AWSRootKey) ProviderConfig() map[string]string {
	return map[string]string{
		"region":         rk.Spec.Region,
		"keyUri":         rk.Spec.KeyURI,
		"endpointUrl":    rk.Spec.EndpointURL,
		"trustAnchorArn": rk.Spec.RolesAnywhere.TrustAnchorARN,
		"profileArn":     rk.Spec.RolesAnywhere.ProfileARN,
		"roleArn":        rk.Spec.RolesAnywhere.RoleARN,
	}
}

func (rk *AWSRootKey) TenantNameRef() string                     { return rk.Spec.TenantNameRef }
func (rk *AWSRootKey) DesiredLifecycle() shared.DesiredLifecycle { return rk.Spec.Lifecycle }
func (rk *AWSRootKey) GetCryptoState() *shared.CryptoState       { return rk.Status.CryptoState }
func (rk *AWSRootKey) SetCryptoState(s *shared.CryptoState)      { rk.Status.CryptoState = s }
func (rk *AWSRootKey) GetReconciliationStatus() *shared.ReconciliationStatus {
	return rk.Status.ReconciliationStatus
}
func (rk *AWSRootKey) SetReconciliationStatus(s *shared.ReconciliationStatus) {
	rk.Status.ReconciliationStatus = s
}
func (rk *AWSRootKey) SetOperationID(id string)              { rk.Status.OperationID = id }
func (rk *AWSRootKey) GetObservedGeneration() int64          { return rk.Status.ObservedGeneration }
func (rk *AWSRootKey) SetObservedGeneration(g int64)         { rk.Status.ObservedGeneration = g }
func (rk *AWSRootKey) StatusConditions() *[]metav1.Condition { return &rk.Status.Conditions }

// --- AzureRootKey ---

func (rk *AzureRootKey) ProviderName() string { return "azure" }

func (rk *AzureRootKey) ProviderConfig() map[string]string {
	return map[string]string{
		"vaultUrl":   rk.Spec.VaultURL,
		"keyName":    rk.Spec.KeyName,
		"keyVersion": rk.Spec.KeyVersion,
		"tenantId":   rk.Spec.FederatedIdentity.TenantID,
		"clientId":   rk.Spec.FederatedIdentity.ClientID,
	}
}

func (rk *AzureRootKey) TenantNameRef() string                     { return rk.Spec.TenantNameRef }
func (rk *AzureRootKey) DesiredLifecycle() shared.DesiredLifecycle { return rk.Spec.Lifecycle }
func (rk *AzureRootKey) GetCryptoState() *shared.CryptoState       { return rk.Status.CryptoState }
func (rk *AzureRootKey) SetCryptoState(s *shared.CryptoState)      { rk.Status.CryptoState = s }
func (rk *AzureRootKey) GetReconciliationStatus() *shared.ReconciliationStatus {
	return rk.Status.ReconciliationStatus
}
func (rk *AzureRootKey) SetReconciliationStatus(s *shared.ReconciliationStatus) {
	rk.Status.ReconciliationStatus = s
}
func (rk *AzureRootKey) SetOperationID(id string)      { rk.Status.OperationID = id }
func (rk *AzureRootKey) GetObservedGeneration() int64  { return rk.Status.ObservedGeneration }
func (rk *AzureRootKey) SetObservedGeneration(g int64) { rk.Status.ObservedGeneration = g }
func (rk *AzureRootKey) StatusConditions() *[]metav1.Condition {
	return &rk.Status.Conditions
}

// --- OpenBaoRootKey ---

func (rk *OpenBaoRootKey) ProviderName() string { return "openbao" }

func (rk *OpenBaoRootKey) ProviderConfig() map[string]string {
	return map[string]string{
		"serverAddress": rk.Spec.ServerAddress,
		"enginePath":    rk.Spec.EnginePath,
		"keyName":       rk.Spec.KeyName,
		"authMountPath": rk.Spec.CertAuth.AuthMountPath,
		"roleName":      rk.Spec.CertAuth.RoleName,
	}
}

func (rk *OpenBaoRootKey) TenantNameRef() string                     { return rk.Spec.TenantNameRef }
func (rk *OpenBaoRootKey) DesiredLifecycle() shared.DesiredLifecycle { return rk.Spec.Lifecycle }
func (rk *OpenBaoRootKey) GetCryptoState() *shared.CryptoState       { return rk.Status.CryptoState }
func (rk *OpenBaoRootKey) SetCryptoState(s *shared.CryptoState)      { rk.Status.CryptoState = s }
func (rk *OpenBaoRootKey) GetReconciliationStatus() *shared.ReconciliationStatus {
	return rk.Status.ReconciliationStatus
}
func (rk *OpenBaoRootKey) SetReconciliationStatus(s *shared.ReconciliationStatus) {
	rk.Status.ReconciliationStatus = s
}
func (rk *OpenBaoRootKey) SetOperationID(id string)      { rk.Status.OperationID = id }
func (rk *OpenBaoRootKey) GetObservedGeneration() int64  { return rk.Status.ObservedGeneration }
func (rk *OpenBaoRootKey) SetObservedGeneration(g int64) { rk.Status.ObservedGeneration = g }
func (rk *OpenBaoRootKey) StatusConditions() *[]metav1.Condition {
	return &rk.Status.Conditions
}

// --- GCPRootKey ---

func (rk *GCPRootKey) ProviderName() string { return "gcp" }

func (rk *GCPRootKey) ProviderConfig() map[string]string {
	return map[string]string{
		"keyUri": rk.Spec.KeyURI,
	}
}

func (rk *GCPRootKey) TenantNameRef() string                     { return rk.Spec.TenantNameRef }
func (rk *GCPRootKey) DesiredLifecycle() shared.DesiredLifecycle { return rk.Spec.Lifecycle }
func (rk *GCPRootKey) GetCryptoState() *shared.CryptoState       { return rk.Status.CryptoState }
func (rk *GCPRootKey) SetCryptoState(s *shared.CryptoState)      { rk.Status.CryptoState = s }
func (rk *GCPRootKey) GetReconciliationStatus() *shared.ReconciliationStatus {
	return rk.Status.ReconciliationStatus
}
func (rk *GCPRootKey) SetReconciliationStatus(s *shared.ReconciliationStatus) {
	rk.Status.ReconciliationStatus = s
}
func (rk *GCPRootKey) SetOperationID(id string)              { rk.Status.OperationID = id }
func (rk *GCPRootKey) GetObservedGeneration() int64          { return rk.Status.ObservedGeneration }
func (rk *GCPRootKey) SetObservedGeneration(g int64)         { rk.Status.ObservedGeneration = g }
func (rk *GCPRootKey) StatusConditions() *[]metav1.Condition { return &rk.Status.Conditions }

// --- HSMRootKey ---

func (rk *HSMRootKey) ProviderName() string { return "hsm" }

func (rk *HSMRootKey) ProviderConfig() map[string]string {
	return map[string]string{
		"slotUri": rk.Spec.SlotURI,
	}
}

func (rk *HSMRootKey) TenantNameRef() string                     { return rk.Spec.TenantNameRef }
func (rk *HSMRootKey) DesiredLifecycle() shared.DesiredLifecycle { return rk.Spec.Lifecycle }
func (rk *HSMRootKey) GetCryptoState() *shared.CryptoState       { return rk.Status.CryptoState }
func (rk *HSMRootKey) SetCryptoState(s *shared.CryptoState)      { rk.Status.CryptoState = s }
func (rk *HSMRootKey) GetReconciliationStatus() *shared.ReconciliationStatus {
	return rk.Status.ReconciliationStatus
}
func (rk *HSMRootKey) SetReconciliationStatus(s *shared.ReconciliationStatus) {
	rk.Status.ReconciliationStatus = s
}
func (rk *HSMRootKey) SetOperationID(id string)              { rk.Status.OperationID = id }
func (rk *HSMRootKey) GetObservedGeneration() int64          { return rk.Status.ObservedGeneration }
func (rk *HSMRootKey) SetObservedGeneration(g int64)         { rk.Status.ObservedGeneration = g }
func (rk *HSMRootKey) StatusConditions() *[]metav1.Condition { return &rk.Status.Conditions }

// --- VaultRootKey ---

func (rk *VaultRootKey) ProviderName() string { return "vault" }

func (rk *VaultRootKey) ProviderConfig() map[string]string {
	return map[string]string{
		"serverAddress": rk.Spec.ServerAddress,
		"keyName":       rk.Spec.KeyName,
	}
}

func (rk *VaultRootKey) TenantNameRef() string                     { return rk.Spec.TenantNameRef }
func (rk *VaultRootKey) DesiredLifecycle() shared.DesiredLifecycle { return rk.Spec.Lifecycle }
func (rk *VaultRootKey) GetCryptoState() *shared.CryptoState       { return rk.Status.CryptoState }
func (rk *VaultRootKey) SetCryptoState(s *shared.CryptoState)      { rk.Status.CryptoState = s }
func (rk *VaultRootKey) GetReconciliationStatus() *shared.ReconciliationStatus {
	return rk.Status.ReconciliationStatus
}
func (rk *VaultRootKey) SetReconciliationStatus(s *shared.ReconciliationStatus) {
	rk.Status.ReconciliationStatus = s
}
func (rk *VaultRootKey) SetOperationID(id string)              { rk.Status.OperationID = id }
func (rk *VaultRootKey) GetObservedGeneration() int64          { return rk.Status.ObservedGeneration }
func (rk *VaultRootKey) SetObservedGeneration(g int64)         { rk.Status.ObservedGeneration = g }
func (rk *VaultRootKey) StatusConditions() *[]metav1.Condition { return &rk.Status.Conditions }
