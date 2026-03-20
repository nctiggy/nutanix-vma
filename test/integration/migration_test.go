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

package integration

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
)

var _ = Describe("Migration Controller", func() {

	const (
		migTimeout  = 60 * time.Second
		migInterval = 500 * time.Millisecond

		migTestNS       = "default"
		migTargetNS     = "mig-target-ns"
		migSCName       = "mig-test-sc"
		migProviderName = "mig-integ-provider"
		migSecretName   = "mig-integ-creds"
		migNetMapName   = "mig-integ-netmap"
		migStorMapName  = "mig-integ-stormap"
	)

	BeforeEach(func() {
		// Create target namespace
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: migTargetNS},
		}
		if err := k8sClient.Create(ctx, ns); err != nil {
			Expect(apierrors.IsAlreadyExists(err)).To(BeTrue(),
				"unexpected error creating namespace: %v", err)
		}

		// Create StorageClass
		sc := &storagev1.StorageClass{
			ObjectMeta:  metav1.ObjectMeta{Name: migSCName},
			Provisioner: "kubernetes.io/no-provisioner",
		}
		if err := k8sClient.Create(ctx, sc); err != nil {
			Expect(apierrors.IsAlreadyExists(err)).To(BeTrue(),
				"unexpected error creating StorageClass: %v", err)
		}

		// Create credential secret pointing to mock server
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      migSecretName,
				Namespace: migTestNS,
			},
			Data: map[string][]byte{
				"username": []byte("admin"),
				"password": []byte("nutanix/4u"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		// Create Provider
		provider := &vmav1alpha1.NutanixProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      migProviderName,
				Namespace: migTestNS,
			},
			Spec: vmav1alpha1.NutanixProviderSpec{
				URL:                mockServer.URL(),
				SecretRef:          corev1.LocalObjectReference{Name: migSecretName},
				InsecureSkipVerify: true,
			},
		}
		Expect(k8sClient.Create(ctx, provider)).To(Succeed())

		// Create NetworkMap (subnet -> pod network)
		netMap := &vmav1alpha1.NetworkMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      migNetMapName,
				Namespace: migTestNS,
			},
			Spec: vmav1alpha1.NetworkMapSpec{
				ProviderRef: corev1.LocalObjectReference{
					Name: migProviderName,
				},
				Map: []vmav1alpha1.NetworkPair{{
					Source: vmav1alpha1.NetworkSource{
						ID: "subnet-uuid-001",
					},
					Destination: vmav1alpha1.NetworkDestination{
						Type: vmav1alpha1.NetworkDestinationPod,
					},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, netMap)).To(Succeed())

		// Create StorageMap
		storMap := &vmav1alpha1.StorageMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      migStorMapName,
				Namespace: migTestNS,
			},
			Spec: vmav1alpha1.StorageMapSpec{
				ProviderRef: corev1.LocalObjectReference{
					Name: migProviderName,
				},
				Map: []vmav1alpha1.StoragePair{{
					Source: vmav1alpha1.StorageSource{
						ID: "sc-uuid-001",
					},
					Destination: vmav1alpha1.StorageDestination{
						StorageClass: migSCName,
						VolumeMode:   corev1.PersistentVolumeFilesystem,
						AccessMode:   corev1.ReadWriteOnce,
					},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, storMap)).To(Succeed())
	})

	AfterEach(func() {
		// Cleanup migrations (remove finalizer first)
		migrations := &vmav1alpha1.MigrationList{}
		if err := k8sClient.List(ctx, migrations); err == nil {
			for i := range migrations.Items {
				m := &migrations.Items[i]
				m.Finalizers = nil
				_ = k8sClient.Update(ctx, m)
				_ = k8sClient.Delete(ctx, m)
			}
		}

		// Cleanup plans
		plans := &vmav1alpha1.MigrationPlanList{}
		if err := k8sClient.List(ctx, plans); err == nil {
			for i := range plans.Items {
				_ = k8sClient.Delete(ctx, &plans.Items[i])
			}
		}

		// Cleanup provider (remove finalizer first)
		provider := &vmav1alpha1.NutanixProvider{}
		key := types.NamespacedName{
			Name:      migProviderName,
			Namespace: migTestNS,
		}
		if err := k8sClient.Get(ctx, key, provider); err == nil {
			provider.Finalizers = nil
			_ = k8sClient.Update(ctx, provider)
			_ = k8sClient.Delete(ctx, provider)
		}

		// Cleanup maps
		netMap := &vmav1alpha1.NetworkMap{}
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name:      migNetMapName,
			Namespace: migTestNS,
		}, netMap); err == nil {
			_ = k8sClient.Delete(ctx, netMap)
		}
		storMap := &vmav1alpha1.StorageMap{}
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name:      migStorMapName,
			Namespace: migTestNS,
		}, storMap); err == nil {
			_ = k8sClient.Delete(ctx, storMap)
		}

		// Cleanup secret
		secret := &corev1.Secret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name:      migSecretName,
			Namespace: migTestNS,
		}, secret); err == nil {
			_ = k8sClient.Delete(ctx, secret)
		}

		// Cleanup KubeVirt VMs in target namespace
		vmList := &kubevirtv1.VirtualMachineList{}
		if err := k8sClient.List(ctx, vmList); err == nil {
			for i := range vmList.Items {
				_ = k8sClient.Delete(ctx, &vmList.Items[i])
			}
		}

		// Cleanup DataVolumes in target namespace
		dvList := &cdiv1beta1.DataVolumeList{}
		if err := k8sClient.List(ctx, dvList); err == nil {
			for i := range dvList.Items {
				_ = k8sClient.Delete(ctx, &dvList.Items[i])
			}
		}
	})

	Context("cold migration of a Linux VM", func() {
		It("should complete end-to-end", func() {
			// Create Plan
			plan := &vmav1alpha1.MigrationPlan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mig-cold-plan",
					Namespace: migTestNS,
				},
				Spec: vmav1alpha1.MigrationPlanSpec{
					ProviderRef: corev1.LocalObjectReference{
						Name: migProviderName,
					},
					TargetNamespace: migTargetNS,
					NetworkMapRef: corev1.LocalObjectReference{
						Name: migNetMapName,
					},
					StorageMapRef: corev1.LocalObjectReference{
						Name: migStorMapName,
					},
					VMs: []vmav1alpha1.PlanVM{
						{ID: "vm-uuid-001"},
					},
					TargetPowerState: vmav1alpha1.TargetPowerStateRunning,
				},
			}
			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			// Create Migration
			migration := &vmav1alpha1.Migration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mig-cold-test",
					Namespace: migTestNS,
				},
				Spec: vmav1alpha1.MigrationSpec{
					PlanRef: corev1.LocalObjectReference{
						Name: "mig-cold-plan",
					},
				},
			}
			Expect(k8sClient.Create(ctx, migration)).To(Succeed())

			// Wait for DataVolumes to be created (VM reaches ImportDisks)
			Eventually(func(g Gomega) {
				m := &vmav1alpha1.Migration{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      "mig-cold-test",
					Namespace: migTestNS,
				}, m)).To(Succeed())
				g.Expect(m.Status.VMs).NotTo(BeEmpty())
				g.Expect(m.Status.VMs[0].DataVolumeNames).
					NotTo(BeEmpty())
			}, migTimeout, migInterval).Should(Succeed())

			// Get the DataVolume names
			m := &vmav1alpha1.Migration{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      "mig-cold-test",
				Namespace: migTestNS,
			}, m)).To(Succeed())

			// Mark all DataVolumes as Succeeded
			for _, dvName := range m.Status.VMs[0].DataVolumeNames {
				Eventually(func(g Gomega) {
					dv := &cdiv1beta1.DataVolume{}
					g.Expect(k8sClient.Get(ctx,
						types.NamespacedName{
							Name:      dvName,
							Namespace: migTargetNS,
						}, dv)).To(Succeed())

					dv.Status.Phase = cdiv1beta1.Succeeded
					g.Expect(k8sClient.Status().Update(
						ctx, dv)).To(Succeed())
				}, migTimeout, migInterval).Should(Succeed())
			}

			// Wait for migration to complete
			Eventually(func(g Gomega) {
				m := &vmav1alpha1.Migration{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      "mig-cold-test",
					Namespace: migTestNS,
				}, m)).To(Succeed())
				g.Expect(m.Status.Phase).To(
					Equal(vmav1alpha1.MigrationPhaseCompleted))
			}, migTimeout, migInterval).Should(Succeed())

			// Verify KubeVirt VM exists in target namespace
			kvVM := &kubevirtv1.VirtualMachine{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      "test-vm-linux",
				Namespace: migTargetNS,
			}, kvVM)).To(Succeed())

			// Verify source UUID label
			Expect(kvVM.Labels).To(HaveKeyWithValue(
				"vma.nutanix.io/source-vm-uuid", "vm-uuid-001"))

			// Verify RunStrategy is Always (Running target)
			Expect(kvVM.Spec.RunStrategy).NotTo(BeNil())
			Expect(*kvVM.Spec.RunStrategy).To(
				Equal(kubevirtv1.RunStrategyAlways))

			// Verify VM migration status
			m = &vmav1alpha1.Migration{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      "mig-cold-test",
				Namespace: migTestNS,
			}, m)).To(Succeed())

			Expect(m.Status.VMs).To(HaveLen(1))
			Expect(m.Status.VMs[0].Phase).To(
				Equal(vmav1alpha1.VMPhaseCompleted))
			Expect(m.Status.VMs[0].SnapshotUUID).NotTo(BeEmpty())
			Expect(m.Status.VMs[0].ImageUUIDs).NotTo(BeEmpty())
			Expect(m.Status.VMs[0].DataVolumeNames).NotTo(BeEmpty())
			Expect(m.Status.Started).NotTo(BeNil())
			Expect(m.Status.Completed).NotTo(BeNil())

			// Verify Ready condition
			hasReady := false
			for _, c := range m.Status.Conditions {
				if c.Type == "Ready" &&
					c.Status == metav1.ConditionTrue {
					hasReady = true
				}
			}
			Expect(hasReady).To(BeTrue(),
				"Expected Ready=True condition")
		})
	})
})
