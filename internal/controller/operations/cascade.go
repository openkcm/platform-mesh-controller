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

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
)

func ensureParentDomainKey(ctx context.Context, cl client.Client, namespace, fallbackName, accountName string) (string, error) {
	dks := &operationsv1alpha1.DomainKeyList{}
	if err := cl.List(ctx, dks, client.InNamespace(namespace)); err != nil {
		return "", err
	}
	var winner *operationsv1alpha1.DomainKey
	for i := range dks.Items {
		dk := &dks.Items[i]
		if !dk.DeletionTimestamp.IsZero() {
			continue
		}
		if winner == nil || dk.CreationTimestamp.Before(&winner.CreationTimestamp) ||
			(dk.CreationTimestamp.Equal(&winner.CreationTimestamp) && dk.Name < winner.Name) {
			winner = dk
		}
	}
	if winner != nil {
		return winner.Name, nil
	}

	dk := &operationsv1alpha1.DomainKey{}
	dk.Name = fallbackName
	dk.Namespace = namespace
	dk.Annotations = map[string]string{bootstrapAnnotation: bootstrapAnnotationAuto}
	dk.Spec = operationsv1alpha1.DomainKeySpec{
		Type:          domainKeyTypeTeam,
		TenantNameRef: accountName,
	}
	if err := cl.Create(ctx, dk); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", err
	}
	return fallbackName, nil
}

func ensureServiceKey(ctx context.Context, cl client.Client, namespace, name, accountName string) error {
	existing := &operationsv1alpha1.ServiceKey{}
	err := cl.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	sk := &operationsv1alpha1.ServiceKey{}
	sk.Name = name
	sk.Namespace = namespace
	sk.Annotations = map[string]string{bootstrapAnnotation: bootstrapAnnotationAuto}
	sk.Spec = operationsv1alpha1.ServiceKeySpec{TenantNameRef: accountName}
	if err := cl.Create(ctx, sk); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}
