/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package operations_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	operationsv1alpha1 "github.com/openkcm/platform-mesh-controller/api/operations/v1alpha1"
	"github.com/openkcm/platform-mesh-controller/api/shared"
	operations "github.com/openkcm/platform-mesh-controller/internal/controller/operations"
)

const (
	accountRef = "acc"
	rkPrimary  = "rk-primary"
)

func TestCascadeDeactivateRootKey_PrimaryAndFallbackRefs(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme), "add operations scheme")

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			// DK referencing the L1 as primary.
			&operationsv1alpha1.DomainKey{
				Name: "dk-primary", Namespace: testDefaultTenantNamespace,
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          testDomainKeyTypeTeam,
					TenantNameRef: accountRef,
					PrimaryRootKeyRef: &shared.TypedReference{
						APIGroup: operations.OperationsAPIExportName,
						Kind:     testOpenBaoRootKeyKind, Name: rkPrimary,
					},
					Lifecycle: shared.DesiredLifecycleActive,
				},
			},
			// DK referencing the L1 as a fallback.
			&operationsv1alpha1.DomainKey{
				Name: "dk-fallback", Namespace: testDefaultTenantNamespace,
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          testDomainKeyTypeTeam,
					TenantNameRef: accountRef,
					PrimaryRootKeyRef: &shared.TypedReference{
						APIGroup: operations.OperationsAPIExportName,
						Kind:     "AzureRootKey", Name: "other-primary",
					},
					FallbackRootKeyRefs: []shared.TypedReference{{
						APIGroup: operations.OperationsAPIExportName,
						Kind:     testOpenBaoRootKeyKind, Name: rkPrimary,
					}},
					Lifecycle: shared.DesiredLifecycleActive,
				},
			},
			// DK in a namespace referencing the account-level L1.
			&operationsv1alpha1.DomainKey{
				Name: "dk-namespace", Namespace: teamA,
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          testDomainKeyTypeTeam,
					TenantNameRef: accountRef,
					PrimaryRootKeyRef: &shared.TypedReference{
						APIGroup:  operations.OperationsAPIExportName,
						Kind:      testOpenBaoRootKeyKind,
						Namespace: testDefaultTenantNamespace,
						Name:      rkPrimary,
					},
					Lifecycle: shared.DesiredLifecycleActive,
				},
			},
			// DK NOT referencing the L1.
			&operationsv1alpha1.DomainKey{
				Name: "dk-unrelated", Namespace: testDefaultTenantNamespace,
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          testDomainKeyTypeTeam,
					TenantNameRef: accountRef,
					PrimaryRootKeyRef: &shared.TypedReference{
						APIGroup: operations.OperationsAPIExportName,
						Kind:     testAWSRootKeyKind, Name: "different",
					},
					Lifecycle: shared.DesiredLifecycleActive,
				},
			},
		).
		Build()

	err := operations.CascadeDeactivateRootKey(ctx, cl, testOpenBaoRootKeyKind, testDefaultTenantNamespace, rkPrimary)
	require.NoError(t, err, "cascadeDeactivateRootKey")

	cases := []struct {
		namespace string
		name      string
		want      shared.DesiredLifecycle
	}{
		{testDefaultTenantNamespace, "dk-primary", shared.DesiredLifecycleDeactivated},
		{testDefaultTenantNamespace, "dk-fallback", shared.DesiredLifecycleDeactivated},
		{teamA, "dk-namespace", shared.DesiredLifecycleDeactivated},
		{testDefaultTenantNamespace, "dk-unrelated", shared.DesiredLifecycleActive},
	}
	for _, c := range cases {
		dk := &operationsv1alpha1.DomainKey{}
		key := types.NamespacedName{Namespace: c.namespace, Name: c.name}
		require.NoErrorf(t, cl.Get(ctx, key, dk), "get %s", c.name)
		require.Equalf(t, c.want, dk.Spec.Lifecycle, "%s.spec.lifecycle", c.name)
	}
}

func TestCascadeDeactivateDomainKey_AndServiceKey(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	require.NoError(t, operationsv1alpha1.AddToScheme(scheme), "add operations scheme")

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&operationsv1alpha1.ServiceKey{
				Name: "sk-1", Namespace: testDefaultTenantNamespace,
				Spec: operationsv1alpha1.ServiceKeySpec{
					TenantNameRef: accountRef,
					DomainKeyRef:  "dk-1",
					Lifecycle:     shared.DesiredLifecycleActive,
				},
			},
			&operationsv1alpha1.ServiceKey{
				Name: "sk-3", Namespace: testDefaultTenantNamespace,
				Spec: operationsv1alpha1.ServiceKeySpec{
					TenantNameRef: accountRef,
					DomainKeyRef:  "dk-1",
					Lifecycle:     shared.DesiredLifecycleActive,
				},
			},
			&operationsv1alpha1.ServiceKey{
				Name: "sk-2", Namespace: testDefaultTenantNamespace,
				Spec: operationsv1alpha1.ServiceKeySpec{
					TenantNameRef: accountRef,
					DomainKeyRef:  "different-dk",
					Lifecycle:     shared.DesiredLifecycleActive,
				},
			},
			&operationsv1alpha1.DataEncryptionKey{
				Name: "dek-1a", Namespace: testDefaultTenantNamespace,
				Spec: operationsv1alpha1.DataEncryptionKeySpec{
					TenantNameRef: accountRef,
					ServiceKeyRef: "sk-1",
					Lifecycle:     shared.DesiredLifecycleActive,
				},
			},
			&operationsv1alpha1.DataEncryptionKey{
				Name: "dek-other", Namespace: testDefaultTenantNamespace,
				Spec: operationsv1alpha1.DataEncryptionKeySpec{
					TenantNameRef: accountRef,
					ServiceKeyRef: "sk-2",
					Lifecycle:     shared.DesiredLifecycleActive,
				},
			},
		).
		Build()

	// Lifecycle clamp test: when parent is not Active, effective desired
	// collapses to Deactivated regardless of spec.lifecycle.
	require.Equal(t, shared.DesiredLifecycleActive,
		operations.EffectiveDesiredLifecycle(shared.DesiredLifecycleActive, shared.LifecycleActive),
		"active parent + Active spec")
	require.Equal(t, shared.DesiredLifecycleDeactivated,
		operations.EffectiveDesiredLifecycle(shared.DesiredLifecycleActive, shared.LifecycleDeactivated),
		"deactivated parent + Active spec")
	require.Equal(t, shared.DesiredLifecycleDeactivated,
		operations.EffectiveDesiredLifecycle(shared.DesiredLifecycleActive, shared.LifecycleSuspended),
		"suspended parent + Active spec (child≤parent)")
	require.Equal(t, shared.DesiredLifecycleDeactivated,
		operations.EffectiveDesiredLifecycle(shared.DesiredLifecycleActive, ""),
		"missing parent state + Active spec (clamp-to-safe)")

	err := operations.CascadeDeactivateDomainKey(ctx, cl, testDefaultTenantNamespace, "dk-1")
	require.NoError(t, err, "cascadeDeactivateDomainKey")
	err = operations.CascadeDeactivateServiceKey(ctx, cl, testDefaultTenantNamespace, "sk-1")
	require.NoError(t, err, "cascadeDeactivateServiceKey")

	checks := []struct {
		obj  string
		want shared.DesiredLifecycle
	}{
		{"sk:sk-1", shared.DesiredLifecycleDeactivated},
		{"sk:sk-3", shared.DesiredLifecycleDeactivated},
		{"sk:sk-2", shared.DesiredLifecycleActive},
		{"dek:dek-1a", shared.DesiredLifecycleDeactivated},
		{"dek:dek-other", shared.DesiredLifecycleActive},
	}
	for _, c := range checks {
		var lc shared.DesiredLifecycle
		switch c.obj[:3] {
		case "sk:":
			obj := &operationsv1alpha1.ServiceKey{}
			key := types.NamespacedName{Namespace: testDefaultTenantNamespace, Name: c.obj[3:]}
			require.NoErrorf(t, cl.Get(ctx, key, obj), "get %s", c.obj)
			lc = obj.Spec.Lifecycle
		case "dek":
			obj := &operationsv1alpha1.DataEncryptionKey{}
			key := types.NamespacedName{Namespace: testDefaultTenantNamespace, Name: c.obj[4:]}
			require.NoErrorf(t, cl.Get(ctx, key, obj), "get %s", c.obj)
			lc = obj.Spec.Lifecycle
		}
		require.Equalf(t, c.want, lc, "%s lifecycle", c.obj)
	}
}
