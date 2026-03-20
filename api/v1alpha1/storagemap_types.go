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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StorageSource identifies a Nutanix storage container by ID and/or name.
type StorageSource struct {
	// ID is the UUID of the Nutanix storage container.
	// +optional
	ID string `json:"id,omitempty"`

	// Name is the name of the Nutanix storage container.
	// +optional
	Name string `json:"name,omitempty"`
}

// StorageDestination describes the KubeVirt target storage.
type StorageDestination struct {
	// StorageClass is the Kubernetes StorageClass to use for the PVC.
	// +kubebuilder:validation:Required
	StorageClass string `json:"storageClass"`

	// VolumeMode specifies whether the PVC should be Filesystem or Block.
	// Defaults to Filesystem.
	// +optional
	// +kubebuilder:default="Filesystem"
	// +kubebuilder:validation:Enum=Filesystem;Block
	VolumeMode corev1.PersistentVolumeMode `json:"volumeMode,omitempty"`

	// AccessMode specifies the access mode for the PVC.
	// Defaults to ReadWriteOnce.
	// +optional
	// +kubebuilder:default="ReadWriteOnce"
	// +kubebuilder:validation:Enum=ReadWriteOnce;ReadWriteMany;ReadOnlyMany
	AccessMode corev1.PersistentVolumeAccessMode `json:"accessMode,omitempty"`
}

// StoragePair maps a Nutanix storage container to a Kubernetes StorageClass.
type StoragePair struct {
	// Source is the Nutanix storage container.
	// +kubebuilder:validation:Required
	Source StorageSource `json:"source"`

	// Destination is the KubeVirt storage target.
	// +kubebuilder:validation:Required
	Destination StorageDestination `json:"destination"`
}

// StorageMapSpec defines the desired state of StorageMap.
type StorageMapSpec struct {
	// ProviderRef references the NutanixProvider this map applies to.
	// +kubebuilder:validation:Required
	ProviderRef corev1.LocalObjectReference `json:"providerRef"`

	// Map is the list of storage mappings from Nutanix containers to Kubernetes StorageClasses.
	// +kubebuilder:validation:MinItems=1
	Map []StoragePair `json:"map"`
}

// +kubebuilder:object:root=true

// StorageMap is the Schema for the storagemaps API.
type StorageMap struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec StorageMapSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// StorageMapList contains a list of StorageMap.
type StorageMapList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StorageMap `json:"items"`
}

func init() {
	SchemeBuilder.Register(&StorageMap{}, &StorageMapList{})
}
