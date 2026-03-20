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

package validation

import (
	"context"
	"strings"
	"testing"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Test constants to avoid goconst warnings.
const (
	testVMUUID        = "vm-uuid-1"
	testVMName        = "test-vm"
	testDiskID        = "disk-uuid-1"
	testDiskID2       = "disk-uuid-2"
	testContainerID   = "container-uuid-1"
	testContainerName = "default-container"
	testSubnetID      = "subnet-uuid-1"
	testSubnetName    = "vlan-100"
	testNICID         = "nic-uuid-1"
	testMAC           = "50:6b:8d:12:34:56"
	testMAC2          = "50:6b:8d:12:34:57"
	testNamespace     = "target-ns"
	testStorageClass  = "ceph-rbd"
	testNADName       = "my-nad"
	testDeviceDisk    = "DISK"
	testBusSCSI       = "SCSI"
)

// mockReader implements client.Reader for testing target-side checks.
type mockReader struct {
	namespaces     map[string]bool
	storageClasses map[string]bool
	nadKeys        map[string]bool // "namespace/name"
	cdiInstalled   bool
	kvInstalled    bool
}

func newMockReader() *mockReader {
	return &mockReader{
		namespaces:     make(map[string]bool),
		storageClasses: make(map[string]bool),
		nadKeys:        make(map[string]bool),
	}
}

func (m *mockReader) Get(
	_ context.Context,
	key client.ObjectKey,
	obj client.Object,
	_ ...client.GetOption,
) error {
	switch o := obj.(type) {
	case *corev1.Namespace:
		if m.namespaces[key.Name] {
			return nil
		}
	case *storagev1.StorageClass:
		if m.storageClasses[key.Name] {
			return nil
		}
	case *unstructured.Unstructured:
		gvk := o.GroupVersionKind()
		if gvk.Group == "k8s.cni.cncf.io" &&
			gvk.Kind == "NetworkAttachmentDefinition" {
			k := key.Namespace + "/" + key.Name
			if m.nadKeys[k] {
				return nil
			}
		}
		if gvk.Group == "apiextensions.k8s.io" &&
			gvk.Kind == "CustomResourceDefinition" {
			if key.Name == cdiCRDName && m.cdiInstalled {
				return nil
			}
			if key.Name == kubevirtCRDName && m.kvInstalled {
				return nil
			}
		}
	}
	return apierrors.NewNotFound(
		schema.GroupResource{}, key.Name,
	)
}

func (m *mockReader) List(
	_ context.Context,
	_ client.ObjectList,
	_ ...client.ListOption,
) error {
	return nil
}

// --- Helper builders ---

func makeVM() *nutanix.VM {
	return &nutanix.VM{
		ExtID: testVMUUID,
		Name:  testVMName,
	}
}

func makeDisk(id, bus string) nutanix.Disk {
	return nutanix.Disk{
		ExtID:      id,
		DeviceType: testDeviceDisk,
		DiskAddress: &nutanix.DiskAddress{
			BusType: bus,
		},
		BackingInfo: &nutanix.DiskBackingInfo{
			StorageContainerRef: &nutanix.Reference{
				ExtID: testContainerID,
				Name:  testContainerName,
			},
		},
	}
}

func makeNIC(mac string) nutanix.NIC {
	return nutanix.NIC{
		ExtID:      testNICID,
		NicType:    "NORMAL_NIC",
		MacAddress: mac,
		NetworkRef: &nutanix.Reference{
			ExtID: testSubnetID,
			Name:  testSubnetName,
		},
	}
}

func makeStorageMap(sourceID, sourceName string) *vmav1alpha1.StorageMap {
	return &vmav1alpha1.StorageMap{
		Spec: vmav1alpha1.StorageMapSpec{
			Map: []vmav1alpha1.StoragePair{{
				Source: vmav1alpha1.StorageSource{
					ID:   sourceID,
					Name: sourceName,
				},
				Destination: vmav1alpha1.StorageDestination{
					StorageClass: testStorageClass,
				},
			}},
		},
	}
}

func makeNetworkMap(
	sourceID, sourceName string,
	destType vmav1alpha1.NetworkDestinationType,
	destName string,
) *vmav1alpha1.NetworkMap {
	return &vmav1alpha1.NetworkMap{
		Spec: vmav1alpha1.NetworkMapSpec{
			Map: []vmav1alpha1.NetworkPair{{
				Source: vmav1alpha1.NetworkSource{
					ID:   sourceID,
					Name: sourceName,
				},
				Destination: vmav1alpha1.NetworkDestination{
					Type: destType,
					Name: destName,
				},
			}},
		},
	}
}

// --- Assertion helpers ---

func requireConcerns(
	t *testing.T, concerns []vmav1alpha1.Concern, count int,
) {
	t.Helper()
	if len(concerns) != count {
		t.Fatalf(
			"expected %d concern(s), got %d: %+v",
			count, len(concerns), concerns,
		)
	}
}

func requireCategory(
	t *testing.T,
	concern vmav1alpha1.Concern,
	cat vmav1alpha1.ConcernCategory,
) {
	t.Helper()
	if concern.Category != cat {
		t.Errorf(
			"expected category %s, got %s (message: %s)",
			cat, concern.Category, concern.Message,
		)
	}
}

func requireMessageContains(
	t *testing.T, concern vmav1alpha1.Concern, substr string,
) {
	t.Helper()
	if !strings.Contains(concern.Message, substr) {
		t.Errorf(
			"expected message to contain %q, got %q",
			substr, concern.Message,
		)
	}
}

// --- Source-side rule tests ---

func TestCheckUnmappedStorage(t *testing.T) {
	t.Run("unmapped container", func(t *testing.T) {
		vm := makeVM()
		vm.Disks = []nutanix.Disk{
			makeDisk(testDiskID, testBusSCSI),
		}
		concerns := checkUnmappedStorage(vm, nil)
		requireConcerns(t, concerns, 1)
		requireCategory(t, concerns[0], vmav1alpha1.ConcernCategoryError)
		requireMessageContains(t, concerns[0], testContainerName)
	})

	t.Run("mapped by ID", func(t *testing.T) {
		vm := makeVM()
		vm.Disks = []nutanix.Disk{
			makeDisk(testDiskID, testBusSCSI),
		}
		sm := makeStorageMap(testContainerID, "")
		concerns := checkUnmappedStorage(vm, sm)
		requireConcerns(t, concerns, 0)
	})

	t.Run("mapped by name", func(t *testing.T) {
		vm := makeVM()
		vm.Disks = []nutanix.Disk{
			makeDisk(testDiskID, testBusSCSI),
		}
		sm := makeStorageMap("", testContainerName)
		concerns := checkUnmappedStorage(vm, sm)
		requireConcerns(t, concerns, 0)
	})

	t.Run("CDROM skipped", func(t *testing.T) {
		vm := makeVM()
		vm.Disks = []nutanix.Disk{{
			ExtID:      testDiskID,
			DeviceType: "CDROM",
			BackingInfo: &nutanix.DiskBackingInfo{
				StorageContainerRef: &nutanix.Reference{
					ExtID: "unmapped-id",
					Name:  "unmapped-name",
				},
			},
		}}
		concerns := checkUnmappedStorage(vm, nil)
		requireConcerns(t, concerns, 0)
	})

	t.Run("deduplicates same container", func(t *testing.T) {
		vm := makeVM()
		vm.Disks = []nutanix.Disk{
			makeDisk(testDiskID, testBusSCSI),
			makeDisk(testDiskID2, testBusSCSI),
		}
		concerns := checkUnmappedStorage(vm, nil)
		requireConcerns(t, concerns, 1)
	})
}

func TestCheckUnmappedNetwork(t *testing.T) {
	t.Run("unmapped subnet", func(t *testing.T) {
		vm := makeVM()
		vm.Nics = []nutanix.NIC{
			makeNIC(testMAC),
		}
		concerns := checkUnmappedNetwork(vm, nil)
		requireConcerns(t, concerns, 1)
		requireCategory(t, concerns[0], vmav1alpha1.ConcernCategoryError)
		requireMessageContains(t, concerns[0], testSubnetName)
	})

	t.Run("mapped by ID", func(t *testing.T) {
		vm := makeVM()
		vm.Nics = []nutanix.NIC{
			makeNIC(testMAC),
		}
		nm := makeNetworkMap(
			testSubnetID, "", vmav1alpha1.NetworkDestinationPod, "",
		)
		concerns := checkUnmappedNetwork(vm, nm)
		requireConcerns(t, concerns, 0)
	})

	t.Run("mapped by name", func(t *testing.T) {
		vm := makeVM()
		vm.Nics = []nutanix.NIC{
			makeNIC(testMAC),
		}
		nm := makeNetworkMap(
			"", testSubnetName, vmav1alpha1.NetworkDestinationPod, "",
		)
		concerns := checkUnmappedNetwork(vm, nm)
		requireConcerns(t, concerns, 0)
	})

	t.Run("nil NetworkRef skipped", func(t *testing.T) {
		vm := makeVM()
		vm.Nics = []nutanix.NIC{{ExtID: testNICID}}
		concerns := checkUnmappedNetwork(vm, nil)
		requireConcerns(t, concerns, 0)
	})
}

func TestCheckGPU(t *testing.T) {
	t.Run("GPU present", func(t *testing.T) {
		vm := makeVM()
		vm.Gpus = []nutanix.GPU{{Mode: "PASSTHROUGH"}}
		concerns := checkGPU(vm)
		requireConcerns(t, concerns, 1)
		requireCategory(t, concerns[0], vmav1alpha1.ConcernCategoryWarning)
		requireMessageContains(t, concerns[0], "GPU")
	})

	t.Run("no GPU", func(t *testing.T) {
		vm := makeVM()
		concerns := checkGPU(vm)
		requireConcerns(t, concerns, 0)
	})
}

func TestCheckVolumeGroupDisks(t *testing.T) {
	t.Run("nil BackingInfo", func(t *testing.T) {
		vm := makeVM()
		vm.Disks = []nutanix.Disk{{
			ExtID:      testDiskID,
			DeviceType: testDeviceDisk,
		}}
		concerns := checkVolumeGroupDisks(vm)
		requireConcerns(t, concerns, 1)
		requireCategory(t, concerns[0], vmav1alpha1.ConcernCategoryError)
		requireMessageContains(t, concerns[0], "Volume Group")
	})

	t.Run("nil StorageContainerRef", func(t *testing.T) {
		vm := makeVM()
		vm.Disks = []nutanix.Disk{{
			ExtID:       testDiskID,
			DeviceType:  testDeviceDisk,
			BackingInfo: &nutanix.DiskBackingInfo{},
		}}
		concerns := checkVolumeGroupDisks(vm)
		requireConcerns(t, concerns, 1)
		requireCategory(t, concerns[0], vmav1alpha1.ConcernCategoryError)
	})

	t.Run("valid disk", func(t *testing.T) {
		vm := makeVM()
		vm.Disks = []nutanix.Disk{
			makeDisk(testDiskID, testBusSCSI),
		}
		concerns := checkVolumeGroupDisks(vm)
		requireConcerns(t, concerns, 0)
	})

	t.Run("CDROM with nil BackingInfo", func(t *testing.T) {
		vm := makeVM()
		vm.Disks = []nutanix.Disk{{
			ExtID:      testDiskID,
			DeviceType: "CDROM",
		}}
		concerns := checkVolumeGroupDisks(vm)
		requireConcerns(t, concerns, 0)
	})
}

func TestCheckNICTypes(t *testing.T) {
	t.Run("NETWORK_FUNCTION_NIC", func(t *testing.T) {
		vm := makeVM()
		vm.Nics = []nutanix.NIC{{
			ExtID:   testNICID,
			NicType: "NETWORK_FUNCTION_NIC",
		}}
		concerns := checkNICTypes(vm)
		requireConcerns(t, concerns, 1)
		requireCategory(t, concerns[0], vmav1alpha1.ConcernCategoryError)
		requireMessageContains(t, concerns[0], nicTypeNetFunc)
	})

	t.Run("DIRECT_NIC", func(t *testing.T) {
		vm := makeVM()
		vm.Nics = []nutanix.NIC{{
			ExtID:   testNICID,
			NicType: "DIRECT_NIC",
		}}
		concerns := checkNICTypes(vm)
		requireConcerns(t, concerns, 1)
		requireCategory(t, concerns[0], vmav1alpha1.ConcernCategoryWarning)
		requireMessageContains(t, concerns[0], "SR-IOV")
	})

	t.Run("NORMAL_NIC", func(t *testing.T) {
		vm := makeVM()
		vm.Nics = []nutanix.NIC{{
			ExtID:   testNICID,
			NicType: "NORMAL_NIC",
		}}
		concerns := checkNICTypes(vm)
		requireConcerns(t, concerns, 0)
	})
}

func TestCheckMACConflicts(t *testing.T) {
	t.Run("conflict found", func(t *testing.T) {
		vm := makeVM()
		vm.Nics = []nutanix.NIC{
			makeNIC(testMAC),
		}
		macs := map[string]string{testMAC: "existing-vm"}
		concerns := checkMACConflicts(vm, macs)
		requireConcerns(t, concerns, 1)
		requireCategory(t, concerns[0], vmav1alpha1.ConcernCategoryWarning)
		requireMessageContains(t, concerns[0], "existing-vm")
	})

	t.Run("no conflict", func(t *testing.T) {
		vm := makeVM()
		vm.Nics = []nutanix.NIC{
			makeNIC(testMAC),
		}
		macs := map[string]string{testMAC2: "other-vm"}
		concerns := checkMACConflicts(vm, macs)
		requireConcerns(t, concerns, 0)
	})

	t.Run("empty MAC skipped", func(t *testing.T) {
		vm := makeVM()
		vm.Nics = []nutanix.NIC{
			makeNIC(""),
		}
		macs := map[string]string{testMAC: "existing-vm"}
		concerns := checkMACConflicts(vm, macs)
		requireConcerns(t, concerns, 0)
	})

	t.Run("nil existingMACs", func(t *testing.T) {
		vm := makeVM()
		vm.Nics = []nutanix.NIC{
			makeNIC(testMAC),
		}
		concerns := checkMACConflicts(vm, nil)
		requireConcerns(t, concerns, 0)
	})
}

func TestCheckIDEDisks(t *testing.T) {
	t.Run("IDE disk", func(t *testing.T) {
		vm := makeVM()
		vm.Disks = []nutanix.Disk{
			makeDisk(testDiskID, "IDE"),
		}
		concerns := checkIDEDisks(vm)
		requireConcerns(t, concerns, 1)
		requireCategory(t, concerns[0], vmav1alpha1.ConcernCategoryInfo)
		requireMessageContains(t, concerns[0], "IDE")
	})

	t.Run("SCSI disk", func(t *testing.T) {
		vm := makeVM()
		vm.Disks = []nutanix.Disk{
			makeDisk(testDiskID, testBusSCSI),
		}
		concerns := checkIDEDisks(vm)
		requireConcerns(t, concerns, 0)
	})

	t.Run("CDROM with IDE skipped", func(t *testing.T) {
		vm := makeVM()
		vm.Disks = []nutanix.Disk{{
			ExtID:      testDiskID,
			DeviceType: "CDROM",
			DiskAddress: &nutanix.DiskAddress{
				BusType: "IDE",
			},
		}}
		concerns := checkIDEDisks(vm)
		requireConcerns(t, concerns, 0)
	})
}

// --- Target-side rule tests ---

func TestCheckNamespace(t *testing.T) {
	ctx := context.Background()

	t.Run("exists", func(t *testing.T) {
		mc := newMockReader()
		mc.namespaces[testNamespace] = true
		concerns := checkNamespace(ctx, mc, testNamespace)
		requireConcerns(t, concerns, 0)
	})

	t.Run("not found", func(t *testing.T) {
		mc := newMockReader()
		concerns := checkNamespace(ctx, mc, testNamespace)
		requireConcerns(t, concerns, 1)
		requireCategory(t, concerns[0], vmav1alpha1.ConcernCategoryError)
		requireMessageContains(t, concerns[0], testNamespace)
	})
}

func TestCheckStorageClasses(t *testing.T) {
	ctx := context.Background()

	t.Run("exists", func(t *testing.T) {
		mc := newMockReader()
		mc.storageClasses[testStorageClass] = true
		vm := makeVM()
		vm.Disks = []nutanix.Disk{
			makeDisk(testDiskID, testBusSCSI),
		}
		sm := makeStorageMap(testContainerID, "")
		concerns := checkStorageClasses(ctx, mc, vm, sm)
		requireConcerns(t, concerns, 0)
	})

	t.Run("not found", func(t *testing.T) {
		mc := newMockReader()
		vm := makeVM()
		vm.Disks = []nutanix.Disk{
			makeDisk(testDiskID, testBusSCSI),
		}
		sm := makeStorageMap(testContainerID, "")
		concerns := checkStorageClasses(ctx, mc, vm, sm)
		requireConcerns(t, concerns, 1)
		requireCategory(t, concerns[0], vmav1alpha1.ConcernCategoryError)
		requireMessageContains(t, concerns[0], testStorageClass)
	})

	t.Run("nil StorageMap", func(t *testing.T) {
		mc := newMockReader()
		vm := makeVM()
		concerns := checkStorageClasses(ctx, mc, vm, nil)
		requireConcerns(t, concerns, 0)
	})
}

func TestCheckMultusNADs(t *testing.T) {
	ctx := context.Background()

	t.Run("exists", func(t *testing.T) {
		mc := newMockReader()
		mc.nadKeys[testNamespace+"/"+testNADName] = true
		vm := makeVM()
		vm.Nics = []nutanix.NIC{
			makeNIC(testMAC),
		}
		nm := makeNetworkMap(
			testSubnetID, "",
			vmav1alpha1.NetworkDestinationMultus, testNADName,
		)
		concerns := checkMultusNADs(ctx, mc, vm, nm, testNamespace)
		requireConcerns(t, concerns, 0)
	})

	t.Run("not found", func(t *testing.T) {
		mc := newMockReader()
		vm := makeVM()
		vm.Nics = []nutanix.NIC{
			makeNIC(testMAC),
		}
		nm := makeNetworkMap(
			testSubnetID, "",
			vmav1alpha1.NetworkDestinationMultus, testNADName,
		)
		concerns := checkMultusNADs(ctx, mc, vm, nm, testNamespace)
		requireConcerns(t, concerns, 1)
		requireCategory(t, concerns[0], vmav1alpha1.ConcernCategoryError)
		requireMessageContains(t, concerns[0], testNADName)
	})

	t.Run("pod network no check", func(t *testing.T) {
		mc := newMockReader()
		vm := makeVM()
		vm.Nics = []nutanix.NIC{
			makeNIC(testMAC),
		}
		nm := makeNetworkMap(
			testSubnetID, "",
			vmav1alpha1.NetworkDestinationPod, "",
		)
		concerns := checkMultusNADs(ctx, mc, vm, nm, testNamespace)
		requireConcerns(t, concerns, 0)
	})

	t.Run("nil NetworkMap", func(t *testing.T) {
		mc := newMockReader()
		vm := makeVM()
		concerns := checkMultusNADs(ctx, mc, vm, nil, testNamespace)
		requireConcerns(t, concerns, 0)
	})
}

func TestCheckCDIInstalled(t *testing.T) {
	ctx := context.Background()

	t.Run("installed", func(t *testing.T) {
		mc := newMockReader()
		mc.cdiInstalled = true
		concerns := checkCDIInstalled(ctx, mc)
		requireConcerns(t, concerns, 0)
	})

	t.Run("not installed", func(t *testing.T) {
		mc := newMockReader()
		concerns := checkCDIInstalled(ctx, mc)
		requireConcerns(t, concerns, 1)
		requireCategory(t, concerns[0], vmav1alpha1.ConcernCategoryError)
		requireMessageContains(t, concerns[0], "CDI")
	})
}

func TestCheckKubeVirtInstalled(t *testing.T) {
	ctx := context.Background()

	t.Run("installed", func(t *testing.T) {
		mc := newMockReader()
		mc.kvInstalled = true
		concerns := checkKubeVirtInstalled(ctx, mc)
		requireConcerns(t, concerns, 0)
	})

	t.Run("not installed", func(t *testing.T) {
		mc := newMockReader()
		concerns := checkKubeVirtInstalled(ctx, mc)
		requireConcerns(t, concerns, 1)
		requireCategory(t, concerns[0], vmav1alpha1.ConcernCategoryError)
		requireMessageContains(t, concerns[0], "KubeVirt")
	})
}

// --- Integration tests ---

func TestValidate_NilClient(t *testing.T) {
	vm := makeVM()
	vm.Disks = []nutanix.Disk{
		makeDisk(testDiskID, testBusSCSI),
	}
	vm.Nics = []nutanix.NIC{
		makeNIC(testMAC),
	}
	sm := makeStorageMap(testContainerID, "")
	nm := makeNetworkMap(
		testSubnetID, "", vmav1alpha1.NetworkDestinationPod, "",
	)

	concerns := Validate(context.Background(), vm, ValidationOptions{
		StorageMap:      sm,
		NetworkMap:      nm,
		TargetNamespace: testNamespace,
		Client:          nil, // skip target-side
	})

	// No source-side concerns expected (all mapped, normal NIC, SCSI bus).
	requireConcerns(t, concerns, 0)
}

func TestValidate_CleanVM(t *testing.T) {
	mc := newMockReader()
	mc.namespaces[testNamespace] = true
	mc.storageClasses[testStorageClass] = true
	mc.cdiInstalled = true
	mc.kvInstalled = true

	vm := makeVM()
	vm.Disks = []nutanix.Disk{
		makeDisk(testDiskID, testBusSCSI),
	}
	vm.Nics = []nutanix.NIC{
		makeNIC(testMAC),
	}
	sm := makeStorageMap(testContainerID, "")
	nm := makeNetworkMap(
		testSubnetID, "", vmav1alpha1.NetworkDestinationPod, "",
	)

	concerns := Validate(context.Background(), vm, ValidationOptions{
		StorageMap:      sm,
		NetworkMap:      nm,
		TargetNamespace: testNamespace,
		Client:          mc,
	})

	requireConcerns(t, concerns, 0)
}

func TestValidate_MultipleConcerns(t *testing.T) {
	mc := newMockReader()
	// namespace missing, CDI not installed, KubeVirt not installed

	vm := makeVM()
	// Unmapped storage + IDE disk
	vm.Disks = []nutanix.Disk{
		makeDisk(testDiskID, "IDE"),
	}
	// GPU
	vm.Gpus = []nutanix.GPU{{Mode: "PASSTHROUGH"}}
	// NETWORK_FUNCTION_NIC with unmapped subnet
	vm.Nics = []nutanix.NIC{{
		ExtID:   testNICID,
		NicType: "NETWORK_FUNCTION_NIC",
		NetworkRef: &nutanix.Reference{
			ExtID: testSubnetID,
			Name:  testSubnetName,
		},
	}}

	concerns := Validate(context.Background(), vm, ValidationOptions{
		TargetNamespace: testNamespace,
		Client:          mc,
	})

	// Expected concerns:
	// 1. unmapped storage (Error)
	// 2. unmapped network (Error)
	// 3. GPU (Warning)
	// 4. NETWORK_FUNCTION_NIC (Error)
	// 5. IDE disk (Info)
	// 6. namespace missing (Error)
	// 7. CDI not installed (Error)
	// 8. KubeVirt not installed (Error)
	if len(concerns) < 7 {
		t.Fatalf(
			"expected at least 7 concerns, got %d: %+v",
			len(concerns), concerns,
		)
	}

	if !HasErrors(concerns) {
		t.Error("expected HasErrors to return true")
	}

	// Verify mix of categories.
	var errors, warnings, infos int
	for _, c := range concerns {
		switch c.Category {
		case vmav1alpha1.ConcernCategoryError:
			errors++
		case vmav1alpha1.ConcernCategoryWarning:
			warnings++
		case vmav1alpha1.ConcernCategoryInfo:
			infos++
		}
	}
	if errors == 0 {
		t.Error("expected at least one Error concern")
	}
	if warnings == 0 {
		t.Error("expected at least one Warning concern")
	}
	if infos == 0 {
		t.Error("expected at least one Info concern")
	}
}

func TestHasErrors(t *testing.T) {
	t.Run("with errors", func(t *testing.T) {
		concerns := []vmav1alpha1.Concern{
			{Category: vmav1alpha1.ConcernCategoryWarning},
			{Category: vmav1alpha1.ConcernCategoryError},
		}
		if !HasErrors(concerns) {
			t.Error("expected true")
		}
	})

	t.Run("no errors", func(t *testing.T) {
		concerns := []vmav1alpha1.Concern{
			{Category: vmav1alpha1.ConcernCategoryWarning},
			{Category: vmav1alpha1.ConcernCategoryInfo},
		}
		if HasErrors(concerns) {
			t.Error("expected false")
		}
	})

	t.Run("empty", func(t *testing.T) {
		if HasErrors(nil) {
			t.Error("expected false for nil")
		}
	})
}
