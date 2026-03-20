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

// NetworkDestinationType defines the type of target network.
// +kubebuilder:validation:Enum=pod;multus
type NetworkDestinationType string

const (
	NetworkDestinationPod    NetworkDestinationType = "pod"
	NetworkDestinationMultus NetworkDestinationType = "multus"
)

// NetworkSource identifies a Nutanix subnet by ID and/or name.
type NetworkSource struct {
	// ID is the UUID of the Nutanix subnet.
	// +optional
	ID string `json:"id,omitempty"`

	// Name is the name of the Nutanix subnet.
	// +optional
	Name string `json:"name,omitempty"`
}

// NetworkDestination describes the KubeVirt target network.
type NetworkDestination struct {
	// Type is the network type: "pod" for the default pod network, "multus" for a Multus NAD.
	// +kubebuilder:validation:Required
	Type NetworkDestinationType `json:"type"`

	// Name is the name of the Multus NetworkAttachmentDefinition.
	// Required when Type is "multus".
	// +optional
	Name string `json:"name,omitempty"`

	// Namespace is the namespace of the Multus NetworkAttachmentDefinition.
	// Defaults to the migration target namespace if not specified.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// NetworkPair maps a Nutanix subnet to a KubeVirt network.
type NetworkPair struct {
	// Source is the Nutanix subnet.
	// +kubebuilder:validation:Required
	Source NetworkSource `json:"source"`

	// Destination is the KubeVirt network target.
	// +kubebuilder:validation:Required
	Destination NetworkDestination `json:"destination"`
}

// NetworkMapSpec defines the desired state of NetworkMap.
type NetworkMapSpec struct {
	// ProviderRef references the NutanixProvider this map applies to.
	// +kubebuilder:validation:Required
	ProviderRef corev1.LocalObjectReference `json:"providerRef"`

	// Map is the list of network mappings from Nutanix subnets to KubeVirt networks.
	// +kubebuilder:validation:MinItems=1
	Map []NetworkPair `json:"map"`
}

// +kubebuilder:object:root=true

// NetworkMap is the Schema for the networkmaps API.
type NetworkMap struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec NetworkMapSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkMapList contains a list of NetworkMap.
type NetworkMapList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkMap `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NetworkMap{}, &NetworkMapList{})
}
