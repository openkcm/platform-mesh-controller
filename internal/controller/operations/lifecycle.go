/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package operations

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	"github.com/openkcm/openkcm-controller/api/shared"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

// desiredLifecycleState converts the user-facing spec.lifecycle field to the
// expected status.cryptoState.lifecycleState. An empty / unspecified
// spec.lifecycle defaults to Active.
func desiredLifecycleState(d shared.DesiredLifecycle) shared.LifecycleState {
	if d == shared.DesiredLifecycleDeactivated {
		return shared.LifecycleDeactivated
	}
	return shared.LifecycleActive
}

// effectiveDesiredLifecycle clamps spec.lifecycle by the parent's actual
// lifecycle state. A child cannot be in a state more active than its parent —
// if the parent is anything other than Active, the child must also be
// Deactivated regardless of its own spec.lifecycle. This guards against
// the case where a user re-activates a child while its parent is still
// Deactivated, which would otherwise create an impossible state where the
// encryption chain claims to work but the parent isn't backing it.
func effectiveDesiredLifecycle(
	specLifecycle shared.DesiredLifecycle,
	parentState shared.LifecycleState,
) shared.DesiredLifecycle {
	if parentState != shared.LifecycleActive {
		return shared.DesiredLifecycleDeactivated
	}
	return specLifecycle
}

// reconcileLifecycle drives the mock API to bring a key's effective lifecycle
// into line with the user-desired spec.lifecycle. Returns the new lifecycle
// state and whether a transition was performed.
//
// Only Active⇄Deactivated is exercised here; PreActive→Active is handled by
// the caller's standard create+activate flow before this function is
// reached. KMS-driven transitions (Suspended / Compromised / Destroyed)
// are not user-settable and are ignored on the desired-state side.
func reconcileLifecycle(
	ctx context.Context,
	apiClient Backend,
	keyID string,
	current shared.LifecycleState,
	desired shared.DesiredLifecycle,
) (shared.LifecycleState, bool, error) {
	target := desiredLifecycleState(desired)
	if current == target {
		return current, false, nil
	}
	// We only manage Active⇄Deactivated transitions. If the key is in any
	// other state (PreActive, Suspended, Compromised, Destroyed), defer to
	// the caller's normal flow instead of forcing a transition here.
	if current != shared.LifecycleActive && current != shared.LifecycleDeactivated {
		return current, false, nil
	}
	var resp *openkcmapi.ActivateKeyResponse
	var err error
	switch target {
	case shared.LifecycleActive:
		resp, err = apiClient.ActivateKey(ctx, keyID)
	case shared.LifecycleDeactivated:
		resp, err = apiClient.DeactivateKey(ctx, keyID)
	default:
		return current, false, nil
	}
	if err != nil {
		return current, false, err
	}
	return shared.LifecycleState(resp.LifecycleState), true, nil
}

// cascadeDeactivateRootKey patches spec.lifecycle=Deactivated on every
// DomainKey whose primary or fallback root key reference matches the given
// (rootKeyKind, rootKeyNamespace, rootKeyName). The DomainKey reconciler will
// then deactivate the domain key itself and cascade onward to its dependent
// ServiceKeys + Data Encryption Keys.
func cascadeDeactivateRootKey(ctx context.Context, cl client.Client, rootKeyKind, namespace, rootKeyName string) error {
	dks := &operationsv1alpha1.DomainKeyList{}
	if err := cl.List(ctx, dks); err != nil {
		return err
	}
	for i := range dks.Items {
		dk := &dks.Items[i]
		if !rootKeyRefMatches(dk, rootKeyKind, namespace, rootKeyName) {
			continue
		}
		if dk.Spec.Lifecycle == shared.DesiredLifecycleDeactivated {
			continue
		}
		dk.Spec.Lifecycle = shared.DesiredLifecycleDeactivated
		if err := cl.Update(ctx, dk); err != nil {
			return err
		}
	}
	return nil
}

func rootKeyRefMatches(dk *operationsv1alpha1.DomainKey, kind, namespace, name string) bool {
	if primary := dk.Spec.PrimaryRootKeyRef; primary != nil &&
		primary.Kind == kind && rootKeyReferenceNamespace(primary, dk.Namespace) == namespace && primary.Name == name {
		return true
	}
	for _, ref := range dk.Spec.FallbackRootKeyRefs {
		if ref.Kind == kind && rootKeyReferenceNamespace(&ref, dk.Namespace) == namespace && ref.Name == name {
			return true
		}
	}
	return false
}

// cascadeDeactivateDomainKey patches spec.lifecycle=Deactivated on every
// ServiceKey in the same namespace that points at this DomainKey.
func cascadeDeactivateDomainKey(ctx context.Context, cl client.Client, namespace, domainKeyName string) error {
	sks := &operationsv1alpha1.ServiceKeyList{}
	if err := cl.List(ctx, sks, client.InNamespace(namespace)); err != nil {
		return err
	}
	for i := range sks.Items {
		sk := &sks.Items[i]
		if sk.Spec.DomainKeyRef != domainKeyName {
			continue
		}
		if sk.Spec.Lifecycle == shared.DesiredLifecycleDeactivated {
			continue
		}
		sk.Spec.Lifecycle = shared.DesiredLifecycleDeactivated
		if err := cl.Update(ctx, sk); err != nil {
			return err
		}
	}
	return nil
}

// cascadeDeactivateServiceKey patches spec.lifecycle=Deactivated on every
// DataEncryptionKey in the same namespace that points at this ServiceKey.
func cascadeDeactivateServiceKey(ctx context.Context, cl client.Client, namespace, serviceKeyName string) error {
	deks := &operationsv1alpha1.DataEncryptionKeyList{}
	if err := cl.List(ctx, deks, client.InNamespace(namespace)); err != nil {
		return err
	}
	for i := range deks.Items {
		dek := &deks.Items[i]
		if dek.Spec.ServiceKeyRef != serviceKeyName {
			continue
		}
		if dek.Spec.Lifecycle == shared.DesiredLifecycleDeactivated {
			continue
		}
		dek.Spec.Lifecycle = shared.DesiredLifecycleDeactivated
		if err := cl.Update(ctx, dek); err != nil {
			return err
		}
	}
	return nil
}
