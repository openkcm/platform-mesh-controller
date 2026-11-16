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
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operationsv1alpha1 "github.com/openkcm/platform-mesh-controller/api/operations/v1alpha1"
)

const (
	awsCredentialsSecretPrefix = "awsrootkey-"
	rootKeyCredentialsLabel    = "openkcm.io/root-key-credentials"
	awsAccessKeyIDEntry        = "accessKeyId"
	awsSecretAccessKeyEntry    = "secretAccessKey"
)

const (
	reasonCredentialsPending   = "CredentialsPending"
	reasonCredentialsInvalid   = "CredentialsInvalid"
	reasonCredentialsForbidden = "CredentialsForbidden"
)

var errAWSCredentialsInvalid = errors.New("invalid AWS credentials Secret")

type awsAccessKey struct {
	ID     string
	Secret string
}

func (awsAccessKey) String() string   { return "awsAccessKey{redacted}" }
func (awsAccessKey) GoString() string { return "awsAccessKey{redacted}" }

func awsCredentialsSecretName(rk *operationsv1alpha1.AWSRootKey) string {
	return awsCredentialsSecretPrefix + rk.Name
}

func awsAccessKeyFromSecret(rk *operationsv1alpha1.AWSRootKey, s *corev1.Secret) (awsAccessKey, error) {
	if expectedName := awsCredentialsSecretName(rk); s.Name != expectedName || s.Namespace != rk.Namespace {
		return awsAccessKey{}, fmt.Errorf("%w: %s/%s is not %s/%s",
			errAWSCredentialsInvalid, s.Namespace, s.Name, rk.Namespace, expectedName)
	}
	if s.Labels[rootKeyCredentialsLabel] != "true" {
		return awsAccessKey{}, fmt.Errorf("%w: %s/%s lacks the label %s=true",
			errAWSCredentialsInvalid, s.Namespace, s.Name, rootKeyCredentialsLabel)
	}
	if !isSecretOwnedByAWSRootKey(s, rk) {
		return awsAccessKey{}, fmt.Errorf("%w: %s/%s is not owned by AWSRootKey %s (uid %s)",
			errAWSCredentialsInvalid, s.Namespace, s.Name, rk.Name, rk.UID)
	}
	key := awsAccessKey{
		ID:     string(s.Data[awsAccessKeyIDEntry]),
		Secret: string(s.Data[awsSecretAccessKeyEntry]),
	}
	if key.ID == "" {
		return awsAccessKey{}, fmt.Errorf("%w: %s/%s has no %s",
			errAWSCredentialsInvalid, s.Namespace, s.Name, awsAccessKeyIDEntry)
	}
	if key.Secret == "" {
		return awsAccessKey{}, fmt.Errorf("%w: %s/%s has no %s",
			errAWSCredentialsInvalid, s.Namespace, s.Name, awsSecretAccessKeyEntry)
	}
	return key, nil
}

func isSecretOwnedByAWSRootKey(s *corev1.Secret, rk *operationsv1alpha1.AWSRootKey) bool {
	for _, ref := range s.OwnerReferences {
		if ref.Kind == "AWSRootKey" && ref.UID == rk.UID {
			return true
		}
	}
	return false
}

func readAWSAccessKey(ctx context.Context, reader client.Reader, rk *operationsv1alpha1.AWSRootKey) (awsAccessKey, error) {
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: rk.Namespace, Name: awsCredentialsSecretName(rk)}
	if err := reader.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return awsAccessKey{}, fmt.Errorf("secret %s not found, or it lacks the label %s=true: %w",
				key.Name, rootKeyCredentialsLabel, err)
		}
		return awsAccessKey{}, err
	}
	return awsAccessKeyFromSecret(rk, secret)
}

func awsCredentialsFailureReason(err error) string {
	switch {
	case apierrors.IsNotFound(err):
		return reasonCredentialsPending
	case apierrors.IsForbidden(err):
		return reasonCredentialsForbidden
	case errors.Is(err, errAWSCredentialsInvalid):
		return reasonCredentialsInvalid
	}
	return ""
}
