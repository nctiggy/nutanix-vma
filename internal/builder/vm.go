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
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

const (
	labelSourceUUID = "vma.nutanix.io/source-vm-uuid"
	labelSourceName = "vma.nutanix.io/source-vm-name"
)

// BuildOptions configures the VM builder.
type BuildOptions struct {
	Namespace     string
	PVCNames      []string
	ExistingNames map[string]bool
}

// Build translates a Nutanix VM into a KubeVirt VirtualMachine.
func Build(
	vm *nutanix.VM,
	networkMap *vmav1alpha1.NetworkMap,
	storageMap *vmav1alpha1.StorageMap,
	opts BuildOptions,
) *kubevirtv1.VirtualMachine {
	name := SanitizeName(vm.Name, opts.ExistingNames)

	labels := map[string]string{
		labelSourceUUID: vm.ExtID,
		labelSourceName: SanitizeName(vm.Name, nil),
	}

	disks, volumes := buildDisksAndVolumes(vm, opts.PVCNames)
	interfaces, networks := buildInterfacesAndNetworks(vm, networkMap)
	runStrategy := kubevirtv1.RunStrategyHalted

	return &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: opts.Namespace,
			Labels:    labels,
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			RunStrategy: &runStrategy,
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					Domain: kubevirtv1.DomainSpec{
						CPU:      buildCPU(vm),
						Memory:   buildMemory(vm),
						Machine:  buildMachine(vm),
						Firmware: buildFirmware(vm),
						Features: buildFeatures(vm),
						Resources: kubevirtv1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: *resource.NewQuantity(
									vm.MemorySizeBytes, resource.BinarySI,
								),
							},
						},
						Devices: kubevirtv1.Devices{
							Disks:      disks,
							Interfaces: interfaces,
						},
					},
					Volumes:  volumes,
					Networks: networks,
				},
			},
		},
	}
}

func buildCPU(vm *nutanix.VM) *kubevirtv1.CPU {
	threads := uint32(vm.NumThreadsPerCore)
	if threads == 0 {
		threads = 1
	}
	return &kubevirtv1.CPU{
		Sockets: uint32(vm.NumSockets),
		Cores:   uint32(vm.NumCoresPerSocket),
		Threads: threads,
	}
}

func buildMemory(vm *nutanix.VM) *kubevirtv1.Memory {
	mib := vm.MemorySizeBytes / (1024 * 1024)
	q := resource.NewQuantity(mib*1024*1024, resource.BinarySI)
	return &kubevirtv1.Memory{
		Guest: q,
	}
}

func buildMachine(vm *nutanix.VM) *kubevirtv1.Machine {
	switch strings.ToUpper(vm.MachineType) {
	case "PC":
		return &kubevirtv1.Machine{Type: "pc"}
	default:
		return &kubevirtv1.Machine{Type: "q35"}
	}
}

func buildFirmware(vm *nutanix.VM) *kubevirtv1.Firmware {
	if vm.BootConfig == nil {
		return &kubevirtv1.Firmware{
			Bootloader: &kubevirtv1.Bootloader{BIOS: &kubevirtv1.BIOS{}},
		}
	}

	switch strings.ToUpper(vm.BootConfig.BootType) {
	case "UEFI":
		sb := false
		return &kubevirtv1.Firmware{
			Bootloader: &kubevirtv1.Bootloader{
				EFI: &kubevirtv1.EFI{SecureBoot: &sb},
			},
		}
	case "SECURE_BOOT":
		sb := true
		return &kubevirtv1.Firmware{
			Bootloader: &kubevirtv1.Bootloader{
				EFI: &kubevirtv1.EFI{SecureBoot: &sb},
			},
		}
	default: // LEGACY or unrecognized
		return &kubevirtv1.Firmware{
			Bootloader: &kubevirtv1.Bootloader{BIOS: &kubevirtv1.BIOS{}},
		}
	}
}

func buildFeatures(vm *nutanix.VM) *kubevirtv1.Features {
	if vm.BootConfig != nil &&
		strings.ToUpper(vm.BootConfig.BootType) == "SECURE_BOOT" {
		enabled := true
		return &kubevirtv1.Features{
			SMM: &kubevirtv1.FeatureState{Enabled: &enabled},
		}
	}
	return nil
}

// mapDiskBus converts a Nutanix disk bus type to a KubeVirt DiskBus.
func mapDiskBus(busType string) kubevirtv1.DiskBus {
	switch strings.ToUpper(busType) {
	case "SCSI", "PCI":
		return kubevirtv1.DiskBusSCSI
	case "IDE", "SATA":
		return kubevirtv1.DiskBusSATA
	default:
		return kubevirtv1.DiskBusVirtio
	}
}

// isBootDisk returns true if "DISK" is in the boot order.
func isBootDisk(vm *nutanix.VM) bool {
	if vm.BootConfig == nil {
		return false
	}
	for _, entry := range vm.BootConfig.BootOrder {
		if strings.ToUpper(entry) == "DISK" {
			return true
		}
	}
	return false
}

func buildDisksAndVolumes(
	vm *nutanix.VM, pvcNames []string,
) ([]kubevirtv1.Disk, []kubevirtv1.Volume) {
	var disks []kubevirtv1.Disk
	var volumes []kubevirtv1.Volume

	bootDisk := isBootDisk(vm)
	bootOrderAssigned := false
	pvcIdx := 0

	for i, d := range vm.Disks {
		// Skip CDROM entries.
		if strings.ToUpper(d.DeviceType) == "CDROM" {
			continue
		}

		volName := fmt.Sprintf("disk-%d", i)

		bus := kubevirtv1.DiskBusVirtio
		if d.DiskAddress != nil {
			bus = mapDiskBus(d.DiskAddress.BusType)
		}

		disk := kubevirtv1.Disk{
			Name: volName,
			DiskDevice: kubevirtv1.DiskDevice{
				Disk: &kubevirtv1.DiskTarget{
					Bus: bus,
				},
			},
		}

		// Set boot order on the first data disk when DISK is in boot order.
		if bootDisk && !bootOrderAssigned {
			order := uint(1)
			disk.BootOrder = &order
			bootOrderAssigned = true
		}

		disks = append(disks, disk)

		// Build volume referencing PVC.
		vol := kubevirtv1.Volume{
			Name: volName,
		}
		if pvcIdx < len(pvcNames) {
			vol.VolumeSource = kubevirtv1.VolumeSource{
				PersistentVolumeClaim: &kubevirtv1.PersistentVolumeClaimVolumeSource{
					PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvcNames[pvcIdx],
					},
				},
			}
		}
		pvcIdx++
		volumes = append(volumes, vol)
	}

	return disks, volumes
}

// findNetworkMapping looks up the network destination for a Nutanix subnet.
func findNetworkMapping(
	subnetRef *nutanix.Reference,
	networkMap *vmav1alpha1.NetworkMap,
) *vmav1alpha1.NetworkDestination {
	if subnetRef == nil || networkMap == nil {
		return nil
	}
	for _, pair := range networkMap.Spec.Map {
		if pair.Source.ID != "" && pair.Source.ID == subnetRef.ExtID {
			return &pair.Destination
		}
		if pair.Source.Name != "" && pair.Source.Name == subnetRef.Name {
			return &pair.Destination
		}
	}
	return nil
}

func buildInterfacesAndNetworks(
	vm *nutanix.VM,
	networkMap *vmav1alpha1.NetworkMap,
) ([]kubevirtv1.Interface, []kubevirtv1.Network) {
	interfaces := make([]kubevirtv1.Interface, 0, len(vm.Nics))
	networks := make([]kubevirtv1.Network, 0, len(vm.Nics))

	for i, nic := range vm.Nics {
		netName := fmt.Sprintf("net-%d", i)
		dest := findNetworkMapping(nic.NetworkRef, networkMap)

		iface := kubevirtv1.Interface{
			Name: netName,
		}
		net := kubevirtv1.Network{
			Name: netName,
		}

		if dest != nil && dest.Type == vmav1alpha1.NetworkDestinationMultus {
			// Bridge interface -- preserve MAC address.
			iface.InterfaceBindingMethod = kubevirtv1.InterfaceBindingMethod{
				Bridge: &kubevirtv1.InterfaceBridge{},
			}
			iface.MacAddress = nic.MacAddress

			nadName := dest.Name
			if dest.Namespace != "" {
				nadName = dest.Namespace + "/" + dest.Name
			}
			net.NetworkSource = kubevirtv1.NetworkSource{
				Multus: &kubevirtv1.MultusNetwork{
					NetworkName: nadName,
				},
			}
		} else {
			// Default: masquerade (pod network) -- no MAC.
			iface.InterfaceBindingMethod = kubevirtv1.InterfaceBindingMethod{
				Masquerade: &kubevirtv1.InterfaceMasquerade{},
			}
			net.NetworkSource = kubevirtv1.NetworkSource{
				Pod: &kubevirtv1.PodNetwork{},
			}
		}

		interfaces = append(interfaces, iface)
		networks = append(networks, net)
	}

	return interfaces, networks
}
