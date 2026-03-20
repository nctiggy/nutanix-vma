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

package transfer

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

const (
	// SecretKeyAccessKeyID is the CDI HTTP credential key for username.
	SecretKeyAccessKeyID = "accessKeyId"
	// SecretKeySecretKey is the CDI HTTP credential key for password.
	SecretKeySecretKey = "secretKey"
	// ConfigMapKeyCA is the key for the CA certificate in a ConfigMap.
	ConfigMapKeyCA = "ca.pem"

	imageDownloadPath = "/api/vmm/v4.0/images/%s/file"
)

// Manager handles disk transfer lifecycle: credential secrets,
// CA ConfigMaps, and CDI DataVolumes.
type Manager struct {
	Client   client.Client
	PrismURL string
	Username string
	Password string
	CACert   []byte
	Insecure bool
}

// CreateCredentialSecret creates the Basic Auth secret for CDI HTTP source.
// The secret uses accessKeyId/secretKey format as expected by CDI.
// Idempotent: returns nil if the secret already exists.
func (m *Manager) CreateCredentialSecret(
	ctx context.Context,
	name, namespace string,
	ownerRef metav1.OwnerReference,
) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Data: map[string][]byte{
			SecretKeyAccessKeyID: []byte(m.Username),
			SecretKeySecretKey:   []byte(m.Password),
		},
	}
	err := m.Client.Create(ctx, secret)
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// CreateCAConfigMap creates a ConfigMap with the CA certificate for CDI.
// Idempotent: returns nil if the ConfigMap already exists.
// Returns nil if there is no CA certificate configured.
func (m *Manager) CreateCAConfigMap(
	ctx context.Context,
	name, namespace string,
	ownerRef metav1.OwnerReference,
) error {
	if len(m.CACert) == 0 {
		return nil
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Data: map[string]string{
			ConfigMapKeyCA: string(m.CACert),
		},
	}
	err := m.Client.Create(ctx, cm)
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// ImageDownloadURL constructs the Prism image download URL for an image UUID.
func (m *Manager) ImageDownloadURL(imageUUID string) string {
	return strings.TrimRight(m.PrismURL, "/") +
		fmt.Sprintf(imageDownloadPath, imageUUID)
}

// FindStorageMapping finds the storage destination for a disk based on
// its storage container reference and the StorageMap.
func FindStorageMapping(
	disk *nutanix.Disk,
	storageMap *vmav1alpha1.StorageMap,
) *vmav1alpha1.StorageDestination {
	if disk.BackingInfo == nil || disk.BackingInfo.StorageContainerRef == nil {
		return nil
	}
	ref := disk.BackingInfo.StorageContainerRef
	for _, pair := range storageMap.Spec.Map {
		if pair.Source.ID != "" && pair.Source.ID == ref.ExtID {
			return &pair.Destination
		}
		if pair.Source.Name != "" && pair.Source.Name == ref.Name {
			return &pair.Destination
		}
	}
	return nil
}

// CreateDataVolume creates a CDI DataVolume for a disk image import.
// Idempotent: returns nil if the DataVolume already exists.
func (m *Manager) CreateDataVolume(
	ctx context.Context,
	opts DataVolumeOptions,
) error {
	dv := BuildDataVolume(opts)
	err := m.Client.Create(ctx, dv)
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// DataVolumeProgress represents the current state of a DataVolume import.
type DataVolumeProgress struct {
	Phase    cdiv1beta1.DataVolumePhase
	Progress string
}

// GetDataVolumeProgress returns the phase and progress of a DataVolume.
func (m *Manager) GetDataVolumeProgress(
	ctx context.Context,
	name, namespace string,
) (*DataVolumeProgress, error) {
	dv := &cdiv1beta1.DataVolume{}
	if err := m.Client.Get(ctx, types.NamespacedName{
		Name: name, Namespace: namespace,
	}, dv); err != nil {
		return nil, err
	}
	return &DataVolumeProgress{
		Phase:    dv.Status.Phase,
		Progress: string(dv.Status.Progress),
	}, nil
}

// DeleteCredentialSecret deletes the credential secret. Idempotent.
func (m *Manager) DeleteCredentialSecret(
	ctx context.Context, name, namespace string,
) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	err := m.Client.Delete(ctx, secret)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// DeleteCAConfigMap deletes the CA ConfigMap. Idempotent.
func (m *Manager) DeleteCAConfigMap(
	ctx context.Context, name, namespace string,
) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	err := m.Client.Delete(ctx, cm)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
