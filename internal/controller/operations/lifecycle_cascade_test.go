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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	"github.com/openkcm/openkcm-controller/api/shared"
)

func TestCascadeDeactivateRootKey_PrimaryAndFallbackRefs(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add operations scheme: %v", err)
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			// DK referencing the L1 as primary.
			&operationsv1alpha1.DomainKey{
				ObjectMeta: metav1.ObjectMeta{Name: "dk-primary", Namespace: "default"},
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          "Team",
					TenantNameRef: "acc",
					PrimaryRootKeyRef: &shared.TypedReference{
						APIGroup: "operations.openkcm.io",
						Kind:     "OpenBaoRootKey", Name: "rk-primary",
					},
					Lifecycle: shared.DesiredLifecycleActive,
				},
			},
			// DK referencing the L1 as a fallback.
			&operationsv1alpha1.DomainKey{
				ObjectMeta: metav1.ObjectMeta{Name: "dk-fallback", Namespace: "default"},
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          "Team",
					TenantNameRef: "acc",
					PrimaryRootKeyRef: &shared.TypedReference{
						APIGroup: "operations.openkcm.io",
						Kind:     "AzureRootKey", Name: "other-primary",
					},
					FallbackRootKeyRefs: []shared.TypedReference{{
						APIGroup: "operations.openkcm.io",
						Kind:     "OpenBaoRootKey", Name: "rk-primary",
					}},
					Lifecycle: shared.DesiredLifecycleActive,
				},
			},
			// DK in a namespace referencing the account-level L1.
			&operationsv1alpha1.DomainKey{
				ObjectMeta: metav1.ObjectMeta{Name: "dk-namespace", Namespace: "team-a"},
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          "Team",
					TenantNameRef: "acc",
					PrimaryRootKeyRef: &shared.TypedReference{
						APIGroup:  "operations.openkcm.io",
						Kind:      "OpenBaoRootKey",
						Namespace: "default",
						Name:      "rk-primary",
					},
					Lifecycle: shared.DesiredLifecycleActive,
				},
			},
			// DK NOT referencing the L1.
			&operationsv1alpha1.DomainKey{
				ObjectMeta: metav1.ObjectMeta{Name: "dk-unrelated", Namespace: "default"},
				Spec: operationsv1alpha1.DomainKeySpec{
					Type:          "Team",
					TenantNameRef: "acc",
					PrimaryRootKeyRef: &shared.TypedReference{
						APIGroup: "operations.openkcm.io",
						Kind:     "AWSRootKey", Name: "different",
					},
					Lifecycle: shared.DesiredLifecycleActive,
				},
			},
		).
		Build()

	if err := cascadeDeactivateRootKey(ctx, cl, "OpenBaoRootKey", "default", "rk-primary"); err != nil {
		t.Fatalf("cascadeDeactivateRootKey: %v", err)
	}

	cases := []struct {
		namespace string
		name      string
		want      shared.DesiredLifecycle
	}{
		{"default", "dk-primary", shared.DesiredLifecycleDeactivated},
		{"default", "dk-fallback", shared.DesiredLifecycleDeactivated},
		{"team-a", "dk-namespace", shared.DesiredLifecycleDeactivated},
		{"default", "dk-unrelated", shared.DesiredLifecycleActive},
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
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := operationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add operations scheme: %v", err)
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&operationsv1alpha1.ServiceKey{
				ObjectMeta: metav1.ObjectMeta{Name: "sk-1", Namespace: "default"},
				Spec: operationsv1alpha1.ServiceKeySpec{
					TenantNameRef: "acc",
					DomainKeyRef:  "dk-1",
					Lifecycle:     shared.DesiredLifecycleActive,
				},
			},
			&operationsv1alpha1.ServiceKey{
				ObjectMeta: metav1.ObjectMeta{Name: "sk-2", Namespace: "default"},
				Spec: operationsv1alpha1.ServiceKeySpec{
					TenantNameRef: "acc",
					DomainKeyRef:  "different-dk",
					Lifecycle:     shared.DesiredLifecycleActive,
				},
			},
			&operationsv1alpha1.DataEncryptionKey{
				ObjectMeta: metav1.ObjectMeta{Name: "dek-1a", Namespace: "default"},
				Spec: operationsv1alpha1.DataEncryptionKeySpec{
					TenantNameRef: "acc",
					ServiceKeyRef: "sk-1",
					Lifecycle:     shared.DesiredLifecycleActive,
				},
			},
			&operationsv1alpha1.DataEncryptionKey{
				ObjectMeta: metav1.ObjectMeta{Name: "dek-other", Namespace: "default"},
				Spec: operationsv1alpha1.DataEncryptionKeySpec{
					TenantNameRef: "acc",
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

	if err := cascadeDeactivateDomainKey(ctx, cl, "default", "dk-1"); err != nil {
		t.Fatalf("cascadeDeactivateDomainKey: %v", err)
	}
	if err := cascadeDeactivateServiceKey(ctx, cl, "default", "sk-1"); err != nil {
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
			if err := cl.Get(ctx, types.NamespacedName{Namespace: "default", Name: c.obj[3:]}, obj); err != nil {
				t.Fatalf("get %s: %v", c.obj, err)
			}
			lc = obj.Spec.Lifecycle
		case "dek":
			obj := &operationsv1alpha1.DataEncryptionKey{}
			if err := cl.Get(ctx, types.NamespacedName{Namespace: "default", Name: c.obj[4:]}, obj); err != nil {
				t.Fatalf("get %s: %v", c.obj, err)
			}
			lc = obj.Spec.Lifecycle
		}
		if lc != c.want {
			t.Fatalf("%s lifecycle = %q, want %q", c.obj, lc, c.want)
		}
	}
}
