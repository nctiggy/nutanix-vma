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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
)

var _ = Describe("Provider Controller", func() {

	const (
		testNamespace = "default"
		timeout       = 30 * time.Second
		interval      = 250 * time.Millisecond
	)

	Context("when a NutanixProvider CR is created with valid credentials", func() {
		var (
			providerName string
			ns           string
		)

		BeforeEach(func() {
			ns = testNamespace
			providerName = "test-provider-valid"

			// Create credential secret pointing to mock server
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mock-nutanix-creds",
					Namespace: ns,
				},
				Data: map[string][]byte{
					"username": []byte("admin"),
					"password": []byte("nutanix/4u"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		})

		AfterEach(func() {
			// Cleanup
			provider := &vmav1alpha1.NutanixProvider{}
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Name: providerName, Namespace: ns,
			}, provider); err == nil {
				// Remove finalizer before deleting
				provider.Finalizers = nil
				_ = k8sClient.Update(ctx, provider)
				_ = k8sClient.Delete(ctx, provider)
			}

			secret := &corev1.Secret{}
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "mock-nutanix-creds", Namespace: ns,
			}, secret); err == nil {
				_ = k8sClient.Delete(ctx, secret)
			}
		})

		It("should connect, fetch inventory, and update status", func() {
			provider := &vmav1alpha1.NutanixProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      providerName,
					Namespace: ns,
				},
				Spec: vmav1alpha1.NutanixProviderSpec{
					URL:                mockServer.URL(),
					SecretRef:          corev1.LocalObjectReference{Name: "mock-nutanix-creds"},
					RefreshInterval:    "5m",
					InsecureSkipVerify: true,
				},
			}
			Expect(k8sClient.Create(ctx, provider)).To(Succeed())

			// Wait for Connected phase
			Eventually(func(g Gomega) {
				p := &vmav1alpha1.NutanixProvider{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: providerName, Namespace: ns,
				}, p)).To(Succeed())
				g.Expect(p.Status.Phase).To(Equal(vmav1alpha1.ProviderPhaseConnected))
			}, timeout, interval).Should(Succeed())

			// Verify VM count from mock fixtures (3 VMs)
			Eventually(func(g Gomega) {
				p := &vmav1alpha1.NutanixProvider{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: providerName, Namespace: ns,
				}, p)).To(Succeed())
				g.Expect(p.Status.VMCount).To(Equal(3))
			}, timeout, interval).Should(Succeed())

			// Verify conditions
			p := &vmav1alpha1.NutanixProvider{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: providerName, Namespace: ns,
			}, p)).To(Succeed())

			hasConnected := false
			hasInventory := false
			for _, c := range p.Status.Conditions {
				if c.Type == "Connected" && c.Status == metav1.ConditionTrue {
					hasConnected = true
				}
				if c.Type == "InventoryReady" && c.Status == metav1.ConditionTrue {
					hasInventory = true
				}
			}
			Expect(hasConnected).To(BeTrue(), "Expected Connected condition")
			Expect(hasInventory).To(BeTrue(), "Expected InventoryReady condition")

			// Verify finalizer was added
			Expect(p.Finalizers).To(ContainElement("vma.nutanix.io/provider-protection"))
		})
	})

	Context("when a NutanixProvider CR has bad credentials", func() {
		var (
			providerName string
			ns           string
		)

		BeforeEach(func() {
			ns = testNamespace
			providerName = "test-provider-bad-creds"

			// Create secret with empty password
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bad-creds",
					Namespace: ns,
				},
				Data: map[string][]byte{
					"username": []byte("admin"),
					"password": []byte(""),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		})

		AfterEach(func() {
			provider := &vmav1alpha1.NutanixProvider{}
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Name: providerName, Namespace: ns,
			}, provider); err == nil {
				provider.Finalizers = nil
				_ = k8sClient.Update(ctx, provider)
				_ = k8sClient.Delete(ctx, provider)
			}

			secret := &corev1.Secret{}
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "bad-creds", Namespace: ns,
			}, secret); err == nil {
				_ = k8sClient.Delete(ctx, secret)
			}
		})

		It("should set Error phase with condition", func() {
			provider := &vmav1alpha1.NutanixProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      providerName,
					Namespace: ns,
				},
				Spec: vmav1alpha1.NutanixProviderSpec{
					URL:       mockServer.URL(),
					SecretRef: corev1.LocalObjectReference{Name: "bad-creds"},
				},
			}
			Expect(k8sClient.Create(ctx, provider)).To(Succeed())

			// Wait for Error phase
			Eventually(func(g Gomega) {
				p := &vmav1alpha1.NutanixProvider{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: providerName, Namespace: ns,
				}, p)).To(Succeed())
				g.Expect(p.Status.Phase).To(Equal(vmav1alpha1.ProviderPhaseError))
			}, timeout, interval).Should(Succeed())

			// Verify Connected=False condition
			p := &vmav1alpha1.NutanixProvider{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: providerName, Namespace: ns,
			}, p)).To(Succeed())

			hasError := false
			for _, c := range p.Status.Conditions {
				if c.Type == "Connected" && c.Status == metav1.ConditionFalse {
					hasError = true
				}
			}
			Expect(hasError).To(BeTrue(), "Expected Connected=False condition for bad credentials")
		})
	})

	Context("when a NutanixProvider references a missing secret", func() {
		var (
			providerName string
			ns           string
		)

		BeforeEach(func() {
			ns = testNamespace
			providerName = "test-provider-no-secret"
		})

		AfterEach(func() {
			provider := &vmav1alpha1.NutanixProvider{}
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Name: providerName, Namespace: ns,
			}, provider); err == nil {
				provider.Finalizers = nil
				_ = k8sClient.Update(ctx, provider)
				_ = k8sClient.Delete(ctx, provider)
			}
		})

		It("should set Error phase when secret is not found", func() {
			provider := &vmav1alpha1.NutanixProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      providerName,
					Namespace: ns,
				},
				Spec: vmav1alpha1.NutanixProviderSpec{
					URL:       mockServer.URL(),
					SecretRef: corev1.LocalObjectReference{Name: "nonexistent-secret"},
				},
			}
			Expect(k8sClient.Create(ctx, provider)).To(Succeed())

			Eventually(func(g Gomega) {
				p := &vmav1alpha1.NutanixProvider{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: providerName, Namespace: ns,
				}, p)).To(Succeed())
				g.Expect(p.Status.Phase).To(Equal(vmav1alpha1.ProviderPhaseError))
			}, timeout, interval).Should(Succeed())
		})
	})
})
