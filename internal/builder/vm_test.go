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

package builder

import (
	"testing"

	kubevirtv1 "kubevirt.io/api/core/v1"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

const testBootTypeSecureBoot = "SECURE_BOOT"

// --- helpers ---

func simpleNetworkMap() *vmav1alpha1.NetworkMap {
	return &vmav1alpha1.NetworkMap{
		Spec: vmav1alpha1.NetworkMapSpec{
			Map: []vmav1alpha1.NetworkPair{
				{
					Source: vmav1alpha1.NetworkSource{
						ID: "subnet-aaa",
					},
					Destination: vmav1alpha1.NetworkDestination{
						Type: vmav1alpha1.NetworkDestinationPod,
					},
				},
			},
		},
	}
}

func multusNetworkMap() *vmav1alpha1.NetworkMap {
	return &vmav1alpha1.NetworkMap{
		Spec: vmav1alpha1.NetworkMapSpec{
			Map: []vmav1alpha1.NetworkPair{
				{
					Source: vmav1alpha1.NetworkSource{
						ID: "subnet-bbb",
					},
					Destination: vmav1alpha1.NetworkDestination{
						Type:      vmav1alpha1.NetworkDestinationMultus,
						Name:      "my-nad",
						Namespace: "networking",
					},
				},
			},
		},
	}
}

func simpleStorageMap() *vmav1alpha1.StorageMap {
	return &vmav1alpha1.StorageMap{
		Spec: vmav1alpha1.StorageMapSpec{
			Map: []vmav1alpha1.StoragePair{
				{
					Source: vmav1alpha1.StorageSource{
						ID: "sc-111",
					},
					Destination: vmav1alpha1.StorageDestination{
						StorageClass: "ceph-rbd",
					},
				},
			},
		},
	}
}

func singleDiskLinuxVM() *nutanix.VM {
	return &nutanix.VM{
		ExtID:             "vm-uuid-001",
		Name:              "linux-web-01",
		NumSockets:        2,
		NumCoresPerSocket: 4,
		NumThreadsPerCore: 1,
		MemorySizeBytes:   4 * 1024 * 1024 * 1024, // 4 GiB
		MachineType:       "Q35",
		BootConfig: &nutanix.BootConfig{
			BootType:  "LEGACY",
			BootOrder: []string{"DISK", "NETWORK"},
		},
		Disks: []nutanix.Disk{
			{
				ExtID:      "disk-001",
				DeviceType: "DISK",
				DiskAddress: &nutanix.DiskAddress{
					BusType: "SCSI",
					Index:   0,
				},
				DiskSizeBytes: 50 * 1024 * 1024 * 1024,
			},
		},
		Nics: []nutanix.NIC{
			{
				ExtID:      "nic-001",
				MacAddress: "00:50:56:aa:bb:cc",
				NetworkRef: &nutanix.Reference{ExtID: "subnet-aaa"},
				NicType:    "NORMAL_NIC",
			},
		},
	}
}

// --- Build tests ---

func TestBuild_SingleDiskLinux(t *testing.T) {
	vm := singleDiskLinuxVM()
	nmap := simpleNetworkMap()
	smap := simpleStorageMap()
	opts := BuildOptions{
		Namespace: "target-ns",
		PVCNames:  []string{"linux-web-01-disk-0"},
	}

	result := Build(vm, nmap, smap, opts)

	// Name and namespace.
	if result.Name != "linux-web-01" {
		t.Errorf("expected name linux-web-01, got %s", result.Name)
	}
	if result.Namespace != "target-ns" {
		t.Errorf("expected namespace target-ns, got %s", result.Namespace)
	}

	// Labels on metadata.
	if result.Labels[labelSourceUUID] != "vm-uuid-001" {
		t.Errorf("expected source UUID label, got %s", result.Labels[labelSourceUUID])
	}

	// Labels on template metadata.
	tmplLabels := result.Spec.Template.ObjectMeta.Labels
	if tmplLabels[labelSourceUUID] != "vm-uuid-001" {
		t.Errorf("expected template source UUID label, got %s", tmplLabels[labelSourceUUID])
	}

	// RunStrategy.
	if *result.Spec.RunStrategy != kubevirtv1.RunStrategyHalted {
		t.Errorf("expected RunStrategyHalted")
	}
}

func TestBuild_CPUTopology(t *testing.T) {
	vm := singleDiskLinuxVM()
	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	cpu := result.Spec.Template.Spec.Domain.CPU
	if cpu.Sockets != 2 {
		t.Errorf("expected 2 sockets, got %d", cpu.Sockets)
	}
	if cpu.Cores != 4 {
		t.Errorf("expected 4 cores, got %d", cpu.Cores)
	}
	if cpu.Threads != 1 {
		t.Errorf("expected 1 thread, got %d", cpu.Threads)
	}
}

func TestBuild_CPUThreadsDefault(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.NumThreadsPerCore = 0
	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	if result.Spec.Template.Spec.Domain.CPU.Threads != 1 {
		t.Errorf("expected threads default to 1, got %d",
			result.Spec.Template.Spec.Domain.CPU.Threads)
	}
}

func TestBuild_Memory(t *testing.T) {
	vm := singleDiskLinuxVM()
	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	mem := result.Spec.Template.Spec.Domain.Memory
	if mem == nil || mem.Guest == nil {
		t.Fatal("expected memory.guest to be set")
	}
	// 4 GiB
	expected := int64(4 * 1024 * 1024 * 1024)
	if mem.Guest.Value() != expected {
		t.Errorf("expected memory %d, got %d", expected, mem.Guest.Value())
	}
}

func TestBuild_FirmwareLegacy(t *testing.T) {
	vm := singleDiskLinuxVM()
	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	fw := result.Spec.Template.Spec.Domain.Firmware
	if fw.Bootloader.BIOS == nil {
		t.Error("expected BIOS bootloader for LEGACY")
	}
	if fw.Bootloader.EFI != nil {
		t.Error("expected no EFI for LEGACY")
	}
}

func TestBuild_FirmwareUEFI(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.BootConfig.BootType = "UEFI"

	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	fw := result.Spec.Template.Spec.Domain.Firmware
	if fw.Bootloader.EFI == nil {
		t.Fatal("expected EFI bootloader for UEFI")
	}
	if *fw.Bootloader.EFI.SecureBoot != false {
		t.Error("expected SecureBoot=false for UEFI")
	}
	if fw.Bootloader.BIOS != nil {
		t.Error("expected no BIOS for UEFI")
	}
}

func TestBuild_FirmwareSecureBoot(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.BootConfig.BootType = testBootTypeSecureBoot

	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	fw := result.Spec.Template.Spec.Domain.Firmware
	if fw.Bootloader.EFI == nil {
		t.Fatal("expected EFI bootloader for SECURE_BOOT")
	}
	if *fw.Bootloader.EFI.SecureBoot != true {
		t.Error("expected SecureBoot=true for SECURE_BOOT")
	}

	// SMM must be enabled.
	features := result.Spec.Template.Spec.Domain.Features
	if features == nil || features.SMM == nil || features.SMM.Enabled == nil {
		t.Fatal("expected SMM feature for SECURE_BOOT")
	}
	if *features.SMM.Enabled != true {
		t.Error("expected SMM.Enabled=true for SECURE_BOOT")
	}
}

func TestBuild_FirmwareNilBootConfig(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.BootConfig = nil

	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	fw := result.Spec.Template.Spec.Domain.Firmware
	if fw.Bootloader.BIOS == nil {
		t.Error("expected BIOS bootloader when BootConfig is nil")
	}
}

func TestBuild_MachineQ35(t *testing.T) {
	vm := singleDiskLinuxVM()
	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	if result.Spec.Template.Spec.Domain.Machine.Type != "q35" {
		t.Errorf("expected q35, got %s",
			result.Spec.Template.Spec.Domain.Machine.Type)
	}
}

func TestBuild_MachinePC(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.MachineType = "PC"

	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	if result.Spec.Template.Spec.Domain.Machine.Type != "pc" {
		t.Errorf("expected pc, got %s",
			result.Spec.Template.Spec.Domain.Machine.Type)
	}
}

func TestBuild_BootOrderOnDisk(t *testing.T) {
	vm := singleDiskLinuxVM()
	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	disks := result.Spec.Template.Spec.Domain.Devices.Disks
	if len(disks) != 1 {
		t.Fatalf("expected 1 disk, got %d", len(disks))
	}
	if disks[0].BootOrder == nil || *disks[0].BootOrder != 1 {
		t.Error("expected bootOrder=1 on boot disk")
	}
}

func TestBuild_MultiDiskWindowsCDROM(t *testing.T) {
	vm := &nutanix.VM{
		ExtID:             "vm-uuid-002",
		Name:              "Windows Server 2022",
		NumSockets:        4,
		NumCoresPerSocket: 2,
		NumThreadsPerCore: 2,
		MemorySizeBytes:   16 * 1024 * 1024 * 1024,
		MachineType:       "Q35",
		BootConfig: &nutanix.BootConfig{
			BootType:  "UEFI",
			BootOrder: []string{"DISK"},
		},
		Disks: []nutanix.Disk{
			{
				ExtID:      "disk-win-0",
				DeviceType: "DISK",
				DiskAddress: &nutanix.DiskAddress{
					BusType: "SCSI",
					Index:   0,
				},
				DiskSizeBytes: 100 * 1024 * 1024 * 1024,
			},
			{
				ExtID:      "cdrom-0",
				DeviceType: "CDROM",
				DiskAddress: &nutanix.DiskAddress{
					BusType: "IDE",
					Index:   0,
				},
			},
			{
				ExtID:      "disk-win-1",
				DeviceType: "DISK",
				DiskAddress: &nutanix.DiskAddress{
					BusType: "SCSI",
					Index:   1,
				},
				DiskSizeBytes: 200 * 1024 * 1024 * 1024,
			},
		},
		Nics: []nutanix.NIC{
			{
				ExtID:      "nic-win-0",
				MacAddress: "00:50:56:dd:ee:ff",
				NetworkRef: &nutanix.Reference{ExtID: "subnet-aaa"},
			},
		},
	}

	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "target",
		PVCNames:  []string{"win-disk-0", "win-disk-2"},
	})

	// Name sanitized (spaces -> hyphens, lowercase).
	if result.Name != "windows-server-2022" {
		t.Errorf("expected windows-server-2022, got %s", result.Name)
	}

	// 2 disks (CDROM skipped).
	disks := result.Spec.Template.Spec.Domain.Devices.Disks
	if len(disks) != 2 {
		t.Fatalf("expected 2 disks (CDROM skipped), got %d", len(disks))
	}

	// First disk gets boot order.
	if disks[0].BootOrder == nil || *disks[0].BootOrder != 1 {
		t.Error("expected bootOrder=1 on first disk")
	}
	// Second disk no boot order.
	if disks[1].BootOrder != nil {
		t.Error("expected no bootOrder on second disk")
	}

	// Both disks use SCSI bus.
	for i, d := range disks {
		if d.Disk.Bus != kubevirtv1.DiskBusSCSI {
			t.Errorf("disk %d: expected SCSI bus, got %s",
				i, d.Disk.Bus)
		}
	}

	// 2 volumes.
	volumes := result.Spec.Template.Spec.Volumes
	if len(volumes) != 2 {
		t.Fatalf("expected 2 volumes, got %d", len(volumes))
	}
	if volumes[0].PersistentVolumeClaim.ClaimName != "win-disk-0" {
		t.Errorf("expected PVC win-disk-0, got %s",
			volumes[0].PersistentVolumeClaim.ClaimName)
	}
	if volumes[1].PersistentVolumeClaim.ClaimName != "win-disk-2" {
		t.Errorf("expected PVC win-disk-2, got %s",
			volumes[1].PersistentVolumeClaim.ClaimName)
	}

	// UEFI firmware.
	if result.Spec.Template.Spec.Domain.Firmware.Bootloader.EFI == nil {
		t.Error("expected EFI bootloader")
	}
}

func TestBuild_IDEDisk(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.Disks[0].DiskAddress.BusType = "IDE"

	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	disks := result.Spec.Template.Spec.Domain.Devices.Disks
	if disks[0].Disk.Bus != kubevirtv1.DiskBusSATA {
		t.Errorf("expected SATA bus for IDE disk, got %s",
			disks[0].Disk.Bus)
	}
}

func TestBuild_SATADisk(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.Disks[0].DiskAddress.BusType = "SATA"

	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	if result.Spec.Template.Spec.Domain.Devices.Disks[0].Disk.Bus != kubevirtv1.DiskBusSATA {
		t.Errorf("expected SATA bus for SATA disk")
	}
}

func TestBuild_PCIDisk(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.Disks[0].DiskAddress.BusType = "PCI"

	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	if result.Spec.Template.Spec.Domain.Devices.Disks[0].Disk.Bus != kubevirtv1.DiskBusSCSI {
		t.Errorf("expected SCSI bus for PCI disk")
	}
}

func TestBuild_DefaultBusVirtio(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.Disks[0].DiskAddress.BusType = "UNKNOWN"

	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	if result.Spec.Template.Spec.Domain.Devices.Disks[0].Disk.Bus != kubevirtv1.DiskBusVirtio {
		t.Errorf("expected virtio bus for unknown bus type")
	}
}

func TestBuild_NilDiskAddress(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.Disks[0].DiskAddress = nil

	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	if result.Spec.Template.Spec.Domain.Devices.Disks[0].Disk.Bus != kubevirtv1.DiskBusVirtio {
		t.Errorf("expected virtio bus when DiskAddress is nil")
	}
}

func TestBuild_PodNetwork_Masquerade_NoMAC(t *testing.T) {
	vm := singleDiskLinuxVM()
	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	ifaces := result.Spec.Template.Spec.Domain.Devices.Interfaces
	if len(ifaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(ifaces))
	}
	if ifaces[0].Masquerade == nil {
		t.Error("expected masquerade binding for pod network")
	}
	if ifaces[0].Bridge != nil {
		t.Error("expected no bridge binding for pod network")
	}
	// MAC should NOT be set for masquerade.
	if ifaces[0].MacAddress != "" {
		t.Errorf("expected no MAC on masquerade, got %s", ifaces[0].MacAddress)
	}

	nets := result.Spec.Template.Spec.Networks
	if len(nets) != 1 {
		t.Fatalf("expected 1 network, got %d", len(nets))
	}
	if nets[0].Pod == nil {
		t.Error("expected Pod network source")
	}
}

func TestBuild_MultusNetwork_Bridge_MACPreserved(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.Nics[0].NetworkRef = &nutanix.Reference{ExtID: "subnet-bbb"}
	vm.Nics[0].MacAddress = "aa:bb:cc:dd:ee:ff"

	result := Build(vm, multusNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	ifaces := result.Spec.Template.Spec.Domain.Devices.Interfaces
	if ifaces[0].Bridge == nil {
		t.Error("expected bridge binding for multus network")
	}
	if ifaces[0].Masquerade != nil {
		t.Error("expected no masquerade for multus network")
	}
	if ifaces[0].MacAddress != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("expected MAC preserved, got %s", ifaces[0].MacAddress)
	}

	nets := result.Spec.Template.Spec.Networks
	if nets[0].Multus == nil {
		t.Fatal("expected Multus network source")
	}
	if nets[0].Multus.NetworkName != "networking/my-nad" {
		t.Errorf("expected networking/my-nad, got %s",
			nets[0].Multus.NetworkName)
	}
}

func TestBuild_MultiNIC(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.Nics = append(vm.Nics, nutanix.NIC{
		ExtID:      "nic-002",
		MacAddress: "11:22:33:44:55:66",
		NetworkRef: &nutanix.Reference{ExtID: "subnet-bbb"},
	})

	nmap := &vmav1alpha1.NetworkMap{
		Spec: vmav1alpha1.NetworkMapSpec{
			Map: []vmav1alpha1.NetworkPair{
				{
					Source:      vmav1alpha1.NetworkSource{ID: "subnet-aaa"},
					Destination: vmav1alpha1.NetworkDestination{Type: vmav1alpha1.NetworkDestinationPod},
				},
				{
					Source: vmav1alpha1.NetworkSource{ID: "subnet-bbb"},
					Destination: vmav1alpha1.NetworkDestination{
						Type: vmav1alpha1.NetworkDestinationMultus,
						Name: "storage-net",
					},
				},
			},
		},
	}

	result := Build(vm, nmap, simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	ifaces := result.Spec.Template.Spec.Domain.Devices.Interfaces
	nets := result.Spec.Template.Spec.Networks
	if len(ifaces) != 2 || len(nets) != 2 {
		t.Fatalf("expected 2 interfaces and networks, got %d/%d",
			len(ifaces), len(nets))
	}

	// First NIC: pod/masquerade.
	if ifaces[0].Masquerade == nil {
		t.Error("expected masquerade on first NIC")
	}
	// Second NIC: multus/bridge.
	if ifaces[1].Bridge == nil {
		t.Error("expected bridge on second NIC")
	}
	if ifaces[1].MacAddress != "11:22:33:44:55:66" {
		t.Errorf("expected MAC on bridge NIC, got %s", ifaces[1].MacAddress)
	}
}

func TestBuild_UnmappedNIC_DefaultsMasquerade(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.Nics[0].NetworkRef = &nutanix.Reference{ExtID: "unknown-subnet"}

	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	ifaces := result.Spec.Template.Spec.Domain.Devices.Interfaces
	if ifaces[0].Masquerade == nil {
		t.Error("expected masquerade for unmapped NIC")
	}
}

func TestBuild_NetworkMapByName(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.Nics[0].NetworkRef = &nutanix.Reference{
		Name: "prod-vlan-100",
	}

	nmap := &vmav1alpha1.NetworkMap{
		Spec: vmav1alpha1.NetworkMapSpec{
			Map: []vmav1alpha1.NetworkPair{
				{
					Source: vmav1alpha1.NetworkSource{Name: "prod-vlan-100"},
					Destination: vmav1alpha1.NetworkDestination{
						Type: vmav1alpha1.NetworkDestinationMultus,
						Name: "vlan100",
					},
				},
			},
		},
	}

	result := Build(vm, nmap, simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	if result.Spec.Template.Spec.Domain.Devices.Interfaces[0].Bridge == nil {
		t.Error("expected bridge when matched by name")
	}
}

func TestBuild_NoPVCNames(t *testing.T) {
	vm := singleDiskLinuxVM()
	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
	})

	volumes := result.Spec.Template.Spec.Volumes
	if len(volumes) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(volumes))
	}
	// Volume should exist but with no PVC set.
	if volumes[0].PersistentVolumeClaim != nil {
		t.Error("expected no PVC when PVCNames is empty")
	}
}

func TestBuild_NameCollision(t *testing.T) {
	vm := singleDiskLinuxVM()
	existing := map[string]bool{"linux-web-01": true}

	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace:     "ns",
		PVCNames:      []string{"pvc-0"},
		ExistingNames: existing,
	})

	if result.Name != "linux-web-01-2" {
		t.Errorf("expected linux-web-01-2, got %s", result.Name)
	}
}

func TestBuild_NoBootOrderWithoutDiskInBootConfig(t *testing.T) {
	vm := singleDiskLinuxVM()
	vm.BootConfig.BootOrder = []string{"NETWORK"}

	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	disks := result.Spec.Template.Spec.Domain.Devices.Disks
	if disks[0].BootOrder != nil {
		t.Error("expected no bootOrder when DISK not in boot order")
	}
}

func TestBuild_DiskVolumeNameConsistency(t *testing.T) {
	vm := singleDiskLinuxVM()
	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	disks := result.Spec.Template.Spec.Domain.Devices.Disks
	volumes := result.Spec.Template.Spec.Volumes

	if disks[0].Name != volumes[0].Name {
		t.Errorf("disk name %s doesn't match volume name %s",
			disks[0].Name, volumes[0].Name)
	}
}

func TestBuild_InterfaceNetworkNameConsistency(t *testing.T) {
	vm := singleDiskLinuxVM()
	result := Build(vm, simpleNetworkMap(), simpleStorageMap(), BuildOptions{
		Namespace: "ns",
		PVCNames:  []string{"pvc-0"},
	})

	ifaces := result.Spec.Template.Spec.Domain.Devices.Interfaces
	nets := result.Spec.Template.Spec.Networks

	if ifaces[0].Name != nets[0].Name {
		t.Errorf("interface name %s doesn't match network name %s",
			ifaces[0].Name, nets[0].Name)
	}
}
