/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package operations

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	"github.com/openkcm/openkcm-controller/api/shared"
)

const (
	accountRef = "acc"
	rkPrimary  = "rk-primary"
)

func TestCascadeDeactivateRootKey_PrimaryAndFallbackRefs(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add operations scheme: %v", err)
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			// DK referencing the L1 as primary.
			&operationsv1alpha1.DomainKey{
				Name: "dk-primary", Namespace: defaultTenantNamespace,
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          domainKeyTypeTeam,
					TenantNameRef: accountRef,
					PrimaryRootKeyRef: &shared.TypedReference{
						APIGroup: operationsAPIExportName,
						Kind:     testOpenBaoRootKeyKind, Name: rkPrimary,
					},
					Lifecycle: shared.DesiredLifecycleActive,
				},
			},
			// DK referencing the L1 as a fallback.
			&operationsv1alpha1.DomainKey{
				Name: "dk-fallback", Namespace: defaultTenantNamespace,
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          domainKeyTypeTeam,
					TenantNameRef: accountRef,
					PrimaryRootKeyRef: &shared.TypedReference{
						APIGroup: operationsAPIExportName,
						Kind:     "AzureRootKey", Name: "other-primary",
					},
					FallbackRootKeyRefs: []shared.TypedReference{{
						APIGroup: operationsAPIExportName,
						Kind:     testOpenBaoRootKeyKind, Name: rkPrimary,
					}},
					Lifecycle: shared.DesiredLifecycleActive,
				},
			},
			// DK in a namespace referencing the account-level L1.
			&operationsv1alpha1.DomainKey{
				Name: "dk-namespace", Namespace: teamA,
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          domainKeyTypeTeam,
					TenantNameRef: accountRef,
					PrimaryRootKeyRef: &shared.TypedReference{
						APIGroup:  operationsAPIExportName,
						Kind:      testOpenBaoRootKeyKind,
						Namespace: defaultTenantNamespace,
						Name:      rkPrimary,
					},
					Lifecycle: shared.DesiredLifecycleActive,
				},
			},
			// DK NOT referencing the L1.
			&operationsv1alpha1.DomainKey{
				Name: "dk-unrelated", Namespace: defaultTenantNamespace,
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          domainKeyTypeTeam,
					TenantNameRef: accountRef,
					PrimaryRootKeyRef: &shared.TypedReference{
						APIGroup: operationsAPIExportName,
						Kind:     "AWSRootKey", Name: "different",
					},
					Lifecycle: shared.DesiredLifecycleActive,
				},
			},
		).
		Build()

	if err := cascadeDeactivateRootKey(ctx, cl, testOpenBaoRootKeyKind, defaultTenantNamespace, rkPrimary); err != nil {
		t.Fatalf("cascadeDeactivateRootKey: %v", err)
	}

	cases := []struct {
		namespace string
		name      string
		want      shared.DesiredLifecycle
	}{
		{defaultTenantNamespace, "dk-primary", shared.DesiredLifecycleDeactivated},
		{defaultTenantNamespace, "dk-fallback", shared.DesiredLifecycleDeactivated},
		{teamA, "dk-namespace", shared.DesiredLifecycleDeactivated},
		{defaultTenantNamespace, "dk-unrelated", shared.DesiredLifecycleActive},
	}
	for _, c := range cases {
		dk := &operationsv1alpha1.DomainKey{}
		if err := cl.Get(ctx, types.NamespacedName{Namespace: c.namespace, Name: c.name}, dk); err != nil {
			t.Fatalf("get %s: %v", c.name, err)
		}
		if dk.Spec.Lifecycle != c.want {
			t.Fatalf("%s.spec.lifecycle = %q, want %q", c.name, dk.Spec.Lifecycle, c.want)
		}
	}
}

func TestCascadeDeactivateDomainKey_AndServiceKey(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add operations scheme: %v", err)
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&operationsv1alpha1.ServiceKey{
				Name: "sk-1", Namespace: defaultTenantNamespace,
				Spec: operationsv1alpha1.ServiceKeySpec{
					TenantNameRef: accountRef,
					DomainKeyRef:  "dk-1",
					Lifecycle:     shared.DesiredLifecycleActive,
				},
			},
			&operationsv1alpha1.ServiceKey{
				Name: "sk-2", Namespace: defaultTenantNamespace,
				Spec: operationsv1alpha1.ServiceKeySpec{
					TenantNameRef: accountRef,
					DomainKeyRef:  "different-dk",
					Lifecycle:     shared.DesiredLifecycleActive,
				},
			},
			&operationsv1alpha1.DataEncryptionKey{
				Name: "dek-1a", Namespace: defaultTenantNamespace,
				Spec: operationsv1alpha1.DataEncryptionKeySpec{
					TenantNameRef: accountRef,
					ServiceKeyRef: "sk-1",
					Lifecycle:     shared.DesiredLifecycleActive,
				},
			},
			&operationsv1alpha1.DataEncryptionKey{
				Name: "dek-other", Namespace: defaultTenantNamespace,
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
	if got := effectiveDesiredLifecycle(shared.DesiredLifecycleActive, shared.LifecycleActive); got != shared.DesiredLifecycleActive {
		t.Fatalf("active parent + Active spec = %q, want Active", got)
	}
	if got := effectiveDesiredLifecycle(shared.DesiredLifecycleActive, shared.LifecycleDeactivated); got != shared.DesiredLifecycleDeactivated {
		t.Fatalf("deactivated parent + Active spec = %q, want Deactivated", got)
	}
	if got := effectiveDesiredLifecycle(shared.DesiredLifecycleActive, shared.LifecycleSuspended); got != shared.DesiredLifecycleDeactivated {
		t.Fatalf("suspended parent + Active spec = %q, want Deactivated (child≤parent)", got)
	}
	if got := effectiveDesiredLifecycle(shared.DesiredLifecycleActive, ""); got != shared.DesiredLifecycleDeactivated {
		t.Fatalf("missing parent state + Active spec = %q, want Deactivated (clamp-to-safe)", got)
	}

	if err := cascadeDeactivateDomainKey(ctx, cl, defaultTenantNamespace, "dk-1"); err != nil {
		t.Fatalf("cascadeDeactivateDomainKey: %v", err)
	}
	if err := cascadeDeactivateServiceKey(ctx, cl, defaultTenantNamespace, "sk-1"); err != nil {
		t.Fatalf("cascadeDeactivateServiceKey: %v", err)
	}

	checks := []struct {
		obj  string
		want shared.DesiredLifecycle
	}{
		{"sk:sk-1", shared.DesiredLifecycleDeactivated},
		{"sk:sk-2", shared.DesiredLifecycleActive},
		{"dek:dek-1a", shared.DesiredLifecycleDeactivated},
		{"dek:dek-other", shared.DesiredLifecycleActive},
	}
	for _, c := range checks {
		var lc shared.DesiredLifecycle
		switch c.obj[:3] {
		case "sk:":
			obj := &operationsv1alpha1.ServiceKey{}
			if err := cl.Get(ctx, types.NamespacedName{Namespace: defaultTenantNamespace, Name: c.obj[3:]}, obj); err != nil {
				t.Fatalf("get %s: %v", c.obj, err)
			}
			lc = obj.Spec.Lifecycle
		case "dek":
			obj := &operationsv1alpha1.DataEncryptionKey{}
			if err := cl.Get(ctx, types.NamespacedName{Namespace: defaultTenantNamespace, Name: c.obj[4:]}, obj); err != nil {
				t.Fatalf("get %s: %v", c.obj, err)
			}
			lc = obj.Spec.Lifecycle
		}
		if lc != c.want {
			t.Fatalf("%s lifecycle = %q, want %q", c.obj, lc, c.want)
		}
	}
}
