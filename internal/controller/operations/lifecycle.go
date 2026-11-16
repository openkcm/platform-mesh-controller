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

	"github.com/openkcm/platform-mesh-controller/api/shared"
	"github.com/openkcm/platform-mesh-controller/internal/openkcmapi"
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

// effectiveDesiredLifecycle keeps a child from being activated while its parent
// is not Active. It never deactivates a child that is already Active: there
// is no cascade, the parent's state only gates activation.
func effectiveDesiredLifecycle(
	specLifecycle shared.DesiredLifecycle,
	parentState shared.LifecycleState,
	currentState shared.LifecycleState,
) shared.DesiredLifecycle {
	if parentState != shared.LifecycleActive && currentState != shared.LifecycleActive {
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
	apiClient keyLifecycler,
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
