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
	"os"
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

var _ = Describe("Warm Migration E2E", Ordered, func() {
	const (
		warmTimeout  = 30 * time.Minute
		warmInterval = 10 * time.Second

		warmProviderName   = "e2e-warm-provider"
		warmSecretName     = "e2e-warm-nutanix-creds"
		warmNetworkMapName = "e2e-warm-network-map"
		warmStorageMapName = "e2e-warm-storage-map"
		warmPlanName       = "e2e-warm-plan"
	)

	var (
		testVM        nutanix.VM
		migrationName string
		ns            string
	)

	BeforeAll(func() {
		if os.Getenv("NUTANIX_WARM_E2E") != "true" {
			Skip("Skipping warm migration E2E: " +
				"NUTANIX_WARM_E2E != true")
		}

		ns = e2eConfig.Namespace

		By("discovering test VM for warm migration")
		testVM = findTestVM()
		_, _ = fmt.Fprintf(GinkgoWriter,
			"Using test VM for warm migration: %s (%s)\n",
			testVM.Name, testVM.ExtID)

		By("creating warm migration resources")
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      warmSecretName,
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

		provider := &vmav1alpha1.NutanixProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      warmProviderName,
				Namespace: ns,
			},
			Spec: vmav1alpha1.NutanixProviderSpec{
				URL: e2eConfig.PrismURL,
				SecretRef: corev1.LocalObjectReference{
					Name: warmSecretName,
				},
				InsecureSkipVerify: e2eConfig.Insecure,
			},
		}
		err = k8sClient.Create(ctx, provider)
		if !isAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}

		Eventually(func(g Gomega) {
			p := &vmav1alpha1.NutanixProvider{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: warmProviderName, Namespace: ns,
			}, p)).To(Succeed())
			g.Expect(string(p.Status.Phase)).To(
				Equal(string(vmav1alpha1.ProviderPhaseConnected)))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		networkMap := buildNetworkMap(ns,
			warmNetworkMapName, warmProviderName, testVM)
		err = k8sClient.Create(ctx, networkMap)
		if !isAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}

		storageMap := buildStorageMap(ns,
			warmStorageMapName, warmProviderName, testVM)
		err = k8sClient.Create(ctx, storageMap)
		if !isAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}

		plan := &vmav1alpha1.MigrationPlan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      warmPlanName,
				Namespace: ns,
			},
			Spec: vmav1alpha1.MigrationPlanSpec{
				ProviderRef: corev1.LocalObjectReference{
					Name: warmProviderName,
				},
				TargetNamespace: ns,
				Type:            vmav1alpha1.MigrationTypeWarm,
				NetworkMapRef: corev1.LocalObjectReference{
					Name: warmNetworkMapName,
				},
				StorageMapRef: corev1.LocalObjectReference{
					Name: warmStorageMapName,
				},
				VMs: []vmav1alpha1.PlanVM{
					{ID: testVM.ExtID, Name: testVM.Name},
				},
				MaxInFlight:      1,
				TargetPowerState: vmav1alpha1.TargetPowerStateRunning,
				WarmConfig: &vmav1alpha1.WarmConfig{
					PrecopyInterval:  "1m",
					MaxPrecopyRounds: 3,
				},
			},
		}
		err = k8sClient.Create(ctx, plan)
		if !isAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}

		Eventually(func(g Gomega) {
			p := &vmav1alpha1.MigrationPlan{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: warmPlanName, Namespace: ns,
			}, p)).To(Succeed())
			g.Expect(string(p.Status.Phase)).To(
				Equal(string(vmav1alpha1.PlanPhaseReady)))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())
	})

	AfterAll(func() {
		if os.Getenv("NUTANIX_WARM_E2E") != "true" {
			return
		}
		By("cleaning up warm E2E resources")
		cleanupWarmE2E(ns, migrationName, testVM)
	})

	It("should complete a warm migration with cutover", func() {
		By("creating warm Migration CR with future cutover")
		migrationName = fmt.Sprintf("e2e-warm-%d",
			time.Now().Unix())
		// Cutover 2 minutes from now to allow at least one
		// precopy round
		cutoverTime := metav1.NewTime(
			time.Now().Add(2 * time.Minute))
		migration := &vmav1alpha1.Migration{
			ObjectMeta: metav1.ObjectMeta{
				Name:      migrationName,
				Namespace: ns,
			},
			Spec: vmav1alpha1.MigrationSpec{
				PlanRef: corev1.LocalObjectReference{
					Name: warmPlanName,
				},
				Cutover: &cutoverTime,
			},
		}
		Expect(k8sClient.Create(ctx, migration)).To(Succeed())

		By("waiting for warm migration to complete")
		Eventually(func(g Gomega) {
			m := &vmav1alpha1.Migration{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: migrationName, Namespace: ns,
			}, m)).To(Succeed())

			_, _ = fmt.Fprintf(GinkgoWriter,
				"Warm migration phase: %s\n",
				m.Status.Phase)
			for _, vm := range m.Status.VMs {
				_, _ = fmt.Fprintf(GinkgoWriter,
					"  VM %s: phase=%s\n",
					vm.ID, vm.Phase)
				if vm.Warm != nil {
					_, _ = fmt.Fprintf(GinkgoWriter,
						"    precopy rounds=%d\n",
						vm.Warm.PrecopyRounds)
				}
			}

			g.Expect(string(m.Status.Phase)).To(
				Equal(string(
					vmav1alpha1.MigrationPhaseCompleted)),
				"Warm migration phase: %s",
				m.Status.Phase)
		}, warmTimeout, warmInterval).Should(Succeed())

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
	})
})

// cleanupWarmE2E removes warm migration E2E resources.
func cleanupWarmE2E(
	ns, migrationName string, testVM nutanix.VM,
) {
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

	if migrationName != "" {
		cleanupNutanixResources(ns, migrationName, testVM)
		_ = k8sClient.Delete(ctx, &vmav1alpha1.Migration{
			ObjectMeta: metav1.ObjectMeta{
				Name: migrationName, Namespace: ns,
			},
		})
	}

	for _, obj := range []client.Object{
		&vmav1alpha1.MigrationPlan{
			ObjectMeta: metav1.ObjectMeta{
				Name: "e2e-warm-plan", Namespace: ns,
			},
		},
		&vmav1alpha1.NetworkMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "e2e-warm-network-map", Namespace: ns,
			},
		},
		&vmav1alpha1.StorageMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "e2e-warm-storage-map", Namespace: ns,
			},
		},
		&vmav1alpha1.NutanixProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name: "e2e-warm-provider", Namespace: ns,
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "e2e-warm-nutanix-creds", Namespace: ns,
			},
		},
	} {
		_ = k8sClient.Delete(ctx, obj)
	}
}
