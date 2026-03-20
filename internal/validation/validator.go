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
	"fmt"
	"strings"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	deviceTypeCDROM = "CDROM"
	busTypeIDE      = "IDE"
	nicTypeNetFunc  = "NETWORK_FUNCTION_NIC"
	nicTypeDirect   = "DIRECT_NIC"
	cdiCRDName      = "datavolumes.cdi.kubevirt.io"
	kubevirtCRDName = "virtualmachines.kubevirt.io"
)

var crdGVK = schema.GroupVersionKind{
	Group:   "apiextensions.k8s.io",
	Version: "v1",
	Kind:    "CustomResourceDefinition",
}

// ValidationOptions configures the validation engine.
type ValidationOptions struct {
	// NetworkMap is the network mapping configuration.
	NetworkMap *vmav1alpha1.NetworkMap

	// StorageMap is the storage mapping configuration.
	StorageMap *vmav1alpha1.StorageMap

	// ExistingVMs is a set of existing KubeVirt VM names for collision detection.
	ExistingVMs map[string]bool

	// ExistingMACs maps MAC addresses (lowercase) to owner VM names.
	ExistingMACs map[string]string

	// TargetNamespace is the namespace where migrated VMs will be created.
	TargetNamespace string

	// Client is a Kubernetes client for target-side checks. Nil skips them.
	Client client.Reader
}

// Validate runs all validation rules against a VM and returns concerns.
func Validate(
	ctx context.Context, vm *nutanix.VM, opts ValidationOptions,
) []vmav1alpha1.Concern {
	var concerns []vmav1alpha1.Concern

	// Source-side rules (always run).
	concerns = append(concerns, checkUnmappedStorage(vm, opts.StorageMap)...)
	concerns = append(concerns, checkUnmappedNetwork(vm, opts.NetworkMap)...)
	concerns = append(concerns, checkGPU(vm)...)
	concerns = append(concerns, checkVolumeGroupDisks(vm)...)
	concerns = append(concerns, checkNICTypes(vm)...)
	concerns = append(concerns, checkMACConflicts(vm, opts.ExistingMACs)...)
	concerns = append(concerns, checkIDEDisks(vm)...)

	// Target-side rules (skip if client is nil).
	if opts.Client != nil {
		concerns = append(concerns,
			checkNamespace(ctx, opts.Client, opts.TargetNamespace)...)
		concerns = append(concerns,
			checkStorageClasses(ctx, opts.Client, vm, opts.StorageMap)...)
		concerns = append(concerns, checkMultusNADs(
			ctx, opts.Client, vm, opts.NetworkMap, opts.TargetNamespace,
		)...)
		concerns = append(concerns,
			checkCDIInstalled(ctx, opts.Client)...)
		concerns = append(concerns,
			checkKubeVirtInstalled(ctx, opts.Client)...)
	}

	return concerns
}

// HasErrors returns true if any concern has Error category.
func HasErrors(concerns []vmav1alpha1.Concern) bool {
	for _, c := range concerns {
		if c.Category == vmav1alpha1.ConcernCategoryError {
			return true
		}
	}
	return false
}

// --- Source-side rules ---

func checkUnmappedStorage(
	vm *nutanix.VM, storageMap *vmav1alpha1.StorageMap,
) []vmav1alpha1.Concern {
	var concerns []vmav1alpha1.Concern
	seen := make(map[string]bool)

	for _, disk := range vm.Disks {
		if strings.ToUpper(disk.DeviceType) == deviceTypeCDROM {
			continue
		}
		if disk.BackingInfo == nil ||
			disk.BackingInfo.StorageContainerRef == nil {
			continue // handled by checkVolumeGroupDisks
		}
		ref := disk.BackingInfo.StorageContainerRef
		key := ref.ExtID + "/" + ref.Name
		if seen[key] {
			continue
		}
		seen[key] = true

		if !isStorageMapped(ref, storageMap) {
			concerns = append(concerns, vmav1alpha1.Concern{
				Category: vmav1alpha1.ConcernCategoryError,
				Message: fmt.Sprintf(
					"storage container %q (ID: %s) is not mapped in StorageMap",
					ref.Name, ref.ExtID,
				),
			})
		}
	}
	return concerns
}

func isStorageMapped(
	ref *nutanix.Reference, storageMap *vmav1alpha1.StorageMap,
) bool {
	if ref == nil || storageMap == nil {
		return false
	}
	for _, pair := range storageMap.Spec.Map {
		if pair.Source.ID != "" && pair.Source.ID == ref.ExtID {
			return true
		}
		if pair.Source.Name != "" && pair.Source.Name == ref.Name {
			return true
		}
	}
	return false
}

func checkUnmappedNetwork(
	vm *nutanix.VM, networkMap *vmav1alpha1.NetworkMap,
) []vmav1alpha1.Concern {
	var concerns []vmav1alpha1.Concern
	seen := make(map[string]bool)

	for _, nic := range vm.Nics {
		if nic.NetworkRef == nil {
			continue
		}
		key := nic.NetworkRef.ExtID + "/" + nic.NetworkRef.Name
		if seen[key] {
			continue
		}
		seen[key] = true

		if !isNetworkMapped(nic.NetworkRef, networkMap) {
			concerns = append(concerns, vmav1alpha1.Concern{
				Category: vmav1alpha1.ConcernCategoryError,
				Message: fmt.Sprintf(
					"subnet %q (ID: %s) is not mapped in NetworkMap",
					nic.NetworkRef.Name, nic.NetworkRef.ExtID,
				),
			})
		}
	}
	return concerns
}

func isNetworkMapped(
	ref *nutanix.Reference, networkMap *vmav1alpha1.NetworkMap,
) bool {
	if ref == nil || networkMap == nil {
		return false
	}
	for _, pair := range networkMap.Spec.Map {
		if pair.Source.ID != "" && pair.Source.ID == ref.ExtID {
			return true
		}
		if pair.Source.Name != "" && pair.Source.Name == ref.Name {
			return true
		}
	}
	return false
}

func checkGPU(vm *nutanix.VM) []vmav1alpha1.Concern {
	if len(vm.Gpus) > 0 {
		return []vmav1alpha1.Concern{{
			Category: vmav1alpha1.ConcernCategoryWarning,
			Message: fmt.Sprintf(
				"VM has %d GPU(s) attached; "+
					"GPU passthrough requires manual configuration on target",
				len(vm.Gpus),
			),
		}}
	}
	return nil
}

func checkVolumeGroupDisks(vm *nutanix.VM) []vmav1alpha1.Concern {
	var concerns []vmav1alpha1.Concern
	for _, disk := range vm.Disks {
		if strings.ToUpper(disk.DeviceType) == deviceTypeCDROM {
			continue
		}
		if disk.BackingInfo == nil ||
			disk.BackingInfo.StorageContainerRef == nil {
			concerns = append(concerns, vmav1alpha1.Concern{
				Category: vmav1alpha1.ConcernCategoryError,
				Message: fmt.Sprintf(
					"disk %s has no storage container reference "+
						"(possible Volume Group); "+
						"Volume Groups cannot be migrated",
					disk.ExtID,
				),
			})
		}
	}
	return concerns
}

func checkNICTypes(vm *nutanix.VM) []vmav1alpha1.Concern {
	var concerns []vmav1alpha1.Concern
	for _, nic := range vm.Nics {
		switch strings.ToUpper(nic.NicType) {
		case nicTypeNetFunc:
			concerns = append(concerns, vmav1alpha1.Concern{
				Category: vmav1alpha1.ConcernCategoryError,
				Message: fmt.Sprintf(
					"NIC %s is a %s which cannot be migrated",
					nic.ExtID, nicTypeNetFunc,
				),
			})
		case nicTypeDirect:
			concerns = append(concerns, vmav1alpha1.Concern{
				Category: vmav1alpha1.ConcernCategoryWarning,
				Message: fmt.Sprintf(
					"NIC %s is a %s (passthrough); "+
						"requires SR-IOV on target cluster",
					nic.ExtID, nicTypeDirect,
				),
			})
		}
	}
	return concerns
}

func checkMACConflicts(
	vm *nutanix.VM, existingMACs map[string]string,
) []vmav1alpha1.Concern {
	if len(existingMACs) == 0 {
		return nil
	}
	var concerns []vmav1alpha1.Concern
	for _, nic := range vm.Nics {
		if nic.MacAddress == "" {
			continue
		}
		mac := strings.ToLower(nic.MacAddress)
		if owner, found := existingMACs[mac]; found {
			concerns = append(concerns, vmav1alpha1.Concern{
				Category: vmav1alpha1.ConcernCategoryWarning,
				Message: fmt.Sprintf(
					"MAC address %s on NIC %s conflicts with VM %q",
					nic.MacAddress, nic.ExtID, owner,
				),
			})
		}
	}
	return concerns
}

func checkIDEDisks(vm *nutanix.VM) []vmav1alpha1.Concern {
	var concerns []vmav1alpha1.Concern
	for _, disk := range vm.Disks {
		if strings.ToUpper(disk.DeviceType) == deviceTypeCDROM {
			continue
		}
		if disk.DiskAddress != nil &&
			strings.ToUpper(disk.DiskAddress.BusType) == busTypeIDE {
			concerns = append(concerns, vmav1alpha1.Concern{
				Category: vmav1alpha1.ConcernCategoryInfo,
				Message: fmt.Sprintf(
					"disk %s uses IDE bus; "+
						"will be mapped to SATA on KubeVirt",
					disk.ExtID,
				),
			})
		}
	}
	return concerns
}

// --- Target-side rules ---

func checkNamespace(
	ctx context.Context, c client.Reader, namespace string,
) []vmav1alpha1.Concern {
	ns := &corev1.Namespace{}
	err := c.Get(ctx, types.NamespacedName{Name: namespace}, ns)
	if apierrors.IsNotFound(err) {
		return []vmav1alpha1.Concern{{
			Category: vmav1alpha1.ConcernCategoryError,
			Message: fmt.Sprintf(
				"target namespace %q does not exist", namespace,
			),
		}}
	}
	return nil
}

func checkStorageClasses(
	ctx context.Context,
	c client.Reader,
	vm *nutanix.VM,
	storageMap *vmav1alpha1.StorageMap,
) []vmav1alpha1.Concern {
	if storageMap == nil {
		return nil
	}

	// Collect unique StorageClasses needed by this VM's disks.
	needed := make(map[string]bool)
	for _, disk := range vm.Disks {
		if strings.ToUpper(disk.DeviceType) == deviceTypeCDROM {
			continue
		}
		if disk.BackingInfo == nil ||
			disk.BackingInfo.StorageContainerRef == nil {
			continue
		}
		ref := disk.BackingInfo.StorageContainerRef
		for _, pair := range storageMap.Spec.Map {
			matched := (pair.Source.ID != "" &&
				pair.Source.ID == ref.ExtID) ||
				(pair.Source.Name != "" &&
					pair.Source.Name == ref.Name)
			if matched {
				needed[pair.Destination.StorageClass] = true
			}
		}
	}

	var concerns []vmav1alpha1.Concern
	for sc := range needed {
		obj := &storagev1.StorageClass{}
		err := c.Get(ctx, types.NamespacedName{Name: sc}, obj)
		if apierrors.IsNotFound(err) {
			concerns = append(concerns, vmav1alpha1.Concern{
				Category: vmav1alpha1.ConcernCategoryError,
				Message: fmt.Sprintf(
					"StorageClass %q not found in cluster", sc,
				),
			})
		}
	}
	return concerns
}

func checkMultusNADs(
	ctx context.Context,
	c client.Reader,
	vm *nutanix.VM,
	networkMap *vmav1alpha1.NetworkMap,
	targetNS string,
) []vmav1alpha1.Concern {
	if networkMap == nil {
		return nil
	}

	var concerns []vmav1alpha1.Concern
	checked := make(map[string]bool)

	for _, nic := range vm.Nics {
		if nic.NetworkRef == nil {
			continue
		}
		dest := findNetworkDestination(nic.NetworkRef, networkMap)
		if dest == nil ||
			dest.Type != vmav1alpha1.NetworkDestinationMultus {
			continue
		}

		ns := dest.Namespace
		if ns == "" {
			ns = targetNS
		}
		key := ns + "/" + dest.Name
		if checked[key] {
			continue
		}
		checked[key] = true

		nad := &unstructured.Unstructured{}
		nad.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "k8s.cni.cncf.io",
			Version: "v1",
			Kind:    "NetworkAttachmentDefinition",
		})
		err := c.Get(ctx, types.NamespacedName{
			Namespace: ns, Name: dest.Name,
		}, nad)
		if err != nil {
			concerns = append(concerns, vmav1alpha1.Concern{
				Category: vmav1alpha1.ConcernCategoryError,
				Message: fmt.Sprintf(
					"Multus NetworkAttachmentDefinition %q "+
						"not found in namespace %q",
					dest.Name, ns,
				),
			})
		}
	}
	return concerns
}

func findNetworkDestination(
	ref *nutanix.Reference, networkMap *vmav1alpha1.NetworkMap,
) *vmav1alpha1.NetworkDestination {
	if ref == nil || networkMap == nil {
		return nil
	}
	for i, pair := range networkMap.Spec.Map {
		if pair.Source.ID != "" && pair.Source.ID == ref.ExtID {
			return &networkMap.Spec.Map[i].Destination
		}
		if pair.Source.Name != "" && pair.Source.Name == ref.Name {
			return &networkMap.Spec.Map[i].Destination
		}
	}
	return nil
}

func checkCDIInstalled(
	ctx context.Context, c client.Reader,
) []vmav1alpha1.Concern {
	crd := &unstructured.Unstructured{}
	crd.SetGroupVersionKind(crdGVK)
	err := c.Get(
		ctx, types.NamespacedName{Name: cdiCRDName}, crd,
	)
	if err != nil {
		return []vmav1alpha1.Concern{{
			Category: vmav1alpha1.ConcernCategoryError,
			Message: "CDI (Containerized Data Importer) " +
				"is not installed in the target cluster",
		}}
	}
	return nil
}

func checkKubeVirtInstalled(
	ctx context.Context, c client.Reader,
) []vmav1alpha1.Concern {
	crd := &unstructured.Unstructured{}
	crd.SetGroupVersionKind(crdGVK)
	err := c.Get(
		ctx, types.NamespacedName{Name: kubevirtCRDName}, crd,
	)
	if err != nil {
		return []vmav1alpha1.Concern{{
			Category: vmav1alpha1.ConcernCategoryError,
			Message: "KubeVirt is not installed " +
				"in the target cluster",
		}}
	}
	return nil
}
