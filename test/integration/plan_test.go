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

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
)

var _ = Describe("Plan Controller", func() {

	const (
		planTimeout  = 30 * time.Second
		planInterval = 250 * time.Millisecond

		planTestNS       = "default"
		planTargetNS     = "plan-target-ns"
		planSCName       = "plan-test-sc"
		planProviderName = "plan-integ-provider"
		planSecretName   = "plan-integ-creds"
		planNetMapName   = "plan-integ-netmap"
		planStorMapName  = "plan-integ-stormap"
	)

	// Shared setup: create Provider, Secret, maps, target NS, StorageClass
	BeforeEach(func() {
		// Create target namespace (ignore if already exists or terminating)
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: planTargetNS},
		}
		if err := k8sClient.Create(ctx, ns); err != nil {
			Expect(apierrors.IsAlreadyExists(err)).To(BeTrue(),
				"unexpected error creating namespace: %v", err)
		}

		// Create StorageClass (ignore if already exists)
		sc := &storagev1.StorageClass{
			ObjectMeta:  metav1.ObjectMeta{Name: planSCName},
			Provisioner: "kubernetes.io/no-provisioner",
		}
		if err := k8sClient.Create(ctx, sc); err != nil {
			Expect(apierrors.IsAlreadyExists(err)).To(BeTrue(),
				"unexpected error creating StorageClass: %v", err)
		}

		// Create credential secret pointing to mock server
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planSecretName,
				Namespace: planTestNS,
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
				Name:      planProviderName,
				Namespace: planTestNS,
			},
			Spec: vmav1alpha1.NutanixProviderSpec{
				URL:                mockServer.URL(),
				SecretRef:          corev1.LocalObjectReference{Name: planSecretName},
				InsecureSkipVerify: true,
			},
		}
		Expect(k8sClient.Create(ctx, provider)).To(Succeed())

		// Create NetworkMap (both subnets -> pod network)
		netMap := &vmav1alpha1.NetworkMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planNetMapName,
				Namespace: planTestNS,
			},
			Spec: vmav1alpha1.NetworkMapSpec{
				ProviderRef: corev1.LocalObjectReference{
					Name: planProviderName,
				},
				Map: []vmav1alpha1.NetworkPair{
					{
						Source: vmav1alpha1.NetworkSource{
							ID: "subnet-uuid-001",
						},
						Destination: vmav1alpha1.NetworkDestination{
							Type: vmav1alpha1.NetworkDestinationPod,
						},
					},
					{
						Source: vmav1alpha1.NetworkSource{
							ID: "subnet-uuid-002",
						},
						Destination: vmav1alpha1.NetworkDestination{
							Type: vmav1alpha1.NetworkDestinationPod,
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, netMap)).To(Succeed())

		// Create StorageMap
		storMap := &vmav1alpha1.StorageMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planStorMapName,
				Namespace: planTestNS,
			},
			Spec: vmav1alpha1.StorageMapSpec{
				ProviderRef: corev1.LocalObjectReference{
					Name: planProviderName,
				},
				Map: []vmav1alpha1.StoragePair{{
					Source: vmav1alpha1.StorageSource{
						ID: "sc-uuid-001",
					},
					Destination: vmav1alpha1.StorageDestination{
						StorageClass: planSCName,
					},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, storMap)).To(Succeed())
	})

	AfterEach(func() {
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
			Name:      planProviderName,
			Namespace: planTestNS,
		}
		if err := k8sClient.Get(ctx, key, provider); err == nil {
			provider.Finalizers = nil
			_ = k8sClient.Update(ctx, provider)
			_ = k8sClient.Delete(ctx, provider)
		}

		// Cleanup maps
		netMap := &vmav1alpha1.NetworkMap{}
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name:      planNetMapName,
			Namespace: planTestNS,
		}, netMap); err == nil {
			_ = k8sClient.Delete(ctx, netMap)
		}
		storMap := &vmav1alpha1.StorageMap{}
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name:      planStorMapName,
			Namespace: planTestNS,
		}, storMap); err == nil {
			_ = k8sClient.Delete(ctx, storMap)
		}

		// Cleanup secret
		secret := &corev1.Secret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name:      planSecretName,
			Namespace: planTestNS,
		}, secret); err == nil {
			_ = k8sClient.Delete(ctx, secret)
		}

		// Note: namespace and StorageClass are NOT deleted between tests
		// to avoid Terminating state conflicts on recreation.
	})

	Context("valid plan with a Linux VM", func() {
		It("should reach Ready phase", func() {
			plan := &vmav1alpha1.MigrationPlan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "plan-valid",
					Namespace: planTestNS,
				},
				Spec: vmav1alpha1.MigrationPlanSpec{
					ProviderRef: corev1.LocalObjectReference{
						Name: planProviderName,
					},
					TargetNamespace: planTargetNS,
					NetworkMapRef: corev1.LocalObjectReference{
						Name: planNetMapName,
					},
					StorageMapRef: corev1.LocalObjectReference{
						Name: planStorMapName,
					},
					VMs: []vmav1alpha1.PlanVM{
						{ID: "vm-uuid-001"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			// Wait for Ready phase
			Eventually(func(g Gomega) {
				p := &vmav1alpha1.MigrationPlan{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      "plan-valid",
					Namespace: planTestNS,
				}, p)).To(Succeed())
				g.Expect(p.Status.Phase).To(
					Equal(vmav1alpha1.PlanPhaseReady))
			}, planTimeout, planInterval).Should(Succeed())

			// Verify VM status
			p := &vmav1alpha1.MigrationPlan{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      "plan-valid",
				Namespace: planTestNS,
			}, p)).To(Succeed())

			Expect(p.Status.VMs).To(HaveLen(1))
			Expect(p.Status.VMs[0].ID).To(Equal("vm-uuid-001"))
			Expect(p.Status.VMs[0].Name).To(
				Equal("test-vm-linux"))

			// Verify Validated condition
			hasValidated := false
			for _, c := range p.Status.Conditions {
				if c.Type == "Validated" &&
					c.Status == metav1.ConditionTrue {
					hasValidated = true
				}
			}
			Expect(hasValidated).To(BeTrue(),
				"Expected Validated=True condition")
		})
	})

	Context("plan with a GPU VM", func() {
		It("should reach Ready with Warning concerns", func() {
			plan := &vmav1alpha1.MigrationPlan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "plan-gpu",
					Namespace: planTestNS,
				},
				Spec: vmav1alpha1.MigrationPlanSpec{
					ProviderRef: corev1.LocalObjectReference{
						Name: planProviderName,
					},
					TargetNamespace: planTargetNS,
					NetworkMapRef: corev1.LocalObjectReference{
						Name: planNetMapName,
					},
					StorageMapRef: corev1.LocalObjectReference{
						Name: planStorMapName,
					},
					VMs: []vmav1alpha1.PlanVM{
						{ID: "vm-uuid-003"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			// Wait for Ready phase (GPU is Warning, not Error)
			Eventually(func(g Gomega) {
				p := &vmav1alpha1.MigrationPlan{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      "plan-gpu",
					Namespace: planTestNS,
				}, p)).To(Succeed())
				g.Expect(p.Status.Phase).To(
					Equal(vmav1alpha1.PlanPhaseReady))
			}, planTimeout, planInterval).Should(Succeed())

			// Verify GPU warning
			p := &vmav1alpha1.MigrationPlan{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      "plan-gpu",
				Namespace: planTestNS,
			}, p)).To(Succeed())

			Expect(p.Status.VMs).To(HaveLen(1))
			hasGPUWarning := false
			for _, c := range p.Status.VMs[0].Concerns {
				if c.Category == vmav1alpha1.ConcernCategoryWarning {
					hasGPUWarning = true
				}
			}
			Expect(hasGPUWarning).To(BeTrue(),
				"Expected GPU warning concern")
		})
	})

	Context("plan with unmapped storage", func() {
		It("should reach Error phase", func() {
			// Create a separate StorageMap without the correct mapping
			badStorMap := &vmav1alpha1.StorageMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "plan-bad-stormap",
					Namespace: planTestNS,
				},
				Spec: vmav1alpha1.StorageMapSpec{
					ProviderRef: corev1.LocalObjectReference{
						Name: planProviderName,
					},
					Map: []vmav1alpha1.StoragePair{{
						Source: vmav1alpha1.StorageSource{
							ID: "nonexistent-container",
						},
						Destination: vmav1alpha1.StorageDestination{
							StorageClass: planSCName,
						},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, badStorMap)).To(Succeed())

			plan := &vmav1alpha1.MigrationPlan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "plan-unmapped",
					Namespace: planTestNS,
				},
				Spec: vmav1alpha1.MigrationPlanSpec{
					ProviderRef: corev1.LocalObjectReference{
						Name: planProviderName,
					},
					TargetNamespace: planTargetNS,
					NetworkMapRef: corev1.LocalObjectReference{
						Name: planNetMapName,
					},
					StorageMapRef: corev1.LocalObjectReference{
						Name: "plan-bad-stormap",
					},
					VMs: []vmav1alpha1.PlanVM{
						{ID: "vm-uuid-001"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			// Wait for Error phase
			Eventually(func(g Gomega) {
				p := &vmav1alpha1.MigrationPlan{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      "plan-unmapped",
					Namespace: planTestNS,
				}, p)).To(Succeed())
				g.Expect(p.Status.Phase).To(
					Equal(vmav1alpha1.PlanPhaseError))
			}, planTimeout, planInterval).Should(Succeed())

			// Verify error concerns
			p := &vmav1alpha1.MigrationPlan{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      "plan-unmapped",
				Namespace: planTestNS,
			}, p)).To(Succeed())

			Expect(p.Status.VMs).To(HaveLen(1))
			hasStorageError := false
			for _, c := range p.Status.VMs[0].Concerns {
				if c.Category == vmav1alpha1.ConcernCategoryError {
					hasStorageError = true
				}
			}
			Expect(hasStorageError).To(BeTrue(),
				"Expected unmapped storage Error concern")

			// Cleanup bad stormap
			_ = k8sClient.Delete(ctx, badStorMap)
		})
	})
})
