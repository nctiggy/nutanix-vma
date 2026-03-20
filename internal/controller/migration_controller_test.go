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
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

const (
	migTestPlanName      = "mig-test-plan"
	migTestMigrationName = "test-migration"
	migTestNS            = "default"
	migTestVM1ID         = "mig-vm-001"
	migTestVM2ID         = "mig-vm-002"
	migTestVM3ID         = "mig-vm-003"
	migTestTargetNS      = "target"
	migTestProviderName  = "test-provider"
	migTestSecretName    = "nutanix-creds"
	migTestNetMapName    = "netmap"
	migTestStorMapName   = "stormap"
	migTestPrismURL      = "https://prism.example.com:9440"
)

func defaultMigrationVM() nutanix.VM {
	return nutanix.VM{
		ExtID:      migTestVM1ID,
		Name:       "test-vm",
		PowerState: "ON",
		Disks: []nutanix.Disk{{
			ExtID:      "disk-001",
			DeviceType: "DISK",
			BackingInfo: &nutanix.DiskBackingInfo{
				VMDiskUUID:    "vdisk-uuid-001",
				DiskSizeBytes: 10 * 1024 * 1024 * 1024,
				StorageContainerRef: &nutanix.Reference{
					ExtID: "container-001",
				},
			},
			DiskSizeBytes: 10 * 1024 * 1024 * 1024,
		}},
		Cluster: &nutanix.Reference{ExtID: "cluster-001"},
	}
}

func migTestObjects(
	vmIDs []string, maxInFlight int,
) (*vmav1alpha1.MigrationPlan, *vmav1alpha1.Migration,
	*vmav1alpha1.NutanixProvider, *corev1.Secret,
	*vmav1alpha1.NetworkMap, *vmav1alpha1.StorageMap,
) {
	vms := make([]vmav1alpha1.PlanVM, 0, len(vmIDs))
	for _, id := range vmIDs {
		vms = append(vms, vmav1alpha1.PlanVM{
			ID: id, Name: "vm-" + id,
		})
	}
	plan := &vmav1alpha1.MigrationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      migTestPlanName,
			Namespace: migTestNS,
		},
		Spec: vmav1alpha1.MigrationPlanSpec{
			ProviderRef: corev1.LocalObjectReference{
				Name: migTestProviderName,
			},
			TargetNamespace: migTestTargetNS,
			NetworkMapRef: corev1.LocalObjectReference{
				Name: migTestNetMapName,
			},
			StorageMapRef: corev1.LocalObjectReference{
				Name: migTestStorMapName,
			},
			VMs:         vms,
			MaxInFlight: maxInFlight,
		},
	}
	migration := &vmav1alpha1.Migration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      migTestMigrationName,
			Namespace: migTestNS,
		},
		Spec: vmav1alpha1.MigrationSpec{
			PlanRef: corev1.LocalObjectReference{
				Name: migTestPlanName,
			},
		},
	}
	provider := &vmav1alpha1.NutanixProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      migTestProviderName,
			Namespace: migTestNS,
		},
		Spec: vmav1alpha1.NutanixProviderSpec{
			URL:       migTestPrismURL,
			SecretRef: corev1.LocalObjectReference{Name: migTestSecretName},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      migTestSecretName,
			Namespace: migTestNS,
		},
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte("secret"),
		},
	}
	netMap := &vmav1alpha1.NetworkMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      migTestNetMapName,
			Namespace: migTestNS,
		},
		Spec: vmav1alpha1.NetworkMapSpec{
			ProviderRef: corev1.LocalObjectReference{
				Name: migTestProviderName,
			},
			Map: []vmav1alpha1.NetworkPair{{
				Source:      vmav1alpha1.NetworkSource{ID: "sub-001"},
				Destination: vmav1alpha1.NetworkDestination{Type: "pod"},
			}},
		},
	}
	storMap := &vmav1alpha1.StorageMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      migTestStorMapName,
			Namespace: migTestNS,
		},
		Spec: vmav1alpha1.StorageMapSpec{
			ProviderRef: corev1.LocalObjectReference{
				Name: migTestProviderName,
			},
			Map: []vmav1alpha1.StoragePair{{
				Source: vmav1alpha1.StorageSource{
					ID: "container-001",
				},
				Destination: vmav1alpha1.StorageDestination{
					StorageClass: "fast-ssd",
					VolumeMode:   corev1.PersistentVolumeFilesystem,
					AccessMode:   corev1.ReadWriteOnce,
				},
			}},
		},
	}
	return plan, migration, provider, secret, netMap, storMap
}

func buildFullMigrationReconciler(
	fakeNX *fakeNutanixClient,
	vmIDs []string,
) *MigrationReconciler {
	plan, migration, provider, secret, netMap, storMap :=
		migTestObjects(vmIDs, 1)

	s := newTestScheme()
	_ = cdiv1beta1.AddToScheme(s)
	_ = kubevirtv1.AddToScheme(s)

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(plan, migration, provider, secret,
			netMap, storMap).
		WithStatusSubresource(migration,
			&cdiv1beta1.DataVolume{}).
		Build()

	return &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return fakeNX, nil
		},
	}
}

func migrationRequest() ctrl.Request {
	return ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      migTestMigrationName,
			Namespace: migTestNS,
		},
	}
}

func getMigration(
	t *testing.T, r *MigrationReconciler,
) *vmav1alpha1.Migration {
	t.Helper()
	m := &vmav1alpha1.Migration{}
	if err := r.Get(context.Background(), types.NamespacedName{
		Name:      migTestMigrationName,
		Namespace: migTestNS,
	}, m); err != nil {
		t.Fatalf("failed to get migration: %v", err)
	}
	return m
}

func TestMigrationReconcile_FullPipeline(t *testing.T) {
	vm := defaultMigrationVM()
	fakeNX := &fakeNutanixClient{
		vms:              []nutanix.VM{vm},
		createImageUUIDs: []string{"img-uuid-001"},
	}

	r := buildFullMigrationReconciler(
		fakeNX, []string{migTestVM1ID})

	// First reconcile: advances through phases, ImportDisks waits
	result, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := getMigration(t, r)

	// VM should be in ImportDisks waiting for DataVolume
	if m.Status.VMs[0].Phase != vmav1alpha1.VMPhaseImportDisks {
		t.Errorf("expected ImportDisks, got %s",
			m.Status.VMs[0].Phase)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected requeue while DataVolumes are pending")
	}

	// Verify snapshot was created
	if m.Status.VMs[0].SnapshotUUID == "" {
		t.Error("expected SnapshotUUID to be set")
	}

	// Verify image was exported
	if len(m.Status.VMs[0].ImageUUIDs) != 1 {
		t.Fatalf("expected 1 image UUID, got %d",
			len(m.Status.VMs[0].ImageUUIDs))
	}

	// Verify DataVolume was created
	if len(m.Status.VMs[0].DataVolumeNames) != 1 {
		t.Fatalf("expected 1 DV name, got %d",
			len(m.Status.VMs[0].DataVolumeNames))
	}

	// Verify original power state was stored
	if m.Status.VMs[0].OriginalPowerState != "ON" {
		t.Errorf("expected original power state ON, got %s",
			m.Status.VMs[0].OriginalPowerState)
	}

	// Now simulate DataVolume succeeding
	dvName := m.Status.VMs[0].DataVolumeNames[0]
	dv := &cdiv1beta1.DataVolume{}
	if err := r.Get(context.Background(), types.NamespacedName{
		Name:      dvName,
		Namespace: migTestTargetNS,
	}, dv); err != nil {
		t.Fatalf("DataVolume not found: %v", err)
	}
	dv.Status.Phase = cdiv1beta1.Succeeded
	if err := r.Status().Update(
		context.Background(), dv); err != nil {
		t.Fatalf("failed to update DV status: %v", err)
	}

	// Second reconcile: ImportDisks completes, CreateVM, StartVM, Cleanup
	_, err = r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 2: unexpected error: %v", err)
	}

	m = getMigration(t, r)
	if m.Status.Phase != vmav1alpha1.MigrationPhaseCompleted {
		t.Errorf("expected Completed, got %s", m.Status.Phase)
	}
	if m.Status.VMs[0].Phase != vmav1alpha1.VMPhaseCompleted {
		t.Errorf("expected VM Completed, got %s",
			m.Status.VMs[0].Phase)
	}

	// Verify KubeVirt VM was created in target namespace
	kvVM := &kubevirtv1.VirtualMachine{}
	if err := r.Get(context.Background(), types.NamespacedName{
		Name:      "test-vm",
		Namespace: migTestTargetNS,
	}, kvVM); err != nil {
		t.Fatalf("KubeVirt VM not found: %v", err)
	}

	// Verify source labels
	if kvVM.Labels["vma.nutanix.io/source-vm-uuid"] !=
		migTestVM1ID {
		t.Errorf("expected source UUID label %s, got %s",
			migTestVM1ID,
			kvVM.Labels["vma.nutanix.io/source-vm-uuid"])
	}

	// Verify RunStrategy set to Always (default Running)
	if kvVM.Spec.RunStrategy == nil ||
		*kvVM.Spec.RunStrategy != kubevirtv1.RunStrategyAlways {
		t.Error("expected RunStrategy Always for default " +
			"Running targetPowerState")
	}
}

func TestMigrationReconcile_AlreadyPoweredOff(t *testing.T) {
	vm := defaultMigrationVM()
	vm.PowerState = "OFF"
	fakeNX := &fakeNutanixClient{
		vms:              []nutanix.VM{vm},
		powerState:       nutanix.PowerStateOff,
		createImageUUIDs: []string{"img-uuid-001"},
	}

	r := buildFullMigrationReconciler(
		fakeNX, []string{migTestVM1ID})

	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := getMigration(t, r)
	// Should skip PowerOff and proceed
	if m.Status.VMs[0].OriginalPowerState != "OFF" {
		t.Errorf("expected original power state OFF, got %s",
			m.Status.VMs[0].OriginalPowerState)
	}
}

func TestMigrationReconcile_PowerOffFailure(t *testing.T) {
	vm := defaultMigrationVM()
	fakeNX := &fakeNutanixClient{
		vms:         []nutanix.VM{vm},
		powerOffErr: errors.New("power off failed"),
	}

	r := buildFullMigrationReconciler(
		fakeNX, []string{migTestVM1ID})

	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := getMigration(t, r)
	if m.Status.VMs[0].Phase != vmav1alpha1.VMPhaseFailed {
		t.Errorf("expected Failed, got %s",
			m.Status.VMs[0].Phase)
	}
	if m.Status.VMs[0].Error == "" {
		t.Error("expected error message on failed VM")
	}
}

func TestMigrationReconcile_SnapshotFailure(t *testing.T) {
	vm := defaultMigrationVM()
	fakeNX := &fakeNutanixClient{
		vms:         []nutanix.VM{vm},
		createRPErr: errors.New("snapshot failed"),
	}

	r := buildFullMigrationReconciler(
		fakeNX, []string{migTestVM1ID})

	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := getMigration(t, r)
	if m.Status.VMs[0].Phase != vmav1alpha1.VMPhaseFailed {
		t.Errorf("expected Failed, got %s",
			m.Status.VMs[0].Phase)
	}
}

func TestMigrationReconcile_ExportDisksFailure(t *testing.T) {
	vm := defaultMigrationVM()
	fakeNX := &fakeNutanixClient{
		vms:            []nutanix.VM{vm},
		createImageErr: errors.New("image export failed"),
	}

	r := buildFullMigrationReconciler(
		fakeNX, []string{migTestVM1ID})

	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := getMigration(t, r)
	if m.Status.VMs[0].Phase != vmav1alpha1.VMPhaseFailed {
		t.Errorf("expected Failed, got %s",
			m.Status.VMs[0].Phase)
	}
}

func TestMigrationReconcile_MultiDisk(t *testing.T) {
	vm := defaultMigrationVM()
	vm.Disks = append(vm.Disks, nutanix.Disk{
		ExtID:      "disk-002",
		DeviceType: "DISK",
		BackingInfo: &nutanix.DiskBackingInfo{
			VMDiskUUID:    "vdisk-uuid-002",
			DiskSizeBytes: 20 * 1024 * 1024 * 1024,
			StorageContainerRef: &nutanix.Reference{
				ExtID: "container-001",
			},
		},
		DiskSizeBytes: 20 * 1024 * 1024 * 1024,
	})
	// Add CDROM that should be skipped
	vm.Disks = append(vm.Disks, nutanix.Disk{
		ExtID:      "cdrom-001",
		DeviceType: "CDROM",
	})

	fakeNX := &fakeNutanixClient{
		vms: []nutanix.VM{vm},
		createImageUUIDs: []string{
			"img-uuid-001", "img-uuid-002",
		},
	}

	r := buildFullMigrationReconciler(
		fakeNX, []string{migTestVM1ID})

	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := getMigration(t, r)
	// Should have 2 images (CDROM skipped)
	if len(m.Status.VMs[0].ImageUUIDs) != 2 {
		t.Fatalf("expected 2 images, got %d",
			len(m.Status.VMs[0].ImageUUIDs))
	}
	// Should have 2 DataVolumes
	if len(m.Status.VMs[0].DataVolumeNames) != 2 {
		t.Fatalf("expected 2 DVs, got %d",
			len(m.Status.VMs[0].DataVolumeNames))
	}
}

func TestMigrationReconcile_DataVolumeFailure(t *testing.T) {
	vm := defaultMigrationVM()
	fakeNX := &fakeNutanixClient{
		vms:              []nutanix.VM{vm},
		createImageUUIDs: []string{"img-uuid-001"},
	}

	r := buildFullMigrationReconciler(
		fakeNX, []string{migTestVM1ID})

	// First reconcile: creates DataVolume
	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mark DV as Failed
	m := getMigration(t, r)
	dvName := m.Status.VMs[0].DataVolumeNames[0]
	dv := &cdiv1beta1.DataVolume{}
	if err = r.Get(context.Background(), types.NamespacedName{
		Name: dvName, Namespace: migTestTargetNS,
	}, dv); err != nil {
		t.Fatalf("DV not found: %v", err)
	}
	dv.Status.Phase = cdiv1beta1.Failed
	if err = r.Status().Update(
		context.Background(), dv); err != nil {
		t.Fatalf("failed to update DV status: %v", err)
	}

	// Second reconcile: should fail the VM
	_, err = r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 2: unexpected error: %v", err)
	}

	m = getMigration(t, r)
	if m.Status.VMs[0].Phase != vmav1alpha1.VMPhaseFailed {
		t.Errorf("expected Failed, got %s",
			m.Status.VMs[0].Phase)
	}
}

func TestMigrationReconcile_MaxInFlight(t *testing.T) {
	vm1 := defaultMigrationVM()
	vm2 := defaultMigrationVM()
	vm2.ExtID = migTestVM2ID
	vm2.Name = "test-vm-2"

	fakeNX := &fakeNutanixClient{
		vms: []nutanix.VM{vm1, vm2},
		createImageUUIDs: []string{
			"img-001", "img-002",
		},
	}

	r := buildFullMigrationReconciler(
		fakeNX,
		[]string{migTestVM1ID, migTestVM2ID})

	// First reconcile: VM1 starts, VM2 pending
	result, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected requeue")
	}

	m := getMigration(t, r)
	if m.Status.VMs[1].Phase != vmav1alpha1.VMPhasePending {
		t.Errorf("VM2 expected Pending, got %s",
			m.Status.VMs[1].Phase)
	}
}

func TestMigrationReconcile_Cancellation(t *testing.T) {
	vm1 := defaultMigrationVM()
	vm2 := defaultMigrationVM()
	vm2.ExtID = migTestVM2ID

	fakeNX := &fakeNutanixClient{
		vms:              []nutanix.VM{vm1, vm2},
		createImageUUIDs: []string{"img-001", "img-002"},
	}

	plan, migration, provider, secret, netMap, storMap :=
		migTestObjects(
			[]string{migTestVM1ID, migTestVM2ID}, 2)
	migration.Spec.Cancel = []string{migTestVM2ID}

	s := newTestScheme()
	_ = cdiv1beta1.AddToScheme(s)
	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(plan, migration, provider, secret,
			netMap, storMap).
		WithStatusSubresource(migration).
		Build()

	r := &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return fakeNX, nil
		},
	}

	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := getMigration(t, r)
	if m.Status.VMs[1].Phase != vmav1alpha1.VMPhaseCancelled {
		t.Errorf("VM2 expected Cancelled, got %s",
			m.Status.VMs[1].Phase)
	}
}

func TestMigrationReconcile_Finalizer(t *testing.T) {
	vm := defaultMigrationVM()
	fakeNX := &fakeNutanixClient{
		vms:              []nutanix.VM{vm},
		createImageUUIDs: []string{"img-001"},
	}

	r := buildFullMigrationReconciler(
		fakeNX, []string{migTestVM1ID})

	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := getMigration(t, r)
	hasFinalizer := false
	for _, f := range m.Finalizers {
		if f == migrationFinalizer {
			hasFinalizer = true
		}
	}
	if !hasFinalizer {
		t.Error("expected finalizer to be added")
	}
}

func TestMigrationReconcile_DeletionBlocked(t *testing.T) {
	s := newTestScheme()
	_ = cdiv1beta1.AddToScheme(s)
	plan, migration, provider, secret, netMap, storMap :=
		migTestObjects([]string{migTestVM1ID}, 1)
	migration.Finalizers = []string{migrationFinalizer}
	now := metav1.Now()
	migration.DeletionTimestamp = &now
	migration.Status.Phase = vmav1alpha1.MigrationPhaseRunning

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(plan, migration, provider, secret,
			netMap, storMap).
		WithStatusSubresource(migration).
		Build()

	r := &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return &fakeNutanixClient{}, nil
		},
	}

	result, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected requeue for running migration deletion")
	}
}

func TestMigrationReconcile_DeletionAllowed(t *testing.T) {
	s := newTestScheme()
	_ = cdiv1beta1.AddToScheme(s)
	plan, migration, provider, secret, netMap, storMap :=
		migTestObjects([]string{migTestVM1ID}, 1)
	migration.Finalizers = []string{migrationFinalizer}
	now := metav1.Now()
	migration.DeletionTimestamp = &now
	migration.Status.Phase = vmav1alpha1.MigrationPhaseCompleted

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(plan, migration, provider, secret,
			netMap, storMap).
		WithStatusSubresource(migration).
		Build()

	r := &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return &fakeNutanixClient{}, nil
		},
	}

	result, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue, got %v",
			result.RequeueAfter)
	}
}

func TestMigrationReconcile_NotFound(t *testing.T) {
	s := newTestScheme()
	_ = cdiv1beta1.AddToScheme(s)
	fc := fake.NewClientBuilder().WithScheme(s).Build()

	r := &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return nil, errors.New("not called")
		},
	}

	result, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue, got %v",
			result.RequeueAfter)
	}
}

func TestMigrationReconcile_PlanNotFound(t *testing.T) {
	s := newTestScheme()
	_ = cdiv1beta1.AddToScheme(s)
	migration := &vmav1alpha1.Migration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      migTestMigrationName,
			Namespace: migTestNS,
		},
		Spec: vmav1alpha1.MigrationSpec{
			PlanRef: corev1.LocalObjectReference{
				Name: migTestPlanName,
			},
		},
	}

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(migration).
		WithStatusSubresource(migration).
		Build()

	r := &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return &fakeNutanixClient{}, nil
		},
	}

	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := &vmav1alpha1.Migration{}
	_ = fc.Get(context.Background(), types.NamespacedName{
		Name:      migTestMigrationName,
		Namespace: migTestNS,
	}, m)

	if m.Status.Phase != vmav1alpha1.MigrationPhaseFailed {
		t.Errorf("expected Failed, got %s", m.Status.Phase)
	}
}

func TestMigrationReconcile_ProviderNotFound(t *testing.T) {
	s := newTestScheme()
	_ = cdiv1beta1.AddToScheme(s)

	plan, migration, _, _, netMap, storMap :=
		migTestObjects([]string{migTestVM1ID}, 1)

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(plan, migration, netMap, storMap).
		WithStatusSubresource(migration).
		Build()

	r := &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return &fakeNutanixClient{}, nil
		},
	}

	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := getMigrationFrom(t, r.Client)
	if m.Status.Phase != vmav1alpha1.MigrationPhaseFailed {
		t.Errorf("expected Failed for missing provider, got %s",
			m.Status.Phase)
	}
}

func TestMigrationReconcile_Idempotent(t *testing.T) {
	vm := defaultMigrationVM()
	fakeNX := &fakeNutanixClient{
		vms:              []nutanix.VM{vm},
		createImageUUIDs: []string{"img-001"},
	}

	r := buildFullMigrationReconciler(
		fakeNX, []string{migTestVM1ID})

	// First reconcile
	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}

	// Mark DV as succeeded
	m := getMigration(t, r)
	dvName := m.Status.VMs[0].DataVolumeNames[0]
	dv := &cdiv1beta1.DataVolume{}
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: dvName, Namespace: migTestTargetNS,
	}, dv)
	dv.Status.Phase = cdiv1beta1.Succeeded
	_ = r.Status().Update(context.Background(), dv)

	// Complete migration
	_, err = r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}

	m = getMigration(t, r)
	if m.Status.Phase != vmav1alpha1.MigrationPhaseCompleted {
		t.Fatalf("expected Completed, got %s", m.Status.Phase)
	}

	// Third reconcile: should be no-op
	result, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 3: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue for completed, got %v",
			result.RequeueAfter)
	}
}

func TestMigrationReconcile_SnapshotIdempotent(t *testing.T) {
	vm := defaultMigrationVM()
	fakeNX := &fakeNutanixClient{
		vms:              []nutanix.VM{vm},
		createImageUUIDs: []string{"img-001"},
	}

	r := buildFullMigrationReconciler(
		fakeNX, []string{migTestVM1ID})

	_, _ = r.Reconcile(
		context.Background(), migrationRequest())
	m := getMigration(t, r)

	snapshotUUID := m.Status.VMs[0].SnapshotUUID
	if snapshotUUID == "" {
		t.Fatal("expected SnapshotUUID to be set")
	}

	// Re-reconcile should not change the snapshot UUID
	_, _ = r.Reconcile(
		context.Background(), migrationRequest())
	m = getMigration(t, r)
	if m.Status.VMs[0].SnapshotUUID != snapshotUUID {
		t.Errorf("SnapshotUUID changed on re-reconcile: %s -> %s",
			snapshotUUID, m.Status.VMs[0].SnapshotUUID)
	}
}

func TestNextPhase(t *testing.T) {
	tests := []struct {
		current  vmav1alpha1.VMMigrationPhase
		expected vmav1alpha1.VMMigrationPhase
	}{
		{vmav1alpha1.VMPhasePending,
			vmav1alpha1.VMPhasePreHook},
		{vmav1alpha1.VMPhasePreHook,
			vmav1alpha1.VMPhaseStorePowerState},
		{vmav1alpha1.VMPhaseCleanup,
			vmav1alpha1.VMPhaseCompleted},
		{vmav1alpha1.VMPhaseCompleted,
			vmav1alpha1.VMPhaseCompleted},
	}

	for _, tt := range tests {
		got := nextPhase(tt.current)
		if got != tt.expected {
			t.Errorf("nextPhase(%s) = %s, want %s",
				tt.current, got, tt.expected)
		}
	}
}

func TestIsTerminalVMPhase(t *testing.T) {
	if !isTerminalVMPhase(vmav1alpha1.VMPhaseCompleted) {
		t.Error("Completed should be terminal")
	}
	if !isTerminalVMPhase(vmav1alpha1.VMPhaseFailed) {
		t.Error("Failed should be terminal")
	}
	if !isTerminalVMPhase(vmav1alpha1.VMPhaseCancelled) {
		t.Error("Cancelled should be terminal")
	}
	if isTerminalVMPhase(vmav1alpha1.VMPhasePending) {
		t.Error("Pending should not be terminal")
	}
	if isTerminalVMPhase(vmav1alpha1.VMPhaseCreateVM) {
		t.Error("CreateVM should not be terminal")
	}
}

func TestIsActiveVMPhase(t *testing.T) {
	if isActiveVMPhase(vmav1alpha1.VMPhasePending) {
		t.Error("Pending should not be active")
	}
	if isActiveVMPhase(vmav1alpha1.VMPhaseCompleted) {
		t.Error("Completed should not be active")
	}
	if !isActiveVMPhase(vmav1alpha1.VMPhasePreHook) {
		t.Error("PreHook should be active")
	}
	if !isActiveVMPhase(vmav1alpha1.VMPhaseImportDisks) {
		t.Error("ImportDisks should be active")
	}
}

func TestFilterDataDisks(t *testing.T) {
	disks := []nutanix.Disk{
		{ExtID: "d1", DeviceType: "DISK"},
		{ExtID: "c1", DeviceType: "CDROM"},
		{ExtID: "d2", DeviceType: "DISK"},
		{ExtID: "c2", DeviceType: "cdrom"},
	}

	result := filterDataDisks(disks)
	if len(result) != 2 {
		t.Fatalf("expected 2 data disks, got %d", len(result))
	}
	if result[0].ExtID != "d1" || result[1].ExtID != "d2" {
		t.Errorf("unexpected disk IDs: %s, %s",
			result[0].ExtID, result[1].ExtID)
	}
}

func TestShortID(t *testing.T) {
	if shortID("abcdefghijkl") != "abcdefgh" {
		t.Errorf("expected 8-char prefix, got %s",
			shortID("abcdefghijkl"))
	}
	if shortID("abc") != "abc" {
		t.Errorf("expected 'abc' unchanged, got %s",
			shortID("abc"))
	}
}

func TestMigrationReconcile_StartVMStopped(t *testing.T) {
	vm := defaultMigrationVM()
	fakeNX := &fakeNutanixClient{
		vms:              []nutanix.VM{vm},
		createImageUUIDs: []string{"img-uuid-001"},
	}

	plan, migration, provider, secret, netMap, storMap :=
		migTestObjects([]string{migTestVM1ID}, 1)
	plan.Spec.TargetPowerState = vmav1alpha1.TargetPowerStateStopped

	s := newTestScheme()
	_ = cdiv1beta1.AddToScheme(s)
	_ = kubevirtv1.AddToScheme(s)

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(plan, migration, provider, secret,
			netMap, storMap).
		WithStatusSubresource(migration,
			&cdiv1beta1.DataVolume{}).
		Build()

	r := &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return fakeNX, nil
		},
	}

	// First reconcile: advances to ImportDisks
	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}

	// Mark DV as Succeeded
	m := getMigration(t, r)
	dvName := m.Status.VMs[0].DataVolumeNames[0]
	dv := &cdiv1beta1.DataVolume{}
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: dvName, Namespace: migTestTargetNS,
	}, dv)
	dv.Status.Phase = cdiv1beta1.Succeeded
	_ = r.Status().Update(context.Background(), dv)

	// Second reconcile: completes pipeline
	_, err = r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}

	m = getMigration(t, r)
	if m.Status.Phase != vmav1alpha1.MigrationPhaseCompleted {
		t.Errorf("expected Completed, got %s", m.Status.Phase)
	}

	// Verify RunStrategy is Halted (Stopped target)
	kvVM := &kubevirtv1.VirtualMachine{}
	if err := r.Get(context.Background(), types.NamespacedName{
		Name:      "test-vm",
		Namespace: migTestTargetNS,
	}, kvVM); err != nil {
		t.Fatalf("KubeVirt VM not found: %v", err)
	}

	if kvVM.Spec.RunStrategy == nil ||
		*kvVM.Spec.RunStrategy != kubevirtv1.RunStrategyHalted {
		t.Error("expected RunStrategy Halted for Stopped " +
			"targetPowerState")
	}
}

func TestMigrationReconcile_FailureCleanupRestoresPower(t *testing.T) {
	vm := defaultMigrationVM()
	powerOnCalled := false
	fakeNX := &fakeNutanixClient{
		vms:              []nutanix.VM{vm},
		createImageUUIDs: []string{"img-uuid-001"},
	}

	r := buildFullMigrationReconciler(
		fakeNX, []string{migTestVM1ID})

	// First reconcile: reaches ImportDisks
	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}

	// Mark DV as Failed
	m := getMigration(t, r)
	dvName := m.Status.VMs[0].DataVolumeNames[0]
	dv := &cdiv1beta1.DataVolume{}
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: dvName, Namespace: migTestTargetNS,
	}, dv)
	dv.Status.Phase = cdiv1beta1.Failed
	_ = r.Status().Update(context.Background(), dv)

	// Track PowerOnVM call via custom factory
	fakeNX.powerOnErr = nil
	origPowerOn := fakeNX.PowerOnVM
	_ = origPowerOn // suppress unused warning
	// Replace fakeNX in the reconciler with tracking version
	r.ClientFactory = func(
		_ nutanix.ClientConfig,
	) (nutanix.NutanixClient, error) {
		tracked := &trackingFakeNX{
			fakeNutanixClient: fakeNX,
			onPowerOn:         func() { powerOnCalled = true },
		}
		return tracked, nil
	}

	// Second reconcile: DV failed -> VM fails -> cleanup
	_, err = r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}

	m = getMigration(t, r)
	if m.Status.VMs[0].Phase != vmav1alpha1.VMPhaseFailed {
		t.Errorf("expected Failed, got %s",
			m.Status.VMs[0].Phase)
	}

	// Verify power restore was attempted
	if !powerOnCalled {
		t.Error("expected PowerOnVM to be called for " +
			"failure cleanup")
	}

	// Verify DataVolume was deleted
	dv = &cdiv1beta1.DataVolume{}
	err = r.Get(context.Background(), types.NamespacedName{
		Name: dvName, Namespace: migTestTargetNS,
	}, dv)
	if err == nil {
		t.Error("expected DataVolume to be deleted " +
			"during failure cleanup")
	}
}

// trackingFakeNX wraps fakeNutanixClient to track method calls.
type trackingFakeNX struct {
	*fakeNutanixClient
	onPowerOn func()
}

func (t *trackingFakeNX) PowerOnVM(
	ctx context.Context, uuid string,
) error {
	if t.onPowerOn != nil {
		t.onPowerOn()
	}
	return t.fakeNutanixClient.PowerOnVM(ctx, uuid)
}

func getMigrationFrom(
	t *testing.T, c client.Client,
) *vmav1alpha1.Migration {
	t.Helper()
	m := &vmav1alpha1.Migration{}
	if err := c.Get(context.Background(), types.NamespacedName{
		Name:      migTestMigrationName,
		Namespace: migTestNS,
	}, m); err != nil {
		t.Fatalf("failed to get migration: %v", err)
	}
	return m
}

// --- Warm migration tests ---

func buildWarmMigrationReconciler(
	fakeNX *fakeNutanixClient,
	vmIDs []string,
) *MigrationReconciler {
	plan, migration, provider, secret, netMap, storMap :=
		migTestObjects(vmIDs, 1)
	plan.Spec.Type = vmav1alpha1.MigrationTypeWarm
	plan.Spec.WarmConfig = &vmav1alpha1.WarmConfig{
		PrecopyInterval:  "1s",
		MaxPrecopyRounds: 3,
	}

	s := newTestScheme()
	_ = cdiv1beta1.AddToScheme(s)
	_ = kubevirtv1.AddToScheme(s)

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(plan, migration, provider, secret,
			netMap, storMap).
		WithStatusSubresource(migration,
			&cdiv1beta1.DataVolume{}).
		Build()

	return &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return fakeNX, nil
		},
	}
}

func TestWarmMigration_BulkCopyPhase(t *testing.T) {
	vm := defaultMigrationVM()
	fakeNX := &fakeNutanixClient{
		vms:              []nutanix.VM{vm},
		createImageUUIDs: []string{"img-uuid-001"},
	}

	r := buildWarmMigrationReconciler(
		fakeNX, []string{migTestVM1ID})

	// First reconcile: BulkCopy creates snapshot, exports, DVs
	result, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected requeue while DVs are pending")
	}

	m := getMigration(t, r)

	// VM should be in WaitBulkCopy waiting for DataVolume
	if m.Status.VMs[0].Phase !=
		vmav1alpha1.VMPhaseWaitBulkCopy {
		t.Errorf("expected WaitBulkCopy, got %s",
			m.Status.VMs[0].Phase)
	}

	// Verify warm status initialized
	if m.Status.VMs[0].Warm == nil {
		t.Fatal("expected warm status to be initialized")
	}
	if m.Status.VMs[0].Warm.BaseSnapshotUUID == "" {
		t.Error("expected BaseSnapshotUUID to be set")
	}
	if len(m.Status.VMs[0].Warm.BaseImageUUIDs) != 1 {
		t.Errorf("expected 1 base image UUID, got %d",
			len(m.Status.VMs[0].Warm.BaseImageUUIDs))
	}

	// Verify snapshot and images were created
	if m.Status.VMs[0].SnapshotUUID == "" {
		t.Error("expected SnapshotUUID to be set")
	}
	if len(m.Status.VMs[0].ImageUUIDs) != 1 {
		t.Errorf("expected 1 image UUID, got %d",
			len(m.Status.VMs[0].ImageUUIDs))
	}
	if len(m.Status.VMs[0].DataVolumeNames) != 1 {
		t.Errorf("expected 1 DV name, got %d",
			len(m.Status.VMs[0].DataVolumeNames))
	}
}

func TestWarmMigration_PrecopyRound(t *testing.T) {
	vm := defaultMigrationVM()
	fakeNX := &fakeNutanixClient{
		vms: []nutanix.VM{vm},
		createImageUUIDs: []string{
			"img-bulk-001", // BulkCopy
			"img-pc1-001",  // Precopy round 1
		},
		createRPUUIDs: []string{
			"rp-bulk-001", // BulkCopy
			"rp-pc1-001",  // Precopy round 1
		},
	}

	r := buildWarmMigrationReconciler(
		fakeNX, []string{migTestVM1ID})

	// Reconcile 1: BulkCopy -> WaitBulkCopy (DVs pending)
	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}

	// Mark DV as Succeeded
	m := getMigration(t, r)
	dvName := m.Status.VMs[0].DataVolumeNames[0]
	dv := &cdiv1beta1.DataVolume{}
	if err := r.Get(context.Background(), types.NamespacedName{
		Name: dvName, Namespace: migTestTargetNS,
	}, dv); err != nil {
		t.Fatalf("DV not found: %v", err)
	}
	dv.Status.Phase = cdiv1beta1.Succeeded
	if err := r.Status().Update(
		context.Background(), dv); err != nil {
		t.Fatalf("failed to update DV status: %v", err)
	}

	// Reconcile 2: WaitBulkCopy completes -> Precopy starts
	_, err = r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}

	m = getMigration(t, r)
	// Should be in Precopy with a delta pod created
	if m.Status.VMs[0].Phase !=
		vmav1alpha1.VMPhasePrecopy {
		t.Errorf("expected Precopy, got %s",
			m.Status.VMs[0].Phase)
	}

	warm := m.Status.VMs[0].Warm
	if warm.DeltaSnapshotUUID == "" {
		t.Error("expected DeltaSnapshotUUID to be set")
	}
	if len(warm.DeltaImageUUIDs) != 1 {
		t.Errorf("expected 1 delta image UUID, got %d",
			len(warm.DeltaImageUUIDs))
	}
	if warm.DeltaPodName == "" {
		t.Error("expected DeltaPodName to be set")
	}
}

func TestWarmMigration_CutoverCompletes(t *testing.T) {
	vm := defaultMigrationVM()
	fakeNX := &fakeNutanixClient{
		vms: []nutanix.VM{vm},
		createImageUUIDs: []string{
			"img-bulk-001",  // BulkCopy
			"img-final-001", // FinalSync
		},
		createRPUUIDs: []string{
			"rp-bulk-001",  // BulkCopy
			"rp-final-001", // FinalSync
		},
	}

	plan, migration, provider, secret, netMap, storMap :=
		migTestObjects([]string{migTestVM1ID}, 1)
	plan.Spec.Type = vmav1alpha1.MigrationTypeWarm
	plan.Spec.WarmConfig = &vmav1alpha1.WarmConfig{
		PrecopyInterval:  "1h", // long interval
		MaxPrecopyRounds: 10,
	}
	// Set cutover in the past to skip precopy
	cutover := metav1.NewTime(
		time.Now().Add(-1 * time.Minute))
	migration.Spec.Cutover = &cutover

	s := newTestScheme()
	_ = cdiv1beta1.AddToScheme(s)
	_ = kubevirtv1.AddToScheme(s)

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(plan, migration, provider, secret,
			netMap, storMap).
		WithStatusSubresource(migration,
			&cdiv1beta1.DataVolume{}).
		Build()

	r := &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return fakeNX, nil
		},
	}

	// Reconcile 1: BulkCopy -> WaitBulkCopy
	_, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}

	// Mark DV as Succeeded
	m := getMigration(t, r)
	dvName := m.Status.VMs[0].DataVolumeNames[0]
	dv := &cdiv1beta1.DataVolume{}
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: dvName, Namespace: migTestTargetNS,
	}, dv)
	dv.Status.Phase = cdiv1beta1.Succeeded
	_ = r.Status().Update(context.Background(), dv)

	// Reconcile 2: WaitBulkCopy -> Precopy (cutover -> skip)
	// -> PowerOff -> WaitForPowerOff -> FinalSync
	_, err = r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}

	m = getMigration(t, r)
	// Should be in FinalSync with delta pod created
	if m.Status.VMs[0].Phase !=
		vmav1alpha1.VMPhaseFinalSync {
		t.Errorf("expected FinalSync, got %s",
			m.Status.VMs[0].Phase)
	}
	if m.Status.VMs[0].Warm.DeltaPodName == "" {
		t.Error("expected FinalSync delta pod to be created")
	}

	// Mark delta pod as Succeeded
	pod := &corev1.Pod{}
	podName := m.Status.VMs[0].Warm.DeltaPodName
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: podName, Namespace: migTestTargetNS,
	}, pod)
	pod.Status.Phase = corev1.PodSucceeded
	_ = r.Status().Update(context.Background(), pod)

	// Reconcile 3: FinalSync pod done -> finalize -> CreateVM
	_, err = r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 3: %v", err)
	}

	// Reconcile 4: Should complete
	_, err = r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 4: %v", err)
	}

	m = getMigration(t, r)
	if m.Status.Phase !=
		vmav1alpha1.MigrationPhaseCompleted {
		t.Errorf("expected Completed, got %s",
			m.Status.Phase)
	}

	// Verify KubeVirt VM was created
	kvVM := &kubevirtv1.VirtualMachine{}
	if err := r.Get(context.Background(),
		types.NamespacedName{
			Name:      "test-vm",
			Namespace: migTestTargetNS,
		}, kvVM); err != nil {
		t.Fatalf("KubeVirt VM not found: %v", err)
	}
}

func TestWarmMigration_MaxPrecopyRounds(t *testing.T) {
	vm := defaultMigrationVM()
	fakeNX := &fakeNutanixClient{
		vms:              []nutanix.VM{vm},
		createImageUUIDs: []string{"img-001"},
	}

	plan, migration, provider, secret, netMap, storMap :=
		migTestObjects([]string{migTestVM1ID}, 1)
	plan.Spec.Type = vmav1alpha1.MigrationTypeWarm
	plan.Spec.WarmConfig = &vmav1alpha1.WarmConfig{
		PrecopyInterval:  "1s",
		MaxPrecopyRounds: 0, // No rounds allowed
	}

	s := newTestScheme()
	_ = cdiv1beta1.AddToScheme(s)
	_ = kubevirtv1.AddToScheme(s)

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(plan, migration, provider, secret,
			netMap, storMap).
		WithStatusSubresource(migration,
			&cdiv1beta1.DataVolume{}).
		Build()

	r := &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return fakeNX, nil
		},
	}

	// Reconcile 1: BulkCopy
	_, _ = r.Reconcile(
		context.Background(), migrationRequest())

	// Mark DV Succeeded
	m := getMigration(t, r)
	dvName := m.Status.VMs[0].DataVolumeNames[0]
	dv := &cdiv1beta1.DataVolume{}
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: dvName, Namespace: migTestTargetNS,
	}, dv)
	dv.Status.Phase = cdiv1beta1.Succeeded
	_ = r.Status().Update(context.Background(), dv)

	// Reconcile 2: WaitBulkCopy -> Precopy
	// MaxPrecopyRounds=0 so should stay in Precopy waiting
	_, _ = r.Reconcile(
		context.Background(), migrationRequest())

	m = getMigration(t, r)
	if m.Status.VMs[0].Phase !=
		vmav1alpha1.VMPhasePrecopy {
		t.Errorf("expected Precopy (waiting for cutover), got %s",
			m.Status.VMs[0].Phase)
	}
	// No delta pod should be created
	if m.Status.VMs[0].Warm.DeltaPodName != "" {
		t.Error("no delta pod expected when MaxPrecopyRounds=0")
	}
}

func TestNextPhaseInOrder_Warm(t *testing.T) {
	tests := []struct {
		current  vmav1alpha1.VMMigrationPhase
		expected vmav1alpha1.VMMigrationPhase
	}{
		{vmav1alpha1.VMPhaseStorePowerState,
			vmav1alpha1.VMPhaseBulkCopy},
		{vmav1alpha1.VMPhaseBulkCopy,
			vmav1alpha1.VMPhaseWaitBulkCopy},
		{vmav1alpha1.VMPhaseWaitBulkCopy,
			vmav1alpha1.VMPhasePrecopy},
		{vmav1alpha1.VMPhasePrecopy,
			vmav1alpha1.VMPhasePowerOff},
		{vmav1alpha1.VMPhaseFinalSync,
			vmav1alpha1.VMPhaseCreateVM},
	}

	for _, tt := range tests {
		got := nextPhaseInOrder(tt.current, warmPhaseOrder)
		if got != tt.expected {
			t.Errorf("nextPhaseInOrder(%s, warm) = %s, want %s",
				tt.current, got, tt.expected)
		}
	}
}

func TestIsCutoverTime(t *testing.T) {
	// No cutover set
	m := &vmav1alpha1.Migration{}
	if isCutoverTime(m) {
		t.Error("no cutover should return false")
	}

	// Cutover in the past
	past := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	m.Spec.Cutover = &past
	if !isCutoverTime(m) {
		t.Error("past cutover should return true")
	}

	// Cutover in the future
	future := metav1.NewTime(time.Now().Add(1 * time.Hour))
	m.Spec.Cutover = &future
	if isCutoverTime(m) {
		t.Error("future cutover should return false")
	}
}
