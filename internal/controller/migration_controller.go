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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/builder"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
	"github.com/nctiggy/nutanix-vma/internal/observability"
	"github.com/nctiggy/nutanix-vma/internal/transfer"
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

// warmPhaseOrder defines the pipeline phase sequence for warm migration.
var warmPhaseOrder = []vmav1alpha1.VMMigrationPhase{
	vmav1alpha1.VMPhasePending,
	vmav1alpha1.VMPhasePreHook,
	vmav1alpha1.VMPhaseStorePowerState,
	vmav1alpha1.VMPhaseBulkCopy,
	vmav1alpha1.VMPhaseWaitBulkCopy,
	vmav1alpha1.VMPhasePrecopy,
	vmav1alpha1.VMPhasePowerOff,
	vmav1alpha1.VMPhaseWaitForPowerOff,
	vmav1alpha1.VMPhaseFinalSync,
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

// migrationContext holds all resolved references needed to execute
// migration phases. Built once per reconcile.
type migrationContext struct {
	NutanixClient nutanix.NutanixClient
	Plan          *vmav1alpha1.MigrationPlan
	NetworkMap    *vmav1alpha1.NetworkMap
	StorageMap    *vmav1alpha1.StorageMap
	Provider      *vmav1alpha1.NutanixProvider
	Migration     *vmav1alpha1.Migration
	Secret        *corev1.Secret
	TransferMgr   *transfer.Manager
}

// MigrationReconciler reconciles Migration objects.
type MigrationReconciler struct {
	client.Client
	ClientFactory NutanixClientFactory
	Recorder      events.EventRecorder
}

// SetupMigrationController registers the Migration reconciler with the manager.
func SetupMigrationController(mgr ctrl.Manager) error {
	return (&MigrationReconciler{
		Client:        mgr.GetClient(),
		ClientFactory: nutanix.NewClient,
		Recorder: mgr.GetEventRecorder(
			"migration-controller"),
	}).SetupWithManager(mgr)
}

// +kubebuilder:rbac:groups=vma.nutanix.io,resources=migrations,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=migrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=migrations/finalizers,verbs=update
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=migrationplans,verbs=get
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=nutanixproviders,verbs=get
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=networkmaps,verbs=get
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=storagemaps,verbs=get
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;create;delete
// +kubebuilder:rbac:groups=cdi.kubevirt.io,resources=datavolumes,verbs=get;list;create;delete
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines,verbs=get;create;update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;create;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;create;delete
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=hooks,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

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

	// Resolve all references for phase execution
	mctx, err := r.resolveMigrationContext(ctx, migration, plan)
	if err != nil {
		logger.Error(err, "Failed to resolve migration context")
		return r.setMigrationFailed(ctx, migration,
			fmt.Sprintf("Failed to resolve references: %v", err))
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
		r.recordEvent(migration, corev1.EventTypeNormal,
			"MigrationStarted",
			fmt.Sprintf("Migration started with %d VMs",
				len(migration.Status.VMs)))
		observability.ActiveMigrations.Inc()
		logger.Info("Migration started",
			"migration", migration.Name,
			"vmCount", len(migration.Status.VMs),
			"type", plan.Spec.Type)
	}

	// Handle cancellations
	r.processCancellations(migration)

	// Schedule and advance VMs
	maxInFlight := max(plan.Spec.MaxInFlight, 1)
	needsRequeue := r.advanceVMs(ctx, migration, maxInFlight, mctx)

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

// resolveMigrationContext resolves all references needed for migration phases.
func (r *MigrationReconciler) resolveMigrationContext(
	ctx context.Context,
	migration *vmav1alpha1.Migration,
	plan *vmav1alpha1.MigrationPlan,
) (*migrationContext, error) {
	// Resolve Provider
	provider := &vmav1alpha1.NutanixProvider{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      plan.Spec.ProviderRef.Name,
		Namespace: migration.Namespace,
	}, provider); err != nil {
		return nil, fmt.Errorf(
			"provider %q not found: %w",
			plan.Spec.ProviderRef.Name, err)
	}

	// Read credentials
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      provider.Spec.SecretRef.Name,
		Namespace: provider.Namespace,
	}, secret); err != nil {
		return nil, fmt.Errorf(
			"provider secret %q not found: %w",
			provider.Spec.SecretRef.Name, err)
	}

	username := string(secret.Data["username"])
	password := string(secret.Data["password"])
	if username == "" || password == "" {
		return nil, fmt.Errorf(
			"provider secret must contain non-empty " +
				"'username' and 'password' keys")
	}

	// Create Nutanix client
	nxClient, err := r.ClientFactory(nutanix.ClientConfig{
		Host:               provider.Spec.URL,
		Username:           username,
		Password:           password,
		InsecureSkipVerify: provider.Spec.InsecureSkipVerify,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create Nutanix client: %w", err)
	}

	// Resolve NetworkMap
	networkMap := &vmav1alpha1.NetworkMap{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      plan.Spec.NetworkMapRef.Name,
		Namespace: migration.Namespace,
	}, networkMap); err != nil {
		return nil, fmt.Errorf(
			"NetworkMap %q not found: %w",
			plan.Spec.NetworkMapRef.Name, err)
	}

	// Resolve StorageMap
	storageMap := &vmav1alpha1.StorageMap{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      plan.Spec.StorageMapRef.Name,
		Namespace: migration.Namespace,
	}, storageMap); err != nil {
		return nil, fmt.Errorf(
			"StorageMap %q not found: %w",
			plan.Spec.StorageMapRef.Name, err)
	}

	// Build transfer manager
	transferMgr := &transfer.Manager{
		Client:   r.Client,
		PrismURL: provider.Spec.URL,
		Username: username,
		Password: password,
		Insecure: provider.Spec.InsecureSkipVerify,
	}

	return &migrationContext{
		NutanixClient: nxClient,
		Plan:          plan,
		NetworkMap:    networkMap,
		StorageMap:    storageMap,
		Provider:      provider,
		Migration:     migration,
		Secret:        secret,
		TransferMgr:   transferMgr,
	}, nil
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
	mctx *migrationContext,
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
		if r.advanceSingleVM(ctx, vm, mctx) {
			needsRequeue = true
		}
	}

	return needsRequeue
}

// advanceSingleVM advances a single VM through the phase pipeline.
// Returns true if the VM needs further processing (requeue).
func (r *MigrationReconciler) advanceSingleVM(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) bool {
	logger := log.FromContext(ctx)

	order := phaseOrder
	if mctx.Plan.Spec.Type == vmav1alpha1.MigrationTypeWarm {
		order = warmPhaseOrder
	}

	for {
		if isTerminalVMPhase(vmStatus.Phase) {
			return false
		}

		phaseStart := time.Now()
		result := r.executePhase(ctx, vmStatus, mctx)
		phaseDuration := time.Since(phaseStart)

		if result.Error != nil {
			logger.Info("VM phase failed",
				"migration", mctx.Migration.Name,
				"vm", vmStatus.ID,
				"phase", vmStatus.Phase,
				"duration", phaseDuration.String(),
				"error", result.Error.Error())
			r.recordEvent(mctx.Migration,
				corev1.EventTypeWarning,
				"MigrationFailed",
				fmt.Sprintf("VM %s failed in phase %s: %s",
					vmStatus.Name, vmStatus.Phase,
					result.Error.Error()))
			r.cleanupFailedVM(ctx, vmStatus, mctx)
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
		prev := vmStatus.Phase
		next := nextPhaseInOrder(vmStatus.Phase, order)
		vmStatus.Phase = next
		logger.Info("VM phase transition",
			"migration", mctx.Migration.Name,
			"vm", vmStatus.ID,
			"from", prev, "to", next,
			"duration", phaseDuration.String())
		r.recordEvent(mctx.Migration,
			corev1.EventTypeNormal,
			"PhaseTransition",
			fmt.Sprintf("VM %s: %s -> %s",
				vmStatus.Name, prev, next))

		if next == vmav1alpha1.VMPhaseCompleted {
			now := metav1.Now()
			vmStatus.Completed = &now
			r.recordVMDuration(vmStatus)
			return false
		}
	}
}

// executePhase dispatches to the appropriate phase handler.
func (r *MigrationReconciler) executePhase(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	switch vmStatus.Phase {
	case vmav1alpha1.VMPhaseStorePowerState:
		return r.phaseStorePowerState(ctx, vmStatus, mctx)
	case vmav1alpha1.VMPhasePowerOff:
		return r.phasePowerOff(ctx, vmStatus, mctx)
	case vmav1alpha1.VMPhaseWaitForPowerOff:
		return r.phaseWaitForPowerOff(ctx, vmStatus, mctx)
	case vmav1alpha1.VMPhaseCreateSnapshot:
		return r.phaseCreateSnapshot(ctx, vmStatus, mctx)
	case vmav1alpha1.VMPhaseExportDisks:
		return r.phaseExportDisks(ctx, vmStatus, mctx)
	case vmav1alpha1.VMPhaseImportDisks:
		return r.phaseImportDisks(ctx, vmStatus, mctx)
	case vmav1alpha1.VMPhaseCreateVM:
		return r.phaseCreateVM(ctx, vmStatus, mctx)
	case vmav1alpha1.VMPhaseStartVM:
		return r.phaseStartVM(ctx, vmStatus, mctx)
	case vmav1alpha1.VMPhaseCleanup:
		return r.phaseCleanup(ctx, vmStatus, mctx)
	case vmav1alpha1.VMPhaseBulkCopy:
		return r.phaseBulkCopy(ctx, vmStatus, mctx)
	case vmav1alpha1.VMPhaseWaitBulkCopy:
		return r.phaseWaitBulkCopy(ctx, vmStatus, mctx)
	case vmav1alpha1.VMPhasePrecopy:
		return r.phasePrecopy(ctx, vmStatus, mctx)
	case vmav1alpha1.VMPhaseFinalSync:
		return r.phaseFinalSync(ctx, vmStatus, mctx)
	case vmav1alpha1.VMPhasePreHook:
		return r.runHook(ctx, vmStatus, mctx, "PreHook")
	case vmav1alpha1.VMPhasePostHook:
		return r.runHook(ctx, vmStatus, mctx, "PostHook")
	default:
		return PhaseResult{Completed: true}
	}
}

// phaseStorePowerState records the VM's current power state.
func (r *MigrationReconciler) phaseStorePowerState(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	if vmStatus.OriginalPowerState != "" {
		return PhaseResult{Completed: true}
	}

	state, err := mctx.NutanixClient.GetVMPowerState(
		ctx, vmStatus.ID)
	if err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"StorePowerState: %w", err)}
	}
	vmStatus.OriginalPowerState = string(state)
	return PhaseResult{Completed: true}
}

// phasePowerOff powers off the source VM. Skips if already off.
func (r *MigrationReconciler) phasePowerOff(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	if vmStatus.OriginalPowerState == string(nutanix.PowerStateOff) {
		return PhaseResult{Completed: true}
	}

	if err := mctx.NutanixClient.PowerOffVM(
		ctx, vmStatus.ID); err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"PowerOff: %w", err)}
	}
	return PhaseResult{Completed: true}
}

// phaseWaitForPowerOff verifies the VM is powered off.
func (r *MigrationReconciler) phaseWaitForPowerOff(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	state, err := mctx.NutanixClient.GetVMPowerState(
		ctx, vmStatus.ID)
	if err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"WaitForPowerOff: %w", err)}
	}
	if state != nutanix.PowerStateOff {
		return PhaseResult{Completed: false}
	}
	return PhaseResult{Completed: true}
}

// phaseCreateSnapshot creates a recovery point for the VM.
// Idempotent: skips if SnapshotUUID is already set.
func (r *MigrationReconciler) phaseCreateSnapshot(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	if vmStatus.SnapshotUUID != "" {
		return PhaseResult{Completed: true}
	}

	name := fmt.Sprintf("vma-%s-%s",
		mctx.Migration.Name, shortID(vmStatus.ID))
	uuid, err := mctx.NutanixClient.CreateRecoveryPoint(
		ctx, vmStatus.ID, name)
	if err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"CreateSnapshot: %w", err)}
	}
	vmStatus.SnapshotUUID = uuid
	return PhaseResult{Completed: true}
}

// phaseExportDisks creates Nutanix images from each data disk.
// Idempotent: skips disks that already have image UUIDs.
func (r *MigrationReconciler) phaseExportDisks(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	vm, err := mctx.NutanixClient.GetVM(ctx, vmStatus.ID)
	if err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"ExportDisks: failed to get VM: %w", err)}
	}

	dataDisks := filterDataDisks(vm.Disks)
	if len(vmStatus.ImageUUIDs) >= len(dataDisks) {
		return PhaseResult{Completed: true}
	}

	clusterRef := ""
	if vm.Cluster != nil {
		clusterRef = vm.Cluster.ExtID
	}

	for i, disk := range dataDisks {
		if i < len(vmStatus.ImageUUIDs) {
			continue
		}

		diskUUID := ""
		if disk.BackingInfo != nil {
			diskUUID = disk.BackingInfo.VMDiskUUID
		}
		if diskUUID == "" {
			diskUUID = disk.ExtID
		}

		name := fmt.Sprintf("vma-%s-%s-disk-%d",
			mctx.Migration.Name, shortID(vmStatus.ID), i)
		imageUUID, createErr := mctx.NutanixClient.CreateImageFromDisk(
			ctx, name, diskUUID, clusterRef)
		if createErr != nil {
			return PhaseResult{Error: fmt.Errorf(
				"ExportDisks: disk %d: %w", i, createErr)}
		}
		vmStatus.ImageUUIDs = append(
			vmStatus.ImageUUIDs, imageUUID)
	}

	return PhaseResult{Completed: true}
}

// phaseImportDisks creates CDI DataVolumes to import disk images into
// the target cluster. Polls DataVolume status until all succeed.
func (r *MigrationReconciler) phaseImportDisks(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	vm, err := mctx.NutanixClient.GetVM(ctx, vmStatus.ID)
	if err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"ImportDisks: failed to get VM: %w", err)}
	}

	dataDisks := filterDataDisks(vm.Disks)
	targetNS := mctx.Plan.Spec.TargetNamespace
	migName := mctx.Migration.Name

	// Owner reference for cleanup
	ownerRef := metav1.OwnerReference{
		APIVersion: vmav1alpha1.GroupVersion.String(),
		Kind:       "Migration",
		Name:       mctx.Migration.Name,
		UID:        mctx.Migration.UID,
	}

	// Credential secret name
	credSecretName := fmt.Sprintf("vma-%s-creds",
		shortID(migName))
	if err := mctx.TransferMgr.CreateCredentialSecret(
		ctx, credSecretName, targetNS, ownerRef); err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"ImportDisks: credential secret: %w", err)}
	}

	// CA ConfigMap (only if custom CA is configured)
	certConfigMapName := ""
	if len(mctx.TransferMgr.CACert) > 0 {
		certConfigMapName = fmt.Sprintf("vma-%s-ca",
			shortID(migName))
		if err := mctx.TransferMgr.CreateCAConfigMap(
			ctx, certConfigMapName, targetNS,
			ownerRef); err != nil {
			return PhaseResult{Error: fmt.Errorf(
				"ImportDisks: CA ConfigMap: %w", err)}
		}
	}

	// Create DataVolumes for each disk image
	if len(vmStatus.DataVolumeNames) < len(vmStatus.ImageUUIDs) {
		for i, imageUUID := range vmStatus.ImageUUIDs {
			if i < len(vmStatus.DataVolumeNames) {
				continue
			}

			dvName := fmt.Sprintf("vma-%s-%s-disk-%d",
				migName, shortID(vmStatus.ID), i)

			// Find storage mapping for this disk
			var storageDest *vmav1alpha1.StorageDestination
			if i < len(dataDisks) {
				storageDest = transfer.FindStorageMapping(
					&dataDisks[i], mctx.StorageMap)
			}

			storageClass := "default"
			volumeMode := corev1.PersistentVolumeFilesystem
			accessMode := corev1.ReadWriteOnce
			if storageDest != nil {
				storageClass = storageDest.StorageClass
				volumeMode = storageDest.VolumeMode
				accessMode = storageDest.AccessMode
			}

			diskSize := int64(0)
			if i < len(dataDisks) {
				diskSize = dataDisks[i].DiskSizeBytes
				if diskSize == 0 &&
					dataDisks[i].BackingInfo != nil {
					diskSize = dataDisks[i].BackingInfo.DiskSizeBytes
				}
			}

			opts := transfer.DataVolumeOptions{
				Name:          dvName,
				Namespace:     targetNS,
				ImageURL:      mctx.TransferMgr.ImageDownloadURL(imageUUID),
				DiskSizeBytes: diskSize,
				StorageClass:  storageClass,
				VolumeMode:    volumeMode,
				AccessMode:    accessMode,
				SecretName:    credSecretName,
				CertConfigMap: certConfigMapName,
				OwnerRef:      ownerRef,
			}

			if err := mctx.TransferMgr.CreateDataVolume(
				ctx, opts); err != nil {
				return PhaseResult{Error: fmt.Errorf(
					"ImportDisks: DataVolume %s: %w",
					dvName, err)}
			}
			vmStatus.DataVolumeNames = append(
				vmStatus.DataVolumeNames, dvName)
		}
	}

	// Poll DataVolume status
	allSucceeded := true
	for _, dvName := range vmStatus.DataVolumeNames {
		progress, pollErr := mctx.TransferMgr.GetDataVolumeProgress(
			ctx, dvName, targetNS)
		if pollErr != nil {
			if apierrors.IsNotFound(pollErr) {
				allSucceeded = false
				continue
			}
			return PhaseResult{Error: fmt.Errorf(
				"ImportDisks: poll %s: %w",
				dvName, pollErr)}
		}

		switch progress.Phase {
		case cdiv1beta1.Succeeded:
			continue
		case cdiv1beta1.Failed:
			return PhaseResult{Error: fmt.Errorf(
				"ImportDisks: DataVolume %s failed",
				dvName)}
		default:
			allSucceeded = false
		}
	}

	if !allSucceeded {
		return PhaseResult{Completed: false}
	}

	// Record transferred bytes from disk sizes
	for _, d := range dataDisks {
		size := d.DiskSizeBytes
		if size == 0 && d.BackingInfo != nil {
			size = d.BackingInfo.DiskSizeBytes
		}
		if size > 0 {
			observability.DiskTransferBytes.WithLabelValues(
				vmStatus.Name).Add(float64(size))
		}
	}

	return PhaseResult{Completed: true}
}

// phaseCreateVM creates the KubeVirt VirtualMachine CR from Nutanix VM metadata.
// The KubeVirt VM has NO owner reference so it outlives the Migration CR.
// Idempotent: returns success if the VM already exists.
func (r *MigrationReconciler) phaseCreateVM(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	vm, err := mctx.NutanixClient.GetVM(ctx, vmStatus.ID)
	if err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"CreateVM: get source VM: %w", err)}
	}

	// PVC names match DataVolume names (CDI creates PVCs with DV name)
	kvVM := builder.Build(vm, mctx.NetworkMap, mctx.StorageMap,
		builder.BuildOptions{
			Namespace: mctx.Plan.Spec.TargetNamespace,
			PVCNames:  vmStatus.DataVolumeNames,
		})

	// NO owner reference -- KubeVirt VM outlives Migration CR
	if err := r.Create(ctx, kvVM); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return PhaseResult{Completed: true}
		}
		return PhaseResult{Error: fmt.Errorf(
			"CreateVM: %w", err)}
	}
	return PhaseResult{Completed: true}
}

// phaseStartVM sets the KubeVirt VM's RunStrategy based on the plan's
// target power state. Defaults to Running if not specified.
func (r *MigrationReconciler) phaseStartVM(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	if mctx.Plan.Spec.TargetPowerState ==
		vmav1alpha1.TargetPowerStateStopped {
		return PhaseResult{Completed: true}
	}

	// Default or Running: start the VM
	vm, err := mctx.NutanixClient.GetVM(ctx, vmStatus.ID)
	if err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"StartVM: get source VM: %w", err)}
	}

	kvVMName := builder.SanitizeName(vm.Name, nil)
	targetNS := mctx.Plan.Spec.TargetNamespace

	kvVM := &kubevirtv1.VirtualMachine{}
	if err := r.Get(ctx, types.NamespacedName{
		Name: kvVMName, Namespace: targetNS,
	}, kvVM); err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"StartVM: get KubeVirt VM: %w", err)}
	}

	runStrategy := kubevirtv1.RunStrategyAlways
	kvVM.Spec.RunStrategy = &runStrategy
	if err := r.Update(ctx, kvVM); err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"StartVM: update RunStrategy: %w", err)}
	}
	return PhaseResult{Completed: true}
}

// phaseCleanup deletes temporary Nutanix resources created during migration.
// Errors are logged but do not fail the phase (best-effort cleanup).
func (r *MigrationReconciler) phaseCleanup(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	logger := log.FromContext(ctx)

	// Delete Nutanix images (temporary exports)
	for _, imageUUID := range vmStatus.ImageUUIDs {
		if err := mctx.NutanixClient.DeleteImage(
			ctx, imageUUID); err != nil {
			logger.Error(err, "Cleanup: delete image",
				"uuid", imageUUID)
		}
	}

	// Delete Nutanix snapshot (recovery point)
	if vmStatus.SnapshotUUID != "" {
		if err := mctx.NutanixClient.DeleteRecoveryPoint(
			ctx, vmStatus.SnapshotUUID); err != nil {
			logger.Error(err, "Cleanup: delete snapshot",
				"uuid", vmStatus.SnapshotUUID)
		}
	}

	// Delete credential secret and CA ConfigMap (best-effort)
	targetNS := mctx.Plan.Spec.TargetNamespace
	migName := mctx.Migration.Name
	credName := fmt.Sprintf("vma-%s-creds", shortID(migName))
	_ = mctx.TransferMgr.DeleteCredentialSecret(
		ctx, credName, targetNS)
	caName := fmt.Sprintf("vma-%s-ca", shortID(migName))
	_ = mctx.TransferMgr.DeleteCAConfigMap(
		ctx, caName, targetNS)

	return PhaseResult{Completed: true}
}

// cleanupFailedVM performs best-effort cleanup when a VM migration fails.
// Deletes DataVolumes, Nutanix images/snapshots, and restores source power state.
func (r *MigrationReconciler) cleanupFailedVM(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) {
	logger := log.FromContext(ctx)

	targetNS := mctx.Plan.Spec.TargetNamespace

	// Delete DataVolumes (contain partially imported data)
	for _, dvName := range vmStatus.DataVolumeNames {
		dv := &cdiv1beta1.DataVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dvName,
				Namespace: targetNS,
			},
		}
		if err := r.Delete(ctx, dv); err != nil &&
			!apierrors.IsNotFound(err) {
			logger.Error(err, "FailureCleanup: delete DV",
				"name", dvName)
		}
	}

	// Delete Nutanix images
	for _, imageUUID := range vmStatus.ImageUUIDs {
		if err := mctx.NutanixClient.DeleteImage(
			ctx, imageUUID); err != nil {
			logger.Error(err, "FailureCleanup: delete image",
				"uuid", imageUUID)
		}
	}

	// Delete Nutanix snapshot
	if vmStatus.SnapshotUUID != "" {
		if err := mctx.NutanixClient.DeleteRecoveryPoint(
			ctx, vmStatus.SnapshotUUID); err != nil {
			logger.Error(err,
				"FailureCleanup: delete snapshot",
				"uuid", vmStatus.SnapshotUUID)
		}
	}

	// Restore source VM power state
	if vmStatus.OriginalPowerState ==
		string(nutanix.PowerStateOn) {
		if err := mctx.NutanixClient.PowerOnVM(
			ctx, vmStatus.ID); err != nil {
			logger.Error(err,
				"FailureCleanup: restore power",
				"vm", vmStatus.ID)
		}
	}

	// Clean up warm migration resources
	if vmStatus.Warm != nil {
		warm := vmStatus.Warm
		// Delete active delta pod and its ConfigMap
		if warm.DeltaPodName != "" {
			r.deletePod(ctx, warm.DeltaPodName, targetNS)
			r.deleteConfigMap(ctx,
				warm.DeltaPodName+"-regions", targetNS)
		}
		// Delete delta round images
		for _, uuid := range warm.DeltaImageUUIDs {
			if err := mctx.NutanixClient.DeleteImage(
				ctx, uuid); err != nil {
				logger.Error(err,
					"FailureCleanup: delete delta image",
					"uuid", uuid)
			}
		}
		// Delete delta round snapshot
		if warm.DeltaSnapshotUUID != "" {
			if err := mctx.NutanixClient.DeleteRecoveryPoint(
				ctx, warm.DeltaSnapshotUUID); err != nil {
				logger.Error(err,
					"FailureCleanup: delete delta snapshot",
					"uuid", warm.DeltaSnapshotUUID)
			}
		}
		// Delete base images
		for _, uuid := range warm.BaseImageUUIDs {
			if err := mctx.NutanixClient.DeleteImage(
				ctx, uuid); err != nil {
				logger.Error(err,
					"FailureCleanup: delete base image",
					"uuid", uuid)
			}
		}
		// Delete base snapshot
		if warm.BaseSnapshotUUID != "" {
			if err := mctx.NutanixClient.DeleteRecoveryPoint(
				ctx, warm.BaseSnapshotUUID); err != nil {
				logger.Error(err,
					"FailureCleanup: delete base snapshot",
					"uuid", warm.BaseSnapshotUUID)
			}
		}
	}
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
		r.recordEvent(migration, corev1.EventTypeNormal,
			"MigrationCancelled", "All VMs were cancelled")
		observability.MigrationsTotal.WithLabelValues(
			"cancelled").Inc()
		observability.ActiveMigrations.Dec()
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
		r.recordEvent(migration, corev1.EventTypeWarning,
			"MigrationFailed",
			fmt.Sprintf("%d of %d VMs failed",
				failed, total))
		observability.MigrationsTotal.WithLabelValues(
			"failed").Inc()
		observability.ActiveMigrations.Dec()
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
	r.recordEvent(migration, corev1.EventTypeNormal,
		"MigrationCompleted",
		fmt.Sprintf("All %d VMs migrated successfully",
			total))
	observability.MigrationsTotal.WithLabelValues(
		"completed").Inc()
	observability.ActiveMigrations.Dec()
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

	r.recordEvent(migration, corev1.EventTypeWarning,
		"MigrationFailed", message)
	observability.MigrationsTotal.WithLabelValues("failed").Inc()

	if err := r.Status().Update(ctx, migration); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// nextPhase returns the next phase in the cold pipeline sequence.
func nextPhase(
	current vmav1alpha1.VMMigrationPhase,
) vmav1alpha1.VMMigrationPhase {
	return nextPhaseInOrder(current, phaseOrder)
}

// nextPhaseInOrder returns the next phase in the given phase order.
func nextPhaseInOrder(
	current vmav1alpha1.VMMigrationPhase,
	order []vmav1alpha1.VMMigrationPhase,
) vmav1alpha1.VMMigrationPhase {
	for i, p := range order {
		if p == current && i+1 < len(order) {
			return order[i+1]
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

// filterDataDisks returns only DISK type entries, skipping CDROMs.
func filterDataDisks(disks []nutanix.Disk) []nutanix.Disk {
	result := make([]nutanix.Disk, 0, len(disks))
	for _, d := range disks {
		if strings.ToUpper(d.DeviceType) == "CDROM" {
			continue
		}
		result = append(result, d)
	}
	return result
}

// shortID returns the first 8 characters of an ID for naming.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// --- Warm migration phases ---

// phaseBulkCopy creates the initial snapshot, exports disk images,
// creates CDI DataVolumes, and initializes warm tracking state.
func (r *MigrationReconciler) phaseBulkCopy(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	// Create snapshot (idempotent)
	if vmStatus.SnapshotUUID == "" {
		name := fmt.Sprintf("vma-%s-%s",
			mctx.Migration.Name, shortID(vmStatus.ID))
		uuid, err := mctx.NutanixClient.CreateRecoveryPoint(
			ctx, vmStatus.ID, name)
		if err != nil {
			return PhaseResult{Error: fmt.Errorf(
				"BulkCopy: create snapshot: %w", err)}
		}
		vmStatus.SnapshotUUID = uuid
	}

	// Export disks (idempotent)
	vm, err := mctx.NutanixClient.GetVM(ctx, vmStatus.ID)
	if err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"BulkCopy: get VM: %w", err)}
	}
	dataDisks := filterDataDisks(vm.Disks)

	if len(vmStatus.ImageUUIDs) < len(dataDisks) {
		clusterRef := ""
		if vm.Cluster != nil {
			clusterRef = vm.Cluster.ExtID
		}
		for i, disk := range dataDisks {
			if i < len(vmStatus.ImageUUIDs) {
				continue
			}
			diskUUID := getDiskUUID(disk)
			name := fmt.Sprintf("vma-%s-%s-disk-%d",
				mctx.Migration.Name,
				shortID(vmStatus.ID), i)
			imageUUID, createErr :=
				mctx.NutanixClient.CreateImageFromDisk(
					ctx, name, diskUUID, clusterRef)
			if createErr != nil {
				return PhaseResult{Error: fmt.Errorf(
					"BulkCopy: export disk %d: %w",
					i, createErr)}
			}
			vmStatus.ImageUUIDs = append(
				vmStatus.ImageUUIDs, imageUUID)
		}
	}

	// Create DataVolumes (idempotent)
	result := r.createDataVolumes(ctx, vmStatus, mctx,
		dataDisks)
	if result.Error != nil {
		return result
	}

	// Initialize warm status
	if vmStatus.Warm == nil {
		vmStatus.Warm = &vmav1alpha1.WarmMigrationStatus{}
	}
	vmStatus.Warm.BaseSnapshotUUID = vmStatus.SnapshotUUID
	vmStatus.Warm.BaseImageUUIDs = make(
		[]string, len(vmStatus.ImageUUIDs))
	copy(vmStatus.Warm.BaseImageUUIDs, vmStatus.ImageUUIDs)

	return PhaseResult{Completed: true}
}

// phaseWaitBulkCopy polls DataVolume status until all succeed.
func (r *MigrationReconciler) phaseWaitBulkCopy(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	return r.pollDataVolumes(ctx, vmStatus, mctx)
}

// phasePrecopy runs incremental sync rounds. Each round:
// creates a snapshot, exports images, computes CBT deltas,
// and creates a delta transfer pod. Loops until cutover time.
func (r *MigrationReconciler) phasePrecopy(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	logger := log.FromContext(ctx)
	warm := vmStatus.Warm
	if warm == nil {
		return PhaseResult{Error: fmt.Errorf(
			"precopy: warm status not initialized")}
	}

	// Check cutover
	if isCutoverTime(mctx.Migration) {
		// Wait for any active pod to finish
		if warm.DeltaPodName != "" {
			phase := r.getPodPhase(ctx,
				warm.DeltaPodName,
				mctx.Plan.Spec.TargetNamespace)
			if phase == corev1.PodRunning ||
				phase == corev1.PodPending {
				return PhaseResult{Completed: false}
			}
		}
		logger.Info("Cutover time reached")
		return PhaseResult{Completed: true}
	}

	// Check active delta pod
	if warm.DeltaPodName != "" {
		return r.checkDeltaPod(ctx, vmStatus, mctx)
	}

	// Check MaxPrecopyRounds
	maxRounds := 10
	if mctx.Plan.Spec.WarmConfig != nil {
		maxRounds = mctx.Plan.Spec.WarmConfig.MaxPrecopyRounds
	}
	if warm.PrecopyRounds >= maxRounds {
		logger.Info("MaxPrecopyRounds reached, waiting for cutover",
			"rounds", warm.PrecopyRounds,
			"max", maxRounds)
		return PhaseResult{Completed: false}
	}

	// Check interval
	interval := 30 * time.Minute
	if mctx.Plan.Spec.WarmConfig != nil &&
		mctx.Plan.Spec.WarmConfig.PrecopyInterval != "" {
		parsed, parseErr := time.ParseDuration(
			mctx.Plan.Spec.WarmConfig.PrecopyInterval)
		if parseErr == nil {
			interval = parsed
		}
	}
	if warm.LastPrecopyTime != nil &&
		time.Since(warm.LastPrecopyTime.Time) < interval {
		return PhaseResult{Completed: false}
	}

	// Start new precopy round
	return r.runDeltaRound(ctx, vmStatus, mctx,
		fmt.Sprintf("pc%d", warm.PrecopyRounds+1),
		r.finalizePrecopyRound)
}

// phaseFinalSync runs a final delta sync after cutover power-off.
func (r *MigrationReconciler) phaseFinalSync(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	warm := vmStatus.Warm
	if warm == nil {
		return PhaseResult{Error: fmt.Errorf(
			"finalSync: warm status not initialized")}
	}

	// Check active delta pod
	if warm.DeltaPodName != "" {
		return r.checkDeltaPod(ctx, vmStatus, mctx)
	}

	return r.runDeltaRound(ctx, vmStatus, mctx,
		"final", r.finalizeFinalSync)
}

// runDeltaRound executes a single delta transfer round:
// snapshot -> export -> CBT -> delta pod for each disk.
func (r *MigrationReconciler) runDeltaRound(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
	label string,
	onComplete func(context.Context,
		*vmav1alpha1.VMMigrationStatus,
		*migrationContext) PhaseResult,
) PhaseResult {
	warm := vmStatus.Warm

	// Create snapshot for this round
	if warm.DeltaSnapshotUUID == "" {
		name := fmt.Sprintf("vma-%s-%s-%s",
			mctx.Migration.Name,
			shortID(vmStatus.ID), label)
		uuid, err := mctx.NutanixClient.CreateRecoveryPoint(
			ctx, vmStatus.ID, name)
		if err != nil {
			return PhaseResult{Error: fmt.Errorf(
				"%s: create snapshot: %w", label, err)}
		}
		warm.DeltaSnapshotUUID = uuid
	}

	// Export images from snapshot
	vm, err := mctx.NutanixClient.GetVM(ctx, vmStatus.ID)
	if err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"%s: get VM: %w", label, err)}
	}
	dataDisks := filterDataDisks(vm.Disks)

	if len(warm.DeltaImageUUIDs) < len(dataDisks) {
		clusterRef := ""
		if vm.Cluster != nil {
			clusterRef = vm.Cluster.ExtID
		}
		for i, disk := range dataDisks {
			if i < len(warm.DeltaImageUUIDs) {
				continue
			}
			diskUUID := getDiskUUID(disk)
			imgName := fmt.Sprintf("vma-%s-%s-%s-disk-%d",
				mctx.Migration.Name,
				shortID(vmStatus.ID), label, i)
			imageUUID, createErr :=
				mctx.NutanixClient.CreateImageFromDisk(
					ctx, imgName, diskUUID, clusterRef)
			if createErr != nil {
				return PhaseResult{Error: fmt.Errorf(
					"%s: export disk %d: %w",
					label, i, createErr)}
			}
			warm.DeltaImageUUIDs = append(
				warm.DeltaImageUUIDs, imageUUID)
		}
	}

	// Process current disk
	diskIdx := warm.DeltaDiskIndex
	if diskIdx >= len(dataDisks) {
		return onComplete(ctx, vmStatus, mctx)
	}

	// CBT: discover cluster + get changed regions
	cbtInfo, cbtErr := mctx.NutanixClient.DiscoverClusterForCBT(
		ctx, vmStatus.ID)
	if cbtErr != nil {
		return PhaseResult{Error: fmt.Errorf(
			"%s: CBT discover: %w", label, cbtErr)}
	}

	disk := dataDisks[diskIdx]
	diskSize := getDiskSize(disk)

	var allRegions []nutanix.ChangedRegion
	var cbtOffset int64
	for {
		regions, regErr :=
			mctx.NutanixClient.GetChangedRegions(
				ctx, cbtInfo.PrismElementURL,
				cbtInfo.JWTToken, vmStatus.ID,
				warm.DeltaSnapshotUUID,
				warm.BaseSnapshotUUID,
				cbtOffset, diskSize,
				transfer.CBTBlockSize)
		if regErr != nil {
			return PhaseResult{Error: fmt.Errorf(
				"%s: CBT regions: %w", label, regErr)}
		}
		allRegions = append(allRegions, regions.Regions...)
		if regions.NextOffset == nil {
			break
		}
		cbtOffset = *regions.NextOffset
	}

	deltaBytes := transfer.DeltaBytes(allRegions)
	warm.DeltaBytes += deltaBytes

	// Skip disk if no changed regions
	if len(allRegions) == 0 {
		warm.DeltaDiskIndex++
		if warm.DeltaDiskIndex >= len(dataDisks) {
			return onComplete(ctx, vmStatus, mctx)
		}
		return PhaseResult{Completed: false}
	}

	// Create delta transfer pod
	targetNS := mctx.Plan.Spec.TargetNamespace
	credSecretName := fmt.Sprintf("vma-%s-creds",
		shortID(mctx.Migration.Name))
	imageURL := mctx.TransferMgr.ImageDownloadURL(
		warm.DeltaImageUUIDs[diskIdx])
	podName := fmt.Sprintf("vma-%s-%s-%s-d%d",
		shortID(mctx.Migration.Name),
		shortID(vmStatus.ID), label, diskIdx)

	ownerRef := metav1.OwnerReference{
		APIVersion: vmav1alpha1.GroupVersion.String(),
		Kind:       "Migration",
		Name:       mctx.Migration.Name,
		UID:        mctx.Migration.UID,
	}

	pod, regionsCM := transfer.BuildDeltaPod(
		transfer.DeltaPodOptions{
			Name:       podName,
			Namespace:  targetNS,
			PVCName:    vmStatus.DataVolumeNames[diskIdx],
			ImageURL:   imageURL,
			SecretName: credSecretName,
			Regions:    allRegions,
			OwnerRef:   ownerRef,
		})

	if err := r.Create(ctx, regionsCM); err != nil &&
		!apierrors.IsAlreadyExists(err) {
		return PhaseResult{Error: fmt.Errorf(
			"%s: create regions ConfigMap: %w", label, err)}
	}
	if err := r.Create(ctx, pod); err != nil &&
		!apierrors.IsAlreadyExists(err) {
		return PhaseResult{Error: fmt.Errorf(
			"%s: create delta pod: %w", label, err)}
	}

	warm.DeltaPodName = podName
	return PhaseResult{Completed: false}
}

// checkDeltaPod checks the status of an active delta transfer pod.
func (r *MigrationReconciler) checkDeltaPod(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	warm := vmStatus.Warm
	targetNS := mctx.Plan.Spec.TargetNamespace

	phase := r.getPodPhase(ctx, warm.DeltaPodName, targetNS)
	switch phase {
	case corev1.PodSucceeded:
		r.deletePod(ctx, warm.DeltaPodName, targetNS)
		r.deleteConfigMap(ctx,
			warm.DeltaPodName+"-regions", targetNS)
		warm.DeltaPodName = ""

		vm, getErr := mctx.NutanixClient.GetVM(
			ctx, vmStatus.ID)
		if getErr != nil {
			return PhaseResult{Error: fmt.Errorf(
				"delta pod: get VM: %w", getErr)}
		}
		dataDisks := filterDataDisks(vm.Disks)

		warm.DeltaDiskIndex++
		if warm.DeltaDiskIndex >= len(dataDisks) {
			// All disks done; the caller (phasePrecopy
			// or phaseFinalSync) will re-enter runDeltaRound
			// which calls onComplete when diskIdx >= len.
			return PhaseResult{Completed: false}
		}
		return PhaseResult{Completed: false}

	case corev1.PodFailed:
		return PhaseResult{Error: fmt.Errorf(
			"delta pod %s failed", warm.DeltaPodName)}

	default: // Pending, Running, Unknown
		return PhaseResult{Completed: false}
	}
}

// finalizePrecopyRound cleans up old base resources and updates stats.
func (r *MigrationReconciler) finalizePrecopyRound(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	logger := log.FromContext(ctx)
	warm := vmStatus.Warm

	// Delete old base snapshot
	if warm.BaseSnapshotUUID != "" {
		if err := mctx.NutanixClient.DeleteRecoveryPoint(
			ctx, warm.BaseSnapshotUUID); err != nil {
			logger.Error(err,
				"Precopy: delete old base snapshot",
				"uuid", warm.BaseSnapshotUUID)
		}
	}
	// Delete old base images
	for _, uuid := range warm.BaseImageUUIDs {
		if err := mctx.NutanixClient.DeleteImage(
			ctx, uuid); err != nil {
			logger.Error(err,
				"Precopy: delete old base image",
				"uuid", uuid)
		}
	}

	// Promote delta to base
	warm.BaseSnapshotUUID = warm.DeltaSnapshotUUID
	warm.BaseImageUUIDs = make(
		[]string, len(warm.DeltaImageUUIDs))
	copy(warm.BaseImageUUIDs, warm.DeltaImageUUIDs)

	// Update stats
	warm.PrecopyRounds++
	warm.CumulativeBytes += warm.DeltaBytes
	warm.LastDeltaBytes = warm.DeltaBytes
	now := metav1.Now()
	warm.LastPrecopyTime = &now

	// Clear delta fields
	warm.DeltaSnapshotUUID = ""
	warm.DeltaImageUUIDs = nil
	warm.DeltaPodName = ""
	warm.DeltaDiskIndex = 0
	warm.DeltaBytes = 0

	logger.Info("Precopy round completed",
		"round", warm.PrecopyRounds,
		"deltaBytes", warm.LastDeltaBytes,
		"cumulativeBytes", warm.CumulativeBytes)

	return PhaseResult{Completed: false}
}

// finalizeFinalSync cleans up temporary resources after the final sync.
func (r *MigrationReconciler) finalizeFinalSync(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	logger := log.FromContext(ctx)
	warm := vmStatus.Warm

	// Delete base snapshot + images (no longer needed)
	if warm.BaseSnapshotUUID != "" {
		if err := mctx.NutanixClient.DeleteRecoveryPoint(
			ctx, warm.BaseSnapshotUUID); err != nil {
			logger.Error(err,
				"FinalSync: delete base snapshot",
				"uuid", warm.BaseSnapshotUUID)
		}
	}
	for _, uuid := range warm.BaseImageUUIDs {
		if err := mctx.NutanixClient.DeleteImage(
			ctx, uuid); err != nil {
			logger.Error(err,
				"FinalSync: delete base image",
				"uuid", uuid)
		}
	}

	// Delete final round images + snapshot
	for _, uuid := range warm.DeltaImageUUIDs {
		if err := mctx.NutanixClient.DeleteImage(
			ctx, uuid); err != nil {
			logger.Error(err,
				"FinalSync: delete delta image",
				"uuid", uuid)
		}
	}
	if warm.DeltaSnapshotUUID != "" {
		if err := mctx.NutanixClient.DeleteRecoveryPoint(
			ctx, warm.DeltaSnapshotUUID); err != nil {
			logger.Error(err,
				"FinalSync: delete delta snapshot",
				"uuid", warm.DeltaSnapshotUUID)
		}
	}

	// Update final stats
	warm.CumulativeBytes += warm.DeltaBytes
	warm.LastDeltaBytes = warm.DeltaBytes
	warm.DeltaSnapshotUUID = ""
	warm.DeltaImageUUIDs = nil
	warm.DeltaDiskIndex = 0
	warm.DeltaBytes = 0

	return PhaseResult{Completed: true}
}

// createDataVolumes creates CDI DataVolumes for disk import (shared by
// cold ImportDisks and warm BulkCopy).
func (r *MigrationReconciler) createDataVolumes(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
	dataDisks []nutanix.Disk,
) PhaseResult {
	targetNS := mctx.Plan.Spec.TargetNamespace
	migName := mctx.Migration.Name
	ownerRef := metav1.OwnerReference{
		APIVersion: vmav1alpha1.GroupVersion.String(),
		Kind:       "Migration",
		Name:       mctx.Migration.Name,
		UID:        mctx.Migration.UID,
	}

	credSecretName := fmt.Sprintf("vma-%s-creds",
		shortID(migName))
	if err := mctx.TransferMgr.CreateCredentialSecret(
		ctx, credSecretName, targetNS, ownerRef); err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"create credential secret: %w", err)}
	}

	certConfigMapName := ""
	if len(mctx.TransferMgr.CACert) > 0 {
		certConfigMapName = fmt.Sprintf("vma-%s-ca",
			shortID(migName))
		if err := mctx.TransferMgr.CreateCAConfigMap(
			ctx, certConfigMapName, targetNS,
			ownerRef); err != nil {
			return PhaseResult{Error: fmt.Errorf(
				"create CA ConfigMap: %w", err)}
		}
	}

	if len(vmStatus.DataVolumeNames) < len(vmStatus.ImageUUIDs) {
		for i, imageUUID := range vmStatus.ImageUUIDs {
			if i < len(vmStatus.DataVolumeNames) {
				continue
			}

			dvName := fmt.Sprintf("vma-%s-%s-disk-%d",
				migName, shortID(vmStatus.ID), i)

			var storageDest *vmav1alpha1.StorageDestination
			if i < len(dataDisks) {
				storageDest = transfer.FindStorageMapping(
					&dataDisks[i], mctx.StorageMap)
			}

			storageClass := "default"
			volumeMode := corev1.PersistentVolumeFilesystem
			accessMode := corev1.ReadWriteOnce
			if storageDest != nil {
				storageClass = storageDest.StorageClass
				volumeMode = storageDest.VolumeMode
				accessMode = storageDest.AccessMode
			}

			diskSize := int64(0)
			if i < len(dataDisks) {
				diskSize = getDiskSize(dataDisks[i])
			}

			opts := transfer.DataVolumeOptions{
				Name:          dvName,
				Namespace:     targetNS,
				ImageURL:      mctx.TransferMgr.ImageDownloadURL(imageUUID),
				DiskSizeBytes: diskSize,
				StorageClass:  storageClass,
				VolumeMode:    volumeMode,
				AccessMode:    accessMode,
				SecretName:    credSecretName,
				CertConfigMap: certConfigMapName,
				OwnerRef:      ownerRef,
			}

			if err := mctx.TransferMgr.CreateDataVolume(
				ctx, opts); err != nil {
				return PhaseResult{Error: fmt.Errorf(
					"DataVolume %s: %w", dvName, err)}
			}
			vmStatus.DataVolumeNames = append(
				vmStatus.DataVolumeNames, dvName)
		}
	}

	return PhaseResult{Completed: true}
}

// pollDataVolumes checks DataVolume status. Returns Completed when
// all DVs succeed, error on failure, false while in progress.
func (r *MigrationReconciler) pollDataVolumes(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) PhaseResult {
	allSucceeded := true
	for _, dvName := range vmStatus.DataVolumeNames {
		progress, pollErr :=
			mctx.TransferMgr.GetDataVolumeProgress(
				ctx, dvName,
				mctx.Plan.Spec.TargetNamespace)
		if pollErr != nil {
			if apierrors.IsNotFound(pollErr) {
				allSucceeded = false
				continue
			}
			return PhaseResult{Error: fmt.Errorf(
				"poll DV %s: %w", dvName, pollErr)}
		}
		switch progress.Phase {
		case cdiv1beta1.Succeeded:
			continue
		case cdiv1beta1.Failed:
			return PhaseResult{Error: fmt.Errorf(
				"DataVolume %s failed", dvName)}
		default:
			allSucceeded = false
		}
	}
	if !allSucceeded {
		return PhaseResult{Completed: false}
	}
	return PhaseResult{Completed: true}
}

// isCutoverTime checks if the migration's cutover timestamp has passed.
func isCutoverTime(migration *vmav1alpha1.Migration) bool {
	if migration.Spec.Cutover == nil ||
		migration.Spec.Cutover.IsZero() {
		return false
	}
	return time.Now().After(migration.Spec.Cutover.Time)
}

// getPodPhase returns the phase of a Pod, or PodUnknown if not found.
func (r *MigrationReconciler) getPodPhase(
	ctx context.Context, name, namespace string,
) corev1.PodPhase {
	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{
		Name: name, Namespace: namespace,
	}, pod); err != nil {
		return corev1.PodUnknown
	}
	return pod.Status.Phase
}

// deletePod deletes a Pod (best-effort).
func (r *MigrationReconciler) deletePod(
	ctx context.Context, name, namespace string,
) {
	pod := &corev1.Pod{}
	pod.Name = name
	pod.Namespace = namespace
	_ = r.Delete(ctx, pod)
}

// deleteConfigMap deletes a ConfigMap (best-effort).
func (r *MigrationReconciler) deleteConfigMap(
	ctx context.Context, name, namespace string,
) {
	cm := &corev1.ConfigMap{}
	cm.Name = name
	cm.Namespace = namespace
	_ = r.Delete(ctx, cm)
}

// getDiskUUID extracts the disk UUID from BackingInfo or falls back to ExtID.
func getDiskUUID(disk nutanix.Disk) string {
	if disk.BackingInfo != nil && disk.BackingInfo.VMDiskUUID != "" {
		return disk.BackingInfo.VMDiskUUID
	}
	return disk.ExtID
}

// getDiskSize returns the disk size in bytes.
func getDiskSize(disk nutanix.Disk) int64 {
	if disk.DiskSizeBytes > 0 {
		return disk.DiskSizeBytes
	}
	if disk.BackingInfo != nil {
		return disk.BackingInfo.DiskSizeBytes
	}
	return 0
}

// recordEvent emits a Kubernetes event if a Recorder is configured.
func (r *MigrationReconciler) recordEvent(
	migration *vmav1alpha1.Migration,
	eventType, reason, message string,
) {
	if r.Recorder != nil {
		r.Recorder.Eventf(
			migration, nil, eventType, reason, reason, message,
		)
	}
}

// recordVMDuration records the migration duration metric for a
// completed VM.
func (r *MigrationReconciler) recordVMDuration(
	vmStatus *vmav1alpha1.VMMigrationStatus,
) {
	if vmStatus.Started != nil && vmStatus.Completed != nil {
		duration := vmStatus.Completed.Time.Sub(
			vmStatus.Started.Time).Seconds()
		observability.MigrationDurationSeconds.WithLabelValues(
			vmStatus.Name).Observe(duration)
	}
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
