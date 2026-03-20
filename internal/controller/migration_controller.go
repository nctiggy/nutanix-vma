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
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/builder"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
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
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=migrationplans,verbs=get
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=nutanixproviders,verbs=get
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=networkmaps,verbs=get
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=storagemaps,verbs=get
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;create;delete
// +kubebuilder:rbac:groups=cdi.kubevirt.io,resources=datavolumes,verbs=get;list;create;delete
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines,verbs=get;create;update

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
	for {
		if isTerminalVMPhase(vmStatus.Phase) {
			return false
		}

		result := r.executePhase(ctx, vmStatus, mctx)
		if result.Error != nil {
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
		next := nextPhase(vmStatus.Phase)
		vmStatus.Phase = next
		if next == vmav1alpha1.VMPhaseCompleted {
			now := metav1.Now()
			vmStatus.Completed = &now
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
	default:
		// PreHook, PostHook are stubs (US-015)
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

// SetupWithManager sets up the controller with the Manager.
func (r *MigrationReconciler) SetupWithManager(
	mgr ctrl.Manager,
) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vmav1alpha1.Migration{}).
		Named("migration").
		Complete(r)
}
