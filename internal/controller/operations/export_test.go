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

package operations

const (
	OperationsAPIExportName = operationsAPIExportName
	PathAnnotation          = pathAnnotation
	PollInterval            = pollInterval
	ReadyType               = readyType
	ReasonDomainKeyInUse    = reasonDomainKeyInUse
	ReasonFailed            = reasonFailed
	ReasonProcess           = reasonProcess
	ServiceKeyFinalizer     = serviceKeyFinalizer
	TenantFinalizer         = tenantFinalizer
	TenantIDAnnotation      = tenantIDAnnotation
)

type AccountIdentity = accountIdentity

var (
	AccountNameFromPath                = accountNameFromPath
	AutoDomainKeyName                  = autoDomainKeyName
	CascadeDeactivateDomainKey         = cascadeDeactivateDomainKey
	CascadeDeactivateRootKey           = cascadeDeactivateRootKey
	CascadeDeactivateServiceKey        = cascadeDeactivateServiceKey
	DataEncryptionKeyOpenKCMName       = dataEncryptionKeyOpenKCMName
	DefaultAccountNamespace            = defaultAccountNamespace
	DomainKeyOpenKCMName               = domainKeyOpenKCMName
	EffectiveDesiredLifecycle          = effectiveDesiredLifecycle
	EnsureAutoDomainKeyForNamespace    = ensureAutoDomainKeyForNamespace
	EnsureAutoDomainKeysForAccountRoot = ensureAutoDomainKeysForAccountRoot
	EnsureDomainKey                    = (*AccountBootstrapReconciler).ensureDomainKey
	EnsureTenant                       = (*AccountBootstrapReconciler).ensureTenant
	FindEarlierDomainKey               = (*DomainKeyReconciler).findEarlierDomainKey
	InstanceDomainKeyTakenBy           = instanceDomainKeyTakenBy
	IsOperationsAPIBinding             = isOperationsAPIBinding
	PrimaryRootKeyLifecycle            = (*DomainKeyReconciler).primaryRootKeyLifecycle
	ResolvePrimaryRootKey              = (*DomainKeyReconciler).resolvePrimaryRootKey
	ResolveTenantID                    = resolveTenantID
	RootKeyResolutionFailureResult     = rootKeyResolutionFailureResult
	ServiceKeyOpenKCMName              = serviceKeyOpenKCMName
)
