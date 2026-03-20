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

// MigrationType defines whether the migration is cold or warm.
// +kubebuilder:validation:Enum=cold;warm
type MigrationType string

const (
	MigrationTypeCold MigrationType = "cold"
	MigrationTypeWarm MigrationType = "warm"
)

// TargetPowerState defines the desired power state of the migrated VM.
// +kubebuilder:validation:Enum=Running;Stopped
type TargetPowerState string

const (
	TargetPowerStateRunning TargetPowerState = "Running"
	TargetPowerStateStopped TargetPowerState = "Stopped"
)

// PlanVM identifies a VM to include in a migration plan.
type PlanVM struct {
	// ID is the UUID of the Nutanix VM.
	// +kubebuilder:validation:Required
	ID string `json:"id"`

	// Name is the display name of the Nutanix VM (for readability).
	// +optional
	Name string `json:"name,omitempty"`

	// Hooks lists Hook CRs to run for this VM.
	// +optional
	Hooks []PlanHookRef `json:"hooks,omitempty"`
}

// PlanHookRef references a Hook CR and specifies when to run it.
type PlanHookRef struct {
	// HookRef references the Hook CR.
	// +kubebuilder:validation:Required
	HookRef corev1.LocalObjectReference `json:"hookRef"`

	// Step specifies when to run the hook: "PreHook" or "PostHook".
	// +kubebuilder:validation:Enum=PreHook;PostHook
	Step string `json:"step"`
}

// WarmConfig defines configuration for warm migration.
type WarmConfig struct {
	// PrecopyInterval is how often to run incremental syncs (e.g. "30m").
	// Defaults to "30m".
	// +optional
	// +kubebuilder:default="30m"
	PrecopyInterval string `json:"precopyInterval,omitempty"`

	// MaxPrecopyRounds limits the number of precopy rounds before requiring cutover.
	// Defaults to 10.
	// +optional
	// +kubebuilder:default=10
	MaxPrecopyRounds int `json:"maxPrecopyRounds,omitempty"`
}

// MigrationPlanSpec defines the desired state of MigrationPlan.
type MigrationPlanSpec struct {
	// ProviderRef references the NutanixProvider to use.
	// +kubebuilder:validation:Required
	ProviderRef corev1.LocalObjectReference `json:"providerRef"`

	// TargetNamespace is the Kubernetes namespace where migrated VMs will be created.
	// +kubebuilder:validation:Required
	TargetNamespace string `json:"targetNamespace"`

	// Type is the migration type: "cold" or "warm".
	// Defaults to "cold".
	// +optional
	// +kubebuilder:default="cold"
	Type MigrationType `json:"type,omitempty"`

	// NetworkMapRef references the NetworkMap to use.
	// +kubebuilder:validation:Required
	NetworkMapRef corev1.LocalObjectReference `json:"networkMapRef"`

	// StorageMapRef references the StorageMap to use.
	// +kubebuilder:validation:Required
	StorageMapRef corev1.LocalObjectReference `json:"storageMapRef"`

	// VMs is the list of VMs to include in this migration plan.
	// +kubebuilder:validation:MinItems=1
	VMs []PlanVM `json:"vms"`

	// MaxInFlight limits how many VMs can be migrated concurrently.
	// Defaults to 1.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	MaxInFlight int `json:"maxInFlight,omitempty"`

	// TargetPowerState defines whether migrated VMs should be started or left stopped.
	// Defaults to "Running".
	// +optional
	// +kubebuilder:default="Running"
	TargetPowerState TargetPowerState `json:"targetPowerState,omitempty"`

	// WarmConfig is configuration for warm migration. Only used when Type is "warm".
	// +optional
	WarmConfig *WarmConfig `json:"warmConfig,omitempty"`
}

// PlanPhase represents the current phase of a MigrationPlan.
// +kubebuilder:validation:Enum=Pending;Validating;Ready;Error
type PlanPhase string

const (
	PlanPhasePending    PlanPhase = "Pending"
	PlanPhaseValidating PlanPhase = "Validating"
	PlanPhaseReady      PlanPhase = "Ready"
	PlanPhaseError      PlanPhase = "Error"
)

// ConcernCategory classifies the severity of a validation concern.
// +kubebuilder:validation:Enum=Error;Warning;Info
type ConcernCategory string

const (
	ConcernCategoryError   ConcernCategory = "Error"
	ConcernCategoryWarning ConcernCategory = "Warning"
	ConcernCategoryInfo    ConcernCategory = "Info"
)

// Concern is a validation finding for a VM.
type Concern struct {
	// Category is the severity of the concern.
	Category ConcernCategory `json:"category"`

	// Message describes the concern.
	Message string `json:"message"`
}

// VMValidationStatus captures the validation result for a single VM in a plan.
type VMValidationStatus struct {
	// ID is the UUID of the Nutanix VM.
	ID string `json:"id"`

	// Name is the display name of the VM.
	// +optional
	Name string `json:"name,omitempty"`

	// Concerns lists validation findings for this VM.
	// +optional
	Concerns []Concern `json:"concerns,omitempty"`
}

// MigrationPlanStatus defines the observed state of MigrationPlan.
type MigrationPlanStatus struct {
	// Phase is the current lifecycle phase of the plan.
	// +optional
	Phase PlanPhase `json:"phase,omitempty"`

	// VMs is the per-VM validation status.
	// +optional
	VMs []VMValidationStatus `json:"vms,omitempty"`

	// Conditions represent the latest available observations of the plan's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="VMs",type=integer,JSONPath=`.spec.maxInFlight`,description="Max in-flight VMs"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MigrationPlan is the Schema for the migrationplans API.
type MigrationPlan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MigrationPlanSpec   `json:"spec,omitempty"`
	Status MigrationPlanStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MigrationPlanList contains a list of MigrationPlan.
type MigrationPlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MigrationPlan `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MigrationPlan{}, &MigrationPlanList{})
}
