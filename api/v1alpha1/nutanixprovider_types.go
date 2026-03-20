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

// NutanixProviderSpec defines the desired state of NutanixProvider.
type NutanixProviderSpec struct {
	// URL is the Prism Central endpoint (e.g. https://prism.example.com:9440).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`

	// SecretRef references a Secret containing "username" and "password" keys.
	// +kubebuilder:validation:Required
	SecretRef corev1.LocalObjectReference `json:"secretRef"`

	// RefreshInterval is how often to re-sync inventory (e.g. "5m", "1h").
	// Defaults to "5m".
	// +optional
	// +kubebuilder:default="5m"
	RefreshInterval string `json:"refreshInterval,omitempty"`

	// InsecureSkipVerify disables TLS certificate verification.
	// +optional
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

// ProviderPhase represents the current phase of a NutanixProvider.
// +kubebuilder:validation:Enum=Pending;Connecting;Connected;Error
type ProviderPhase string

const (
	ProviderPhasePending    ProviderPhase = "Pending"
	ProviderPhaseConnecting ProviderPhase = "Connecting"
	ProviderPhaseConnected  ProviderPhase = "Connected"
	ProviderPhaseError      ProviderPhase = "Error"
)

// NutanixProviderStatus defines the observed state of NutanixProvider.
type NutanixProviderStatus struct {
	// Phase is the current lifecycle phase of the provider.
	// +optional
	Phase ProviderPhase `json:"phase,omitempty"`

	// VMCount is the number of VMs discovered in this Nutanix environment.
	// +optional
	VMCount int `json:"vmCount,omitempty"`

	// Conditions represent the latest available observations of the provider's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.spec.url`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="VMs",type=integer,JSONPath=`.status.vmCount`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// NutanixProvider is the Schema for the nutanixproviders API.
type NutanixProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NutanixProviderSpec   `json:"spec,omitempty"`
	Status NutanixProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NutanixProviderList contains a list of NutanixProvider.
type NutanixProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NutanixProvider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NutanixProvider{}, &NutanixProviderList{})
}
