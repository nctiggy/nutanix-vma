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

package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

const (
	migrationFinalizer         = "vma.nutanix.io/migration-protection"
	conditionTypeMigrationDone = "Ready"

	migrationRequeueInterval = 5 * time.Second
)

// phaseOrder defines the pipeline phase sequence for cold migration.
var phaseOrder = []vmav1alpha1.VMMigrationPhase{
	vmav1alpha1.VMPhasePending,
	vmav1alpha1.VMPhasePreHook,
	vmav1alpha1.VMPhaseStorePowerState,
	vmav1alpha1.VMPhasePowerOff,
	vmav1alpha1.VMPhaseWaitForPowerOff,
	vmav1alpha1.VMPhaseCreateSnapshot,
	vmav1alpha1.VMPhaseExportDisks,
	vmav1alpha1.VMPhaseImportDisks,
	vmav1alpha1.VMPhaseCreateVM,
	vmav1alpha1.VMPhaseStartVM,
	vmav1alpha1.VMPhasePostHook,
	vmav1alpha1.VMPhaseCleanup,
	vmav1alpha1.VMPhaseCompleted,
}

// PhaseResult represents the outcome of executing a migration phase.
type PhaseResult struct {
	// Completed indicates the phase finished successfully.
	Completed bool
	// Error indicates the phase failed.
	Error error
}

// MigrationReconciler reconciles Migration objects.
type MigrationReconciler struct {
	client.Client
	ClientFactory NutanixClientFactory
}

// SetupMigrationController registers the Migration reconciler with the manager.
func SetupMigrationController(mgr ctrl.Manager) error {
	return (&MigrationReconciler{
		Client:        mgr.GetClient(),
		ClientFactory: nutanix.NewClient,
	}).SetupWithManager(mgr)
}

// +kubebuilder:rbac:groups=vma.nutanix.io,resources=migrations,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=migrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=migrations/finalizers,verbs=update

// Reconcile handles Migration reconciliation.
func (r *MigrationReconciler) Reconcile(
	ctx context.Context, req ctrl.Request,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the Migration
	migration := &vmav1alpha1.Migration{}
	if err := r.Get(ctx, req.NamespacedName, migration); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !migration.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, migration)
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(migration, migrationFinalizer) {
		controllerutil.AddFinalizer(migration, migrationFinalizer)
		if err := r.Update(ctx, migration); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Skip if already in terminal state
	if isTerminalMigrationPhase(migration.Status.Phase) {
		return ctrl.Result{}, nil
	}

	// Resolve MigrationPlan
	plan := &vmav1alpha1.MigrationPlan{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      migration.Spec.PlanRef.Name,
		Namespace: migration.Namespace,
	}, plan); err != nil {
		logger.Error(err, "Failed to resolve MigrationPlan")
		return r.setMigrationFailed(ctx, migration,
			fmt.Sprintf("MigrationPlan %q not found: %v",
				migration.Spec.PlanRef.Name, err))
	}

	// Initialize VM statuses on first reconcile
	if migration.Status.Phase == "" ||
		migration.Status.Phase == vmav1alpha1.MigrationPhasePending {
		r.initializeVMStatuses(migration, plan)
		migration.Status.Phase = vmav1alpha1.MigrationPhaseRunning
		now := metav1.Now()
		migration.Status.Started = &now
		meta.SetStatusCondition(&migration.Status.Conditions,
			metav1.Condition{
				Type:               conditionTypeMigrationDone,
				Status:             metav1.ConditionFalse,
				Reason:             "Running",
				Message:            "Migration is in progress",
				ObservedGeneration: migration.Generation,
			})
	}

	// Handle cancellations
	r.processCancellations(migration)

	// Schedule and advance VMs
	maxInFlight := max(plan.Spec.MaxInFlight, 1)
	needsRequeue := r.advanceVMs(ctx, migration, maxInFlight)

	// Update overall migration status
	r.updateOverallStatus(migration)

	if err := r.Status().Update(ctx, migration); err != nil {
		return ctrl.Result{}, err
	}

	if needsRequeue {
		logger.Info("Migration in progress, requeuing",
			"phase", migration.Status.Phase)
		return ctrl.Result{RequeueAfter: migrationRequeueInterval}, nil
	}

	logger.Info("Migration reconciliation complete",
		"phase", migration.Status.Phase)
	return ctrl.Result{}, nil
}

// initializeVMStatuses creates per-VM status entries from the Plan.
// Idempotent: skips VMs that already have status entries.
func (r *MigrationReconciler) initializeVMStatuses(
	migration *vmav1alpha1.Migration,
	plan *vmav1alpha1.MigrationPlan,
) {
	existing := make(map[string]bool, len(migration.Status.VMs))
	for _, vm := range migration.Status.VMs {
		existing[vm.ID] = true
	}

	for _, planVM := range plan.Spec.VMs {
		if existing[planVM.ID] {
			continue
		}
		migration.Status.VMs = append(migration.Status.VMs,
			vmav1alpha1.VMMigrationStatus{
				ID:    planVM.ID,
				Name:  planVM.Name,
				Phase: vmav1alpha1.VMPhasePending,
			})
	}
}

// processCancellations marks VMs in the cancel list as Cancelled.
func (r *MigrationReconciler) processCancellations(
	migration *vmav1alpha1.Migration,
) {
	if len(migration.Spec.Cancel) == 0 {
		return
	}

	cancelSet := make(map[string]bool, len(migration.Spec.Cancel))
	for _, id := range migration.Spec.Cancel {
		cancelSet[id] = true
	}

	for i := range migration.Status.VMs {
		vm := &migration.Status.VMs[i]
		if cancelSet[vm.ID] && !isTerminalVMPhase(vm.Phase) {
			vm.Phase = vmav1alpha1.VMPhaseCancelled
			now := metav1.Now()
			vm.Completed = &now
		}
	}
}

// advanceVMs processes VMs through the pipeline respecting MaxInFlight.
// Returns true if any VM still needs processing (requeue needed).
func (r *MigrationReconciler) advanceVMs(
	ctx context.Context,
	migration *vmav1alpha1.Migration,
	maxInFlight int,
) bool {
	needsRequeue := false

	// Count currently active VMs (past Pending, not terminal)
	activeCount := 0
	for _, vm := range migration.Status.VMs {
		if isActiveVMPhase(vm.Phase) {
			activeCount++
		}
	}

	for i := range migration.Status.VMs {
		vm := &migration.Status.VMs[i]
		if isTerminalVMPhase(vm.Phase) {
			continue
		}

		// Start Pending VMs if under MaxInFlight limit
		if vm.Phase == vmav1alpha1.VMPhasePending {
			if activeCount >= maxInFlight {
				needsRequeue = true
				continue
			}
			now := metav1.Now()
			vm.Started = &now
			vm.Phase = vmav1alpha1.VMPhasePreHook
			activeCount++
		}

		// Advance active VM through phases
		if r.advanceSingleVM(ctx, vm) {
			needsRequeue = true
		}
	}

	return needsRequeue
}

// advanceSingleVM advances a single VM through the phase pipeline.
// Returns true if the VM needs further processing (requeue).
func (r *MigrationReconciler) advanceSingleVM(
	_ context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
) bool {
	for {
		if isTerminalVMPhase(vmStatus.Phase) {
			return false
		}

		result := r.executePhase(vmStatus)
		if result.Error != nil {
			vmStatus.Phase = vmav1alpha1.VMPhaseFailed
			vmStatus.Error = result.Error.Error()
			now := metav1.Now()
			vmStatus.Completed = &now
			return false
		}
		if !result.Completed {
			return true
		}

		// Advance to next phase
		next := nextPhase(vmStatus.Phase)
		vmStatus.Phase = next
		if next == vmav1alpha1.VMPhaseCompleted {
			now := metav1.Now()
			vmStatus.Completed = &now
			return false
		}
	}
}

// executePhase runs the current phase for a VM.
// All phases are stubs in US-013a -- real implementations in US-013b/c.
func (r *MigrationReconciler) executePhase(
	_ *vmav1alpha1.VMMigrationStatus,
) PhaseResult {
	return PhaseResult{Completed: true}
}

// updateOverallStatus derives the overall migration phase from per-VM statuses.
func (r *MigrationReconciler) updateOverallStatus(
	migration *vmav1alpha1.Migration,
) {
	var completed, failed, cancelled int
	total := len(migration.Status.VMs)

	for _, vm := range migration.Status.VMs {
		switch vm.Phase {
		case vmav1alpha1.VMPhaseCompleted:
			completed++
		case vmav1alpha1.VMPhaseFailed:
			failed++
		case vmav1alpha1.VMPhaseCancelled:
			cancelled++
		}
	}

	allDone := (completed + failed + cancelled) == total
	if !allDone {
		migration.Status.Phase = vmav1alpha1.MigrationPhaseRunning
		return
	}

	now := metav1.Now()
	migration.Status.Completed = &now

	if cancelled == total {
		migration.Status.Phase = vmav1alpha1.MigrationPhaseCancelled
		meta.SetStatusCondition(&migration.Status.Conditions,
			metav1.Condition{
				Type:               conditionTypeMigrationDone,
				Status:             metav1.ConditionFalse,
				Reason:             "Cancelled",
				Message:            "All VMs were cancelled",
				ObservedGeneration: migration.Generation,
			})
		return
	}

	if failed > 0 {
		migration.Status.Phase = vmav1alpha1.MigrationPhaseFailed
		meta.SetStatusCondition(&migration.Status.Conditions,
			metav1.Condition{
				Type:   conditionTypeMigrationDone,
				Status: metav1.ConditionFalse,
				Reason: "Failed",
				Message: fmt.Sprintf(
					"%d of %d VMs failed", failed, total),
				ObservedGeneration: migration.Generation,
			})
		return
	}

	migration.Status.Phase = vmav1alpha1.MigrationPhaseCompleted
	meta.SetStatusCondition(&migration.Status.Conditions,
		metav1.Condition{
			Type:   conditionTypeMigrationDone,
			Status: metav1.ConditionTrue,
			Reason: "Completed",
			Message: fmt.Sprintf(
				"All %d VMs migrated successfully", total),
			ObservedGeneration: migration.Generation,
		})
}

// handleDeletion manages Migration deletion with finalizer.
func (r *MigrationReconciler) handleDeletion(
	ctx context.Context,
	migration *vmav1alpha1.Migration,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(migration, migrationFinalizer) {
		return ctrl.Result{}, nil
	}

	// Block deletion while migration is running
	if migration.Status.Phase == vmav1alpha1.MigrationPhaseRunning {
		log.FromContext(ctx).Info(
			"Migration is still running, cannot remove finalizer")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	controllerutil.RemoveFinalizer(migration, migrationFinalizer)
	if err := r.Update(ctx, migration); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// setMigrationFailed sets the migration to Failed with an error condition.
func (r *MigrationReconciler) setMigrationFailed(
	ctx context.Context,
	migration *vmav1alpha1.Migration,
	message string,
) (ctrl.Result, error) {
	migration.Status.Phase = vmav1alpha1.MigrationPhaseFailed
	now := metav1.Now()
	migration.Status.Completed = &now

	meta.SetStatusCondition(&migration.Status.Conditions,
		metav1.Condition{
			Type:               conditionTypeMigrationDone,
			Status:             metav1.ConditionFalse,
			Reason:             "Error",
			Message:            message,
			ObservedGeneration: migration.Generation,
		})

	if err := r.Status().Update(ctx, migration); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// nextPhase returns the next phase in the pipeline sequence.
func nextPhase(
	current vmav1alpha1.VMMigrationPhase,
) vmav1alpha1.VMMigrationPhase {
	for i, p := range phaseOrder {
		if p == current && i+1 < len(phaseOrder) {
			return phaseOrder[i+1]
		}
	}
	return current
}

// isTerminalVMPhase returns true for Completed, Failed, Cancelled.
func isTerminalVMPhase(phase vmav1alpha1.VMMigrationPhase) bool {
	return phase == vmav1alpha1.VMPhaseCompleted ||
		phase == vmav1alpha1.VMPhaseFailed ||
		phase == vmav1alpha1.VMPhaseCancelled
}

// isTerminalMigrationPhase returns true for terminal overall phases.
func isTerminalMigrationPhase(phase vmav1alpha1.MigrationPhase) bool {
	return phase == vmav1alpha1.MigrationPhaseCompleted ||
		phase == vmav1alpha1.MigrationPhaseFailed ||
		phase == vmav1alpha1.MigrationPhaseCancelled
}

// isActiveVMPhase returns true for VMs past Pending but not terminal.
func isActiveVMPhase(phase vmav1alpha1.VMMigrationPhase) bool {
	return phase != vmav1alpha1.VMPhasePending &&
		!isTerminalVMPhase(phase)
}

// SetupWithManager sets up the controller with the Manager.
func (r *MigrationReconciler) SetupWithManager(
	mgr ctrl.Manager,
) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vmav1alpha1.Migration{}).
		Named("migration").
		Complete(r)
}
