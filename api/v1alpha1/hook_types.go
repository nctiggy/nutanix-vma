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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HookSpec defines the desired state of Hook.
type HookSpec struct {
	// Image is the container image to run for this hook (e.g. "quay.io/org/hook:latest").
	// +kubebuilder:validation:Required
	Image string `json:"image"`

	// Playbook is a base64-encoded Ansible playbook or shell script to execute.
	// Mounted into the hook container at /tmp/hook/playbook.
	// +optional
	Playbook string `json:"playbook,omitempty"`

	// Deadline is the maximum duration the hook Job is allowed to run (e.g. "5m", "1h").
	// Defaults to "10m".
	// +optional
	// +kubebuilder:default="10m"
	Deadline string `json:"deadline,omitempty"`

	// ServiceAccount is the Kubernetes ServiceAccount to use for the hook Job.
	// Defaults to "default".
	// +optional
	// +kubebuilder:default="default"
	ServiceAccount string `json:"serviceAccount,omitempty"`
}

// HookStatus defines the observed state of Hook.
type HookStatus struct {
	// Conditions represent the latest available observations of the hook's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Deadline",type=string,JSONPath=`.spec.deadline`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Hook is the Schema for the hooks API.
type Hook struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HookSpec   `json:"spec,omitempty"`
	Status HookStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HookList contains a list of Hook.
type HookList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Hook `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Hook{}, &HookList{})
}
