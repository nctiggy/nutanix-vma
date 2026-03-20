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
	storagev1 "k8s.io/api/storage/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

const (
	planTestTargetNS     = "target-ns"
	planTestSCName       = "test-sc"
	planTestProviderName = "plan-provider"
	planTestSecretName   = "plan-creds"
	planTestNetMapName   = "plan-netmap"
	planTestStorMapName  = "plan-stormap"
	planTestPlanName     = "test-plan"
	planTestVMID         = "vm-uuid-001"
	planTestGPUVMID      = "vm-uuid-003"
)

func newPlanTestScheme() *runtime.Scheme {
	s := newTestScheme()
	_ = apiextensionsv1.AddToScheme(s)
	return s
}

// testVM returns a basic Linux VM for plan controller tests.
func testVM() nutanix.VM {
	return nutanix.VM{
		ExtID:             planTestVMID,
		Name:              "test-linux-vm",
		NumSockets:        2,
		NumCoresPerSocket: 4,
		MemorySizeBytes:   8589934592,
		MachineType:       "Q35",
		Disks: []nutanix.Disk{{
			ExtID:       "disk-001",
			DiskAddress: &nutanix.DiskAddress{BusType: "SCSI", Index: 0},
			BackingInfo: &nutanix.DiskBackingInfo{
				VMDiskUUID: "vdisk-001",
				StorageContainerRef: &nutanix.Reference{
					ExtID: "sc-uuid-001",
					Name:  "default-container",
				},
			},
			DiskSizeBytes: 53687091200,
			DeviceType:    "DISK",
		}},
		Nics: []nutanix.NIC{{
			ExtID:      "nic-001",
			NetworkRef: &nutanix.Reference{ExtID: "subnet-uuid-001", Name: "vm-network"},
			NicType:    "NORMAL_NIC",
			MacAddress: "50:6b:8d:aa:bb:01",
		}},
	}
}

// testGPUVM returns a VM with a GPU for plan controller tests.
func testGPUVM() nutanix.VM {
	return nutanix.VM{
		ExtID:             planTestGPUVMID,
		Name:              "test-gpu-vm",
		NumSockets:        2,
		NumCoresPerSocket: 8,
		MemorySizeBytes:   34359738368,
		MachineType:       "Q35",
		Disks: []nutanix.Disk{{
			ExtID:       "disk-004",
			DiskAddress: &nutanix.DiskAddress{BusType: "SCSI", Index: 0},
			BackingInfo: &nutanix.DiskBackingInfo{
				VMDiskUUID: "vdisk-004",
				StorageContainerRef: &nutanix.Reference{
					ExtID: "sc-uuid-001",
					Name:  "default-container",
				},
			},
			DiskSizeBytes: 214748364800,
			DeviceType:    "DISK",
		}},
		Nics: []nutanix.NIC{{
			ExtID:      "nic-003",
			NetworkRef: &nutanix.Reference{ExtID: "subnet-uuid-002", Name: "gpu-network"},
			NicType:    "NORMAL_NIC",
			MacAddress: "50:6b:8d:aa:bb:03",
		}},
		Gpus: []nutanix.GPU{{
			Mode: "PASSTHROUGH_GRAPHICS", DeviceID: 7864, Vendor: "NVIDIA",
		}},
	}
}

func newPlanTestProvider() *vmav1alpha1.NutanixProvider {
	return &vmav1alpha1.NutanixProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      planTestProviderName,
			Namespace: "default",
		},
		Spec: vmav1alpha1.NutanixProviderSpec{
			URL:       "https://prism.example.com:9440",
			SecretRef: corev1.LocalObjectReference{Name: planTestSecretName},
		},
	}
}

func newPlanTestSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      planTestSecretName,
			Namespace: "default",
		},
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte("secret"),
		},
	}
}

func newPlanTestNetworkMap() *vmav1alpha1.NetworkMap {
	return &vmav1alpha1.NetworkMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      planTestNetMapName,
			Namespace: "default",
		},
		Spec: vmav1alpha1.NetworkMapSpec{
			ProviderRef: corev1.LocalObjectReference{Name: planTestProviderName},
			Map: []vmav1alpha1.NetworkPair{
				{
					Source:      vmav1alpha1.NetworkSource{ID: "subnet-uuid-001"},
					Destination: vmav1alpha1.NetworkDestination{Type: vmav1alpha1.NetworkDestinationPod},
				},
				{
					Source:      vmav1alpha1.NetworkSource{ID: "subnet-uuid-002"},
					Destination: vmav1alpha1.NetworkDestination{Type: vmav1alpha1.NetworkDestinationPod},
				},
			},
		},
	}
}

func newPlanTestStorageMap() *vmav1alpha1.StorageMap {
	return &vmav1alpha1.StorageMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      planTestStorMapName,
			Namespace: "default",
		},
		Spec: vmav1alpha1.StorageMapSpec{
			ProviderRef: corev1.LocalObjectReference{Name: planTestProviderName},
			Map: []vmav1alpha1.StoragePair{{
				Source: vmav1alpha1.StorageSource{ID: "sc-uuid-001"},
				Destination: vmav1alpha1.StorageDestination{
					StorageClass: planTestSCName,
				},
			}},
		},
	}
}

func newPlanTestPlan(vmID string) *vmav1alpha1.MigrationPlan {
	return &vmav1alpha1.MigrationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      planTestPlanName,
			Namespace: "default",
		},
		Spec: vmav1alpha1.MigrationPlanSpec{
			ProviderRef:     corev1.LocalObjectReference{Name: planTestProviderName},
			TargetNamespace: planTestTargetNS,
			NetworkMapRef:   corev1.LocalObjectReference{Name: planTestNetMapName},
			StorageMapRef:   corev1.LocalObjectReference{Name: planTestStorMapName},
			VMs:             []vmav1alpha1.PlanVM{{ID: vmID}},
		},
	}
}

// allPlanTestObjects combines base plan objects with target-side objects
// needed for validation to pass.
func allPlanTestObjects(
	plan *vmav1alpha1.MigrationPlan,
) []runtime.Object {
	return []runtime.Object{
		plan,
		newPlanTestProvider(),
		newPlanTestSecret(),
		newPlanTestNetworkMap(),
		newPlanTestStorageMap(),
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: planTestTargetNS},
		},
		&storagev1.StorageClass{
			ObjectMeta:  metav1.ObjectMeta{Name: planTestSCName},
			Provisioner: "test-provisioner",
		},
		&apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: "datavolumes.cdi.kubevirt.io",
			},
		},
		&apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: "virtualmachines.kubevirt.io",
			},
		},
	}
}

func buildPlanReconciler(
	s *runtime.Scheme,
	fakeNX *fakeNutanixClient,
	objs ...runtime.Object,
) *PlanReconciler {
	builder := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(objs...)

	// Register status subresource for MigrationPlan objects
	for _, o := range objs {
		if plan, ok := o.(*vmav1alpha1.MigrationPlan); ok {
			builder = builder.WithStatusSubresource(plan)
		}
	}

	fc := builder.Build()

	return &PlanReconciler{
		Client: fc,
		ClientFactory: func(_ nutanix.ClientConfig) (nutanix.NutanixClient, error) {
			return fakeNX, nil
		},
	}
}

func TestPlanReconcile_ValidPlan_Ready(t *testing.T) {
	s := newPlanTestScheme()
	plan := newPlanTestPlan(planTestVMID)

	fakeNX := &fakeNutanixClient{
		vms: []nutanix.VM{testVM(), testGPUVM()},
	}

	r := buildPlanReconciler(s, fakeNX, allPlanTestObjects(plan)...)

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: planTestPlanName, Namespace: "default",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue, got %v", result.RequeueAfter)
	}

	updated := &vmav1alpha1.MigrationPlan{}
	if err := r.Get(context.Background(), types.NamespacedName{
		Name: planTestPlanName, Namespace: "default",
	}, updated); err != nil {
		t.Fatalf("failed to get plan: %v", err)
	}

	if updated.Status.Phase != vmav1alpha1.PlanPhaseReady {
		t.Errorf("expected phase Ready, got %s", updated.Status.Phase)
		for _, vm := range updated.Status.VMs {
			for _, c := range vm.Concerns {
				t.Logf("  VM %s concern: [%s] %s", vm.ID, c.Category, c.Message)
			}
		}
	}
	if len(updated.Status.VMs) != 1 {
		t.Fatalf("expected 1 VM status, got %d", len(updated.Status.VMs))
	}
	if updated.Status.VMs[0].ID != planTestVMID {
		t.Errorf("expected VM ID %s, got %s", planTestVMID, updated.Status.VMs[0].ID)
	}
	// Name should come from the Nutanix VM since PlanVM.Name is empty
	if updated.Status.VMs[0].Name != "test-linux-vm" {
		t.Errorf("expected VM name from Nutanix, got %q", updated.Status.VMs[0].Name)
	}

	// Check Validated=True condition
	hasValidated := false
	for _, c := range updated.Status.Conditions {
		if c.Type == conditionTypeValidated && c.Status == metav1.ConditionTrue {
			hasValidated = true
		}
	}
	if !hasValidated {
		t.Error("expected Validated=True condition")
	}
}

func TestPlanReconcile_ProviderNotFound(t *testing.T) {
	s := newPlanTestScheme()
	plan := newPlanTestPlan(planTestVMID)

	fakeNX := &fakeNutanixClient{}

	r := buildPlanReconciler(s, fakeNX, plan)

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: planTestPlanName, Namespace: "default",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("expected requeue after 1m, got %v", result.RequeueAfter)
	}

	updated := &vmav1alpha1.MigrationPlan{}
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: planTestPlanName, Namespace: "default",
	}, updated)

	if updated.Status.Phase != vmav1alpha1.PlanPhaseError {
		t.Errorf("expected phase Error, got %s", updated.Status.Phase)
	}
}

func TestPlanReconcile_SecretNotFound(t *testing.T) {
	s := newPlanTestScheme()
	plan := newPlanTestPlan(planTestVMID)

	fakeNX := &fakeNutanixClient{}

	r := buildPlanReconciler(s, fakeNX,
		plan, newPlanTestProvider(),
	)

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: planTestPlanName, Namespace: "default",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("expected requeue after 1m, got %v", result.RequeueAfter)
	}

	updated := &vmav1alpha1.MigrationPlan{}
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: planTestPlanName, Namespace: "default",
	}, updated)

	if updated.Status.Phase != vmav1alpha1.PlanPhaseError {
		t.Errorf("expected phase Error, got %s", updated.Status.Phase)
	}
}

func TestPlanReconcile_NetworkMapNotFound(t *testing.T) {
	s := newPlanTestScheme()
	plan := newPlanTestPlan(planTestVMID)

	fakeNX := &fakeNutanixClient{
		vms: []nutanix.VM{testVM()},
	}

	r := buildPlanReconciler(s, fakeNX,
		plan, newPlanTestProvider(), newPlanTestSecret(),
		newPlanTestStorageMap(),
	)

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: planTestPlanName, Namespace: "default",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("expected requeue after 1m, got %v", result.RequeueAfter)
	}

	updated := &vmav1alpha1.MigrationPlan{}
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: planTestPlanName, Namespace: "default",
	}, updated)

	if updated.Status.Phase != vmav1alpha1.PlanPhaseError {
		t.Errorf("expected phase Error, got %s", updated.Status.Phase)
	}
}

func TestPlanReconcile_StorageMapNotFound(t *testing.T) {
	s := newPlanTestScheme()
	plan := newPlanTestPlan(planTestVMID)

	fakeNX := &fakeNutanixClient{
		vms: []nutanix.VM{testVM()},
	}

	r := buildPlanReconciler(s, fakeNX,
		plan, newPlanTestProvider(), newPlanTestSecret(),
		newPlanTestNetworkMap(),
	)

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: planTestPlanName, Namespace: "default",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("expected requeue after 1m, got %v", result.RequeueAfter)
	}

	updated := &vmav1alpha1.MigrationPlan{}
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: planTestPlanName, Namespace: "default",
	}, updated)

	if updated.Status.Phase != vmav1alpha1.PlanPhaseError {
		t.Errorf("expected phase Error, got %s", updated.Status.Phase)
	}
}

func TestPlanReconcile_GetVMFails(t *testing.T) {
	s := newPlanTestScheme()
	plan := newPlanTestPlan(planTestVMID)

	fakeNX := &fakeNutanixClient{
		getVMErr: errors.New("connection refused"),
	}

	r := buildPlanReconciler(s, fakeNX, allPlanTestObjects(plan)...)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: planTestPlanName, Namespace: "default",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &vmav1alpha1.MigrationPlan{}
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: planTestPlanName, Namespace: "default",
	}, updated)

	if updated.Status.Phase != vmav1alpha1.PlanPhaseError {
		t.Errorf("expected phase Error, got %s", updated.Status.Phase)
	}
	if len(updated.Status.VMs) != 1 {
		t.Fatalf("expected 1 VM status, got %d", len(updated.Status.VMs))
	}
	if len(updated.Status.VMs[0].Concerns) == 0 {
		t.Fatal("expected concerns for failed VM")
	}
	if updated.Status.VMs[0].Concerns[0].Category != vmav1alpha1.ConcernCategoryError {
		t.Errorf("expected Error concern, got %s",
			updated.Status.VMs[0].Concerns[0].Category)
	}
}

func TestPlanReconcile_GPUWarning(t *testing.T) {
	s := newPlanTestScheme()
	plan := newPlanTestPlan(planTestGPUVMID)

	fakeNX := &fakeNutanixClient{
		vms: []nutanix.VM{testVM(), testGPUVM()},
	}

	r := buildPlanReconciler(s, fakeNX, allPlanTestObjects(plan)...)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: planTestPlanName, Namespace: "default",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &vmav1alpha1.MigrationPlan{}
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: planTestPlanName, Namespace: "default",
	}, updated)

	// GPU is a Warning, not Error -- plan should be Ready
	if updated.Status.Phase != vmav1alpha1.PlanPhaseReady {
		t.Errorf("expected phase Ready (GPU is Warning), got %s",
			updated.Status.Phase)
		for _, vm := range updated.Status.VMs {
			for _, c := range vm.Concerns {
				t.Logf("  VM %s concern: [%s] %s",
					vm.ID, c.Category, c.Message)
			}
		}
	}

	// Verify GPU warning is present
	if len(updated.Status.VMs) != 1 {
		t.Fatalf("expected 1 VM status, got %d", len(updated.Status.VMs))
	}
	hasGPUWarning := false
	for _, c := range updated.Status.VMs[0].Concerns {
		if c.Category == vmav1alpha1.ConcernCategoryWarning {
			hasGPUWarning = true
		}
	}
	if !hasGPUWarning {
		t.Error("expected GPU warning concern")
	}
}

func TestPlanReconcile_UnmappedStorage(t *testing.T) {
	s := newPlanTestScheme()
	plan := newPlanTestPlan(planTestVMID)

	fakeNX := &fakeNutanixClient{
		vms: []nutanix.VM{testVM()},
	}

	// Build objects manually with a bad StorageMap that does NOT map
	// the VM's storage container (replaces the default StorageMap).
	badStorageMap := &vmav1alpha1.StorageMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      planTestStorMapName,
			Namespace: "default",
		},
		Spec: vmav1alpha1.StorageMapSpec{
			ProviderRef: corev1.LocalObjectReference{
				Name: planTestProviderName,
			},
			Map: []vmav1alpha1.StoragePair{{
				Source: vmav1alpha1.StorageSource{
					ID: "different-container-id",
				},
				Destination: vmav1alpha1.StorageDestination{
					StorageClass: planTestSCName,
				},
			}},
		},
	}

	r := buildPlanReconciler(s, fakeNX,
		plan, newPlanTestProvider(), newPlanTestSecret(),
		newPlanTestNetworkMap(), badStorageMap,
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: planTestTargetNS},
		},
		&storagev1.StorageClass{
			ObjectMeta:  metav1.ObjectMeta{Name: planTestSCName},
			Provisioner: "test-provisioner",
		},
		&apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: "datavolumes.cdi.kubevirt.io",
			},
		},
		&apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: "virtualmachines.kubevirt.io",
			},
		},
	)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: planTestPlanName, Namespace: "default",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &vmav1alpha1.MigrationPlan{}
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: planTestPlanName, Namespace: "default",
	}, updated)

	if updated.Status.Phase != vmav1alpha1.PlanPhaseError {
		t.Errorf("expected phase Error for unmapped storage, got %s",
			updated.Status.Phase)
	}

	// Verify unmapped storage concern
	if len(updated.Status.VMs) != 1 {
		t.Fatalf("expected 1 VM status, got %d", len(updated.Status.VMs))
	}
	hasStorageError := false
	for _, c := range updated.Status.VMs[0].Concerns {
		if c.Category == vmav1alpha1.ConcernCategoryError {
			hasStorageError = true
		}
	}
	if !hasStorageError {
		t.Error("expected unmapped storage Error concern")
	}
}

func TestPlanReconcile_ClientFactoryFails(t *testing.T) {
	s := newPlanTestScheme()
	plan := newPlanTestPlan(planTestVMID)

	objs := []runtime.Object{
		plan,
		newPlanTestProvider(),
		newPlanTestSecret(),
		newPlanTestNetworkMap(),
		newPlanTestStorageMap(),
	}

	builder := fake.NewClientBuilder().WithScheme(s).
		WithRuntimeObjects(objs...).
		WithStatusSubresource(plan)
	fc := builder.Build()

	r := &PlanReconciler{
		Client: fc,
		ClientFactory: func(_ nutanix.ClientConfig) (nutanix.NutanixClient, error) {
			return nil, errors.New("TLS handshake failed")
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: planTestPlanName, Namespace: "default",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("expected requeue after 1m, got %v", result.RequeueAfter)
	}

	updated := &vmav1alpha1.MigrationPlan{}
	_ = fc.Get(context.Background(), types.NamespacedName{
		Name: planTestPlanName, Namespace: "default",
	}, updated)

	if updated.Status.Phase != vmav1alpha1.PlanPhaseError {
		t.Errorf("expected phase Error, got %s", updated.Status.Phase)
	}
}

func TestPlanReconcile_NotFound(t *testing.T) {
	s := newPlanTestScheme()

	fc := fake.NewClientBuilder().WithScheme(s).Build()

	r := &PlanReconciler{
		Client: fc,
		ClientFactory: func(_ nutanix.ClientConfig) (nutanix.NutanixClient, error) {
			return nil, errors.New("should not be called")
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: "missing-plan", Namespace: "default",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue, got %v", result.RequeueAfter)
	}
}

func TestPlanReconcile_InvalidCredentials(t *testing.T) {
	s := newPlanTestScheme()
	plan := newPlanTestPlan(planTestVMID)

	// Secret with empty password
	badSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      planTestSecretName,
			Namespace: "default",
		},
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte(""),
		},
	}

	fakeNX := &fakeNutanixClient{}

	r := buildPlanReconciler(s, fakeNX,
		plan, newPlanTestProvider(), badSecret,
		newPlanTestNetworkMap(), newPlanTestStorageMap(),
	)

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: planTestPlanName, Namespace: "default",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("expected requeue after 1m, got %v", result.RequeueAfter)
	}

	updated := &vmav1alpha1.MigrationPlan{}
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: planTestPlanName, Namespace: "default",
	}, updated)

	if updated.Status.Phase != vmav1alpha1.PlanPhaseError {
		t.Errorf("expected phase Error, got %s", updated.Status.Phase)
	}
}

func TestPlanReconcile_VMNameFromPlanSpec(t *testing.T) {
	s := newPlanTestScheme()

	// Plan with explicit VM name
	plan := &vmav1alpha1.MigrationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      planTestPlanName,
			Namespace: "default",
		},
		Spec: vmav1alpha1.MigrationPlanSpec{
			ProviderRef:     corev1.LocalObjectReference{Name: planTestProviderName},
			TargetNamespace: planTestTargetNS,
			NetworkMapRef:   corev1.LocalObjectReference{Name: planTestNetMapName},
			StorageMapRef:   corev1.LocalObjectReference{Name: planTestStorMapName},
			VMs:             []vmav1alpha1.PlanVM{{ID: planTestVMID, Name: "custom-name"}},
		},
	}

	fakeNX := &fakeNutanixClient{
		vms: []nutanix.VM{testVM()},
	}

	r := buildPlanReconciler(s, fakeNX, allPlanTestObjects(plan)...)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: planTestPlanName, Namespace: "default",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &vmav1alpha1.MigrationPlan{}
	_ = r.Get(context.Background(), types.NamespacedName{
		Name: planTestPlanName, Namespace: "default",
	}, updated)

	if len(updated.Status.VMs) != 1 {
		t.Fatalf("expected 1 VM status, got %d", len(updated.Status.VMs))
	}
	// Name should come from PlanVM.Name, not Nutanix
	if updated.Status.VMs[0].Name != "custom-name" {
		t.Errorf("expected custom name, got %q",
			updated.Status.VMs[0].Name)
	}
}
