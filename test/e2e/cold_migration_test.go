//go:build e2e
// +build e2e

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

package e2e

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

var _ = Describe("Cold Migration E2E", Ordered, func() {
	const (
		e2eTimeout  = 20 * time.Minute
		e2eInterval = 5 * time.Second

		providerName   = "e2e-provider"
		secretName     = "e2e-nutanix-creds"
		networkMapName = "e2e-network-map"
		storageMapName = "e2e-storage-map"
		planName       = "e2e-cold-plan"
	)

	var (
		testVM       nutanix.VM
		migrationName string
		ns           string
	)

	BeforeAll(func() {
		ns = e2eConfig.Namespace

		By("discovering test VM from Nutanix")
		testVM = findTestVM()
		_, _ = fmt.Fprintf(GinkgoWriter,
			"Using test VM: %s (%s)\n",
			testVM.Name, testVM.ExtID)

		By("creating Nutanix credentials Secret")
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: ns,
			},
			StringData: map[string]string{
				"username": e2eConfig.Username,
				"password": e2eConfig.Password,
			},
		}
		err := k8sClient.Create(ctx, secret)
		if !isAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}

		By("creating NutanixProvider")
		provider := &vmav1alpha1.NutanixProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      providerName,
				Namespace: ns,
			},
			Spec: vmav1alpha1.NutanixProviderSpec{
				URL: e2eConfig.PrismURL,
				SecretRef: corev1.LocalObjectReference{
					Name: secretName,
				},
				InsecureSkipVerify: e2eConfig.Insecure,
			},
		}
		err = k8sClient.Create(ctx, provider)
		if !isAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}

		By("waiting for Provider to connect")
		Eventually(func(g Gomega) {
			p := &vmav1alpha1.NutanixProvider{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: providerName, Namespace: ns,
			}, p)).To(Succeed())
			g.Expect(string(p.Status.Phase)).To(
				Equal(string(vmav1alpha1.ProviderPhaseConnected)),
				"Provider phase: %s", p.Status.Phase)
		}, 2*time.Minute, e2eInterval).Should(Succeed())

		By("creating NetworkMap")
		networkMap := buildNetworkMap(ns, networkMapName,
			providerName, testVM)
		err = k8sClient.Create(ctx, networkMap)
		if !isAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}

		By("creating StorageMap")
		storageMap := buildStorageMap(ns, storageMapName,
			providerName, testVM)
		err = k8sClient.Create(ctx, storageMap)
		if !isAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}

		By("creating MigrationPlan")
		plan := &vmav1alpha1.MigrationPlan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName,
				Namespace: ns,
			},
			Spec: vmav1alpha1.MigrationPlanSpec{
				ProviderRef: corev1.LocalObjectReference{
					Name: providerName,
				},
				TargetNamespace: ns,
				Type:            vmav1alpha1.MigrationTypeCold,
				NetworkMapRef: corev1.LocalObjectReference{
					Name: networkMapName,
				},
				StorageMapRef: corev1.LocalObjectReference{
					Name: storageMapName,
				},
				VMs: []vmav1alpha1.PlanVM{
					{ID: testVM.ExtID, Name: testVM.Name},
				},
				MaxInFlight:      1,
				TargetPowerState: vmav1alpha1.TargetPowerStateRunning,
			},
		}
		err = k8sClient.Create(ctx, plan)
		if !isAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}

		By("waiting for Plan to reach Ready")
		Eventually(func(g Gomega) {
			p := &vmav1alpha1.MigrationPlan{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: planName, Namespace: ns,
			}, p)).To(Succeed())
			g.Expect(string(p.Status.Phase)).To(
				Equal(string(vmav1alpha1.PlanPhaseReady)),
				"Plan phase: %s", p.Status.Phase)
		}, 2*time.Minute, e2eInterval).Should(Succeed())
	})

	AfterAll(func() {
		By("cleaning up E2E resources")
		cleanupE2E(ns, migrationName, testVM)
	})

	It("should complete a cold migration end-to-end", func() {
		By("creating Migration CR")
		migrationName = fmt.Sprintf("e2e-cold-%d",
			time.Now().Unix())
		migration := &vmav1alpha1.Migration{
			ObjectMeta: metav1.ObjectMeta{
				Name:      migrationName,
				Namespace: ns,
			},
			Spec: vmav1alpha1.MigrationSpec{
				PlanRef: corev1.LocalObjectReference{
					Name: planName,
				},
			},
		}
		Expect(k8sClient.Create(ctx, migration)).To(Succeed())

		By("waiting for Migration to complete")
		Eventually(func(g Gomega) {
			m := &vmav1alpha1.Migration{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: migrationName, Namespace: ns,
			}, m)).To(Succeed())

			_, _ = fmt.Fprintf(GinkgoWriter,
				"Migration phase: %s\n", m.Status.Phase)
			for _, vm := range m.Status.VMs {
				_, _ = fmt.Fprintf(GinkgoWriter,
					"  VM %s: phase=%s\n",
					vm.ID, vm.Phase)
			}

			g.Expect(string(m.Status.Phase)).To(
				Equal(string(
					vmav1alpha1.MigrationPhaseCompleted)),
				"Migration phase: %s", m.Status.Phase)
		}, e2eTimeout, e2eInterval).Should(Succeed())

		By("verifying KubeVirt VM exists")
		vmList := &kubevirtv1.VirtualMachineList{}
		Expect(k8sClient.List(ctx, vmList,
			client.InNamespace(ns),
			client.MatchingLabels{
				"vma.nutanix.io/source-vm-uuid": testVM.ExtID,
			},
		)).To(Succeed())
		Expect(vmList.Items).NotTo(BeEmpty(),
			"Expected KubeVirt VM with source UUID label")

		kvVM := vmList.Items[0]
		_, _ = fmt.Fprintf(GinkgoWriter,
			"KubeVirt VM created: %s\n", kvVM.Name)

		By("verifying migration status fields")
		m := &vmav1alpha1.Migration{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: migrationName, Namespace: ns,
		}, m)).To(Succeed())

		Expect(m.Status.Started).NotTo(BeNil(),
			"Migration should have Started timestamp")
		Expect(m.Status.Completed).NotTo(BeNil(),
			"Migration should have Completed timestamp")
		Expect(m.Status.VMs).To(HaveLen(1))

		vmStatus := m.Status.VMs[0]
		Expect(string(vmStatus.Phase)).To(
			Equal(string(vmav1alpha1.VMPhaseCompleted)))
		Expect(vmStatus.SnapshotUUID).NotTo(BeEmpty(),
			"SnapshotUUID should be tracked")
		Expect(vmStatus.ImageUUIDs).NotTo(BeEmpty(),
			"ImageUUIDs should be tracked")
		Expect(vmStatus.DataVolumeNames).NotTo(BeEmpty(),
			"DataVolumeNames should be tracked")
	})
})

// findTestVM discovers the test VM from Nutanix.
// Uses NUTANIX_TEST_VM_ID or NUTANIX_TEST_VM_NAME to find a specific
// VM. Falls back to the first VM in the inventory.
func findTestVM() nutanix.VM {
	if e2eConfig.TestVMID != "" {
		vm, err := nxClient.GetVM(ctx, e2eConfig.TestVMID)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(),
			"Failed to get VM by ID: %s",
			e2eConfig.TestVMID)
		return *vm
	}

	vms, err := nxClient.ListVMs(ctx)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(),
		"Failed to list VMs")
	ExpectWithOffset(1, vms).NotTo(BeEmpty(),
		"No VMs found in Nutanix inventory")

	if e2eConfig.TestVMName != "" {
		for _, vm := range vms {
			if strings.Contains(
				strings.ToLower(vm.Name),
				strings.ToLower(e2eConfig.TestVMName),
			) {
				return vm
			}
		}
		Fail(fmt.Sprintf(
			"VM matching name %q not found",
			e2eConfig.TestVMName))
	}

	// Fall back to first VM
	return vms[0]
}

// buildNetworkMap creates a NetworkMap from the test VM's NICs.
// Uses NUTANIX_SUBNET_ID/NAME from env, or extracts from the VM.
func buildNetworkMap(
	ns, name, providerName string, vm nutanix.VM,
) *vmav1alpha1.NetworkMap {
	pairs := make([]vmav1alpha1.NetworkPair, 0, len(vm.Nics))

	seen := make(map[string]bool)
	for _, nic := range vm.Nics {
		if nic.NetworkRef == nil {
			continue
		}
		key := nic.NetworkRef.ExtID
		if seen[key] {
			continue
		}
		seen[key] = true

		source := vmav1alpha1.NetworkSource{}
		if e2eConfig.SubnetID != "" {
			source.ID = e2eConfig.SubnetID
		} else {
			source.ID = nic.NetworkRef.ExtID
		}
		if e2eConfig.SubnetName != "" {
			source.Name = e2eConfig.SubnetName
		}

		pairs = append(pairs, vmav1alpha1.NetworkPair{
			Source: source,
			Destination: vmav1alpha1.NetworkDestination{
				Type: vmav1alpha1.NetworkDestinationPod,
			},
		})
	}

	// Ensure at least one mapping exists
	if len(pairs) == 0 {
		pairs = append(pairs, vmav1alpha1.NetworkPair{
			Source: vmav1alpha1.NetworkSource{
				ID:   e2eConfig.SubnetID,
				Name: e2eConfig.SubnetName,
			},
			Destination: vmav1alpha1.NetworkDestination{
				Type: vmav1alpha1.NetworkDestinationPod,
			},
		})
	}

	return &vmav1alpha1.NetworkMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: vmav1alpha1.NetworkMapSpec{
			ProviderRef: corev1.LocalObjectReference{
				Name: providerName,
			},
			Map: pairs,
		},
	}
}

// buildStorageMap creates a StorageMap from the test VM's disks.
// Uses NUTANIX_STORAGE_ID/NAME from env, or extracts from the VM.
func buildStorageMap(
	ns, name, providerName string, vm nutanix.VM,
) *vmav1alpha1.StorageMap {
	pairs := make([]vmav1alpha1.StoragePair, 0, len(vm.Disks))

	seen := make(map[string]bool)
	for _, disk := range vm.Disks {
		if disk.BackingInfo == nil ||
			disk.BackingInfo.StorageContainerRef == nil {
			continue
		}
		key := disk.BackingInfo.StorageContainerRef.ExtID
		if seen[key] {
			continue
		}
		seen[key] = true

		source := vmav1alpha1.StorageSource{}
		if e2eConfig.StorageID != "" {
			source.ID = e2eConfig.StorageID
		} else {
			source.ID = disk.BackingInfo.StorageContainerRef.ExtID
		}
		if e2eConfig.StorageName != "" {
			source.Name = e2eConfig.StorageName
		}

		pairs = append(pairs, vmav1alpha1.StoragePair{
			Source: source,
			Destination: vmav1alpha1.StorageDestination{
				StorageClass: e2eConfig.StorageClass,
				VolumeMode: corev1.PersistentVolumeFilesystem,
				AccessMode: corev1.ReadWriteOnce,
			},
		})
	}

	// Ensure at least one mapping exists
	if len(pairs) == 0 {
		pairs = append(pairs, vmav1alpha1.StoragePair{
			Source: vmav1alpha1.StorageSource{
				ID:   e2eConfig.StorageID,
				Name: e2eConfig.StorageName,
			},
			Destination: vmav1alpha1.StorageDestination{
				StorageClass: e2eConfig.StorageClass,
				VolumeMode: corev1.PersistentVolumeFilesystem,
				AccessMode: corev1.ReadWriteOnce,
			},
		})
	}

	return &vmav1alpha1.StorageMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: vmav1alpha1.StorageMapSpec{
			ProviderRef: corev1.LocalObjectReference{
				Name: providerName,
			},
			Map: pairs,
		},
	}
}

// cleanupE2E deletes resources created during the E2E test.
// Errors are logged but do not fail the suite.
func cleanupE2E(
	ns, migrationName string, testVM nutanix.VM,
) {
	By("deleting KubeVirt VMs created by migration")
	vmList := &kubevirtv1.VirtualMachineList{}
	if err := k8sClient.List(ctx, vmList,
		client.InNamespace(ns),
		client.MatchingLabels{
			"vma.nutanix.io/source-vm-uuid": testVM.ExtID,
		},
	); err == nil {
		for i := range vmList.Items {
			_ = k8sClient.Delete(ctx, &vmList.Items[i])
		}
	}

	By("deleting Migration CR")
	if migrationName != "" {
		migration := &vmav1alpha1.Migration{
			ObjectMeta: metav1.ObjectMeta{
				Name:      migrationName,
				Namespace: ns,
			},
		}
		_ = k8sClient.Delete(ctx, migration)
	}

	By("deleting Plan, Maps, Provider, Secret")
	for _, obj := range []client.Object{
		&vmav1alpha1.MigrationPlan{
			ObjectMeta: metav1.ObjectMeta{
				Name: "e2e-cold-plan", Namespace: ns,
			},
		},
		&vmav1alpha1.NetworkMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "e2e-network-map", Namespace: ns,
			},
		},
		&vmav1alpha1.StorageMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "e2e-storage-map", Namespace: ns,
			},
		},
		&vmav1alpha1.NutanixProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name: "e2e-provider", Namespace: ns,
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "e2e-nutanix-creds", Namespace: ns,
			},
		},
	} {
		_ = k8sClient.Delete(ctx, obj)
	}

	By("cleaning up Nutanix snapshots and images (best-effort)")
	// The controller's Cleanup phase should have handled this,
	// but clean up manually as a safety net.
	cleanupNutanixResources(ns, migrationName, testVM)
}

// cleanupNutanixResources removes any leftover Nutanix-side
// resources (images, snapshots) from a migration.
func cleanupNutanixResources(
	ns, migrationName string, _ nutanix.VM,
) {
	if migrationName == "" {
		return
	}

	m := &vmav1alpha1.Migration{}
	if err := k8sClient.Get(ctx, types.NamespacedName{
		Name: migrationName, Namespace: ns,
	}, m); err != nil {
		return
	}

	for _, vmStatus := range m.Status.VMs {
		// Delete images
		for _, imageUUID := range vmStatus.ImageUUIDs {
			if imageUUID != "" {
				if err := nxClient.DeleteImage(
					ctx, imageUUID,
				); err != nil {
					_, _ = fmt.Fprintf(GinkgoWriter,
						"Failed to clean up image %s: %v\n",
						imageUUID, err)
				}
			}
		}
		// Delete snapshot
		if vmStatus.SnapshotUUID != "" {
			if err := nxClient.DeleteRecoveryPoint(
				ctx, vmStatus.SnapshotUUID,
			); err != nil {
				_, _ = fmt.Fprintf(GinkgoWriter,
					"Failed to clean up snapshot %s: %v\n",
					vmStatus.SnapshotUUID, err)
			}
		}
	}
}
