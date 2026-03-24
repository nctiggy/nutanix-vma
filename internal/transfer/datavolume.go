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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

const (
	// gib is the number of bytes in one gibibyte.
	gib = 1024 * 1024 * 1024
)

// DataVolumeOptions configures CDI DataVolume creation.
type DataVolumeOptions struct {
	// Name is the DataVolume name.
	Name string

	// Namespace is the target namespace.
	Namespace string

	// ImageURL is the Prism image download URL.
	ImageURL string

	// DiskSizeBytes is the disk size for the PVC.
	DiskSizeBytes int64

	// StorageClass is the Kubernetes StorageClass name.
	StorageClass string

	// VolumeMode is the PVC volume mode (Filesystem or Block).
	VolumeMode corev1.PersistentVolumeMode

	// AccessMode is the PVC access mode.
	AccessMode corev1.PersistentVolumeAccessMode

	// SecretName is the credential secret name for HTTP auth.
	SecretName string

	// CertConfigMap is the CA certificate ConfigMap name (optional).
	CertConfigMap string

	// OwnerRef is the owner reference to set on the DataVolume.
	OwnerRef metav1.OwnerReference
}

// bytesToGiQuantity converts bytes to a Kubernetes resource.Quantity
// in Gi (gibibytes) format, rounding up to ensure sufficient storage.
// This ensures PVC storage requests use clean, human-readable values
// like "40Gi" instead of raw byte counts or millibyte notation.
func bytesToGiQuantity(bytes int64) resource.Quantity {
	// Convert bytes to GiB, rounding up to ensure we have enough space
	gibs := (bytes + gib - 1) / gib
	gibs = max(gibs, 1) // Minimum 1Gi
	return resource.MustParse(fmt.Sprintf("%dGi", gibs))
}

// BuildDataVolume creates a CDI DataVolume with an HTTP source pointing
// to a Prism image download endpoint.
func BuildDataVolume(opts DataVolumeOptions) *cdiv1beta1.DataVolume {
	storageClassName := opts.StorageClass
	volumeMode := opts.VolumeMode

	// Convert disk size from bytes to Gi for clean PVC storage requests
	storageQuantity := bytesToGiQuantity(opts.DiskSizeBytes)

	dv := &cdiv1beta1.DataVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:            opts.Name,
			Namespace:       opts.Namespace,
			OwnerReferences: []metav1.OwnerReference{opts.OwnerRef},
		},
		Spec: cdiv1beta1.DataVolumeSpec{
			Source: &cdiv1beta1.DataVolumeSource{
				HTTP: &cdiv1beta1.DataVolumeSourceHTTP{
					URL:       opts.ImageURL,
					SecretRef: opts.SecretName,
				},
			},
			PVC: &corev1.PersistentVolumeClaimSpec{
				AccessModes:      []corev1.PersistentVolumeAccessMode{opts.AccessMode},
				StorageClassName: &storageClassName,
				VolumeMode:       &volumeMode,
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: storageQuantity,
					},
				},
			},
		},
	}

	if opts.CertConfigMap != "" {
		dv.Spec.Source.HTTP.CertConfigMap = opts.CertConfigMap
	}

	return dv
}
