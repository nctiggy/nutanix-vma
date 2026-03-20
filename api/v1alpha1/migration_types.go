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

// MigrationSpec defines the desired state of Migration.
type MigrationSpec struct {
	// PlanRef references the MigrationPlan this migration executes.
	// +kubebuilder:validation:Required
	PlanRef corev1.LocalObjectReference `json:"planRef"`

	// Cutover is the timestamp at which to trigger cutover for warm migrations.
	// For cold migrations, this field is ignored.
	// +optional
	Cutover *metav1.Time `json:"cutover,omitempty"`

	// Cancel is a list of VM UUIDs to cancel migration for.
	// +optional
	Cancel []string `json:"cancel,omitempty"`
}

// MigrationPhase represents the overall phase of a Migration.
// +kubebuilder:validation:Enum=Pending;Running;Completed;Failed;Cancelled
type MigrationPhase string

const (
	MigrationPhasePending   MigrationPhase = "Pending"
	MigrationPhaseRunning   MigrationPhase = "Running"
	MigrationPhaseCompleted MigrationPhase = "Completed"
	MigrationPhaseFailed    MigrationPhase = "Failed"
	MigrationPhaseCancelled MigrationPhase = "Cancelled"
)

// VMMigrationPhase represents the phase of a single VM's migration pipeline.
// +kubebuilder:validation:Enum=Pending;PreHook;StorePowerState;PowerOff;WaitForPowerOff;CreateSnapshot;ExportDisks;ImportDisks;CreateVM;StartVM;PostHook;Cleanup;Completed;Failed;Cancelled
type VMMigrationPhase string

const (
	VMPhasePending         VMMigrationPhase = "Pending"
	VMPhasePreHook         VMMigrationPhase = "PreHook"
	VMPhaseStorePowerState VMMigrationPhase = "StorePowerState"
	VMPhasePowerOff        VMMigrationPhase = "PowerOff"
	VMPhaseWaitForPowerOff VMMigrationPhase = "WaitForPowerOff"
	VMPhaseCreateSnapshot  VMMigrationPhase = "CreateSnapshot"
	VMPhaseExportDisks     VMMigrationPhase = "ExportDisks"
	VMPhaseImportDisks     VMMigrationPhase = "ImportDisks"
	VMPhaseCreateVM        VMMigrationPhase = "CreateVM"
	VMPhaseStartVM         VMMigrationPhase = "StartVM"
	VMPhasePostHook        VMMigrationPhase = "PostHook"
	VMPhaseCleanup         VMMigrationPhase = "Cleanup"
	VMPhaseCompleted       VMMigrationPhase = "Completed"
	VMPhaseFailed          VMMigrationPhase = "Failed"
	VMPhaseCancelled       VMMigrationPhase = "Cancelled"
)

// WarmMigrationStatus tracks warm migration-specific progress.
type WarmMigrationStatus struct {
	// PrecopyRounds is the number of precopy rounds completed.
	// +optional
	PrecopyRounds int `json:"precopyRounds,omitempty"`

	// CumulativeBytes is the total bytes transferred across all rounds.
	// +optional
	CumulativeBytes int64 `json:"cumulativeBytes,omitempty"`

	// LastDeltaBytes is the size of the last delta transfer.
	// +optional
	LastDeltaBytes int64 `json:"lastDeltaBytes,omitempty"`
}

// VMMigrationStatus captures the migration state for a single VM.
type VMMigrationStatus struct {
	// ID is the UUID of the Nutanix VM.
	ID string `json:"id"`

	// Name is the display name of the VM.
	// +optional
	Name string `json:"name,omitempty"`

	// Phase is the current phase in the migration pipeline for this VM.
	// +optional
	Phase VMMigrationPhase `json:"phase,omitempty"`

	// Started is the timestamp when migration of this VM began.
	// +optional
	Started *metav1.Time `json:"started,omitempty"`

	// Completed is the timestamp when migration of this VM finished.
	// +optional
	Completed *metav1.Time `json:"completed,omitempty"`

	// Error contains any error message from a failed phase.
	// +optional
	Error string `json:"error,omitempty"`

	// SnapshotUUID is the UUID of the Nutanix recovery point created for this VM.
	// Used for idempotent reconciliation.
	// +optional
	SnapshotUUID string `json:"snapshotUUID,omitempty"`

	// ImageUUIDs is the list of Nutanix image UUIDs created from snapshot disks.
	// Used for idempotent reconciliation and cleanup.
	// +optional
	ImageUUIDs []string `json:"imageUUIDs,omitempty"`

	// TaskUUID is the UUID of the current in-progress Nutanix async task.
	// Used for idempotent reconciliation.
	// +optional
	TaskUUID string `json:"taskUUID,omitempty"`

	// DataVolumeNames is the list of CDI DataVolume names created for disk import.
	// Used for idempotent reconciliation and cleanup.
	// +optional
	DataVolumeNames []string `json:"dataVolumeNames,omitempty"`

	// OriginalPowerState records the VM's power state before migration started.
	// Used to restore state on failure.
	// +optional
	OriginalPowerState string `json:"originalPowerState,omitempty"`

	// Warm tracks warm migration-specific progress.
	// +optional
	Warm *WarmMigrationStatus `json:"warm,omitempty"`
}

// MigrationStatus defines the observed state of Migration.
type MigrationStatus struct {
	// Phase is the overall migration phase.
	// +optional
	Phase MigrationPhase `json:"phase,omitempty"`

	// Started is the timestamp when the migration began.
	// +optional
	Started *metav1.Time `json:"started,omitempty"`

	// Completed is the timestamp when the migration finished.
	// +optional
	Completed *metav1.Time `json:"completed,omitempty"`

	// VMs is the per-VM migration status.
	// +optional
	VMs []VMMigrationStatus `json:"vms,omitempty"`

	// Conditions represent the latest available observations of the migration's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Plan",type=string,JSONPath=`.spec.planRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Started",type=date,JSONPath=`.status.started`
// +kubebuilder:printcolumn:name="Completed",type=date,JSONPath=`.status.completed`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Migration is the Schema for the migrations API.
type Migration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MigrationSpec   `json:"spec,omitempty"`
	Status MigrationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MigrationList contains a list of Migration.
type MigrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Migration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Migration{}, &MigrationList{})
}
