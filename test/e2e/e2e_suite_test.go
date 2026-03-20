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
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

var (
	k8sClient  client.Client
	nxClient   nutanix.NutanixClient
	ctx        context.Context
	cancel     context.CancelFunc
	e2eConfig  E2EConfig
)

// E2EConfig holds environment-sourced test configuration.
type E2EConfig struct {
	PrismURL    string
	Username    string
	Password    string
	Kubeconfig  string
	Insecure    bool
	TestVMID    string // optional: specific VM UUID to migrate
	TestVMName  string // optional: VM name pattern to find
	SubnetID    string // Nutanix subnet UUID for network mapping
	SubnetName  string // Nutanix subnet name for network mapping
	StorageID   string // Nutanix storage container UUID
	StorageName string // Nutanix storage container name
	StorageClass string // Target Kubernetes StorageClass
	Namespace   string // Test namespace (default: vma-e2e-test)
}

func TestE2E(t *testing.T) {
	if os.Getenv("NUTANIX_E2E") != "true" {
		t.Skip("Skipping E2E tests: NUTANIX_E2E != true")
	}

	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter,
		"Starting nutanix-vma E2E test suite\n")
	RunSpecs(t, "Nutanix VMA E2E Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.Background())

	By("loading E2E configuration from environment")
	e2eConfig = loadConfig()

	By("registering schemes")
	Expect(vmav1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(kubevirtv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(cdiv1beta1.AddToScheme(scheme.Scheme)).To(Succeed())

	By("creating Kubernetes client")
	kubeconfig := e2eConfig.Kubeconfig
	if kubeconfig == "" {
		kubeconfig = clientcmd.RecommendedHomeFile
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	Expect(err).NotTo(HaveOccurred(), "Failed to load kubeconfig")

	k8sClient, err = client.New(cfg, client.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).NotTo(HaveOccurred())

	By("creating Nutanix API client")
	nxClient, err = nutanix.NewClient(nutanix.ClientConfig{
		Host:               e2eConfig.PrismURL,
		Username:           e2eConfig.Username,
		Password:           e2eConfig.Password,
		InsecureSkipVerify: e2eConfig.Insecure,
		Timeout:            60 * time.Second,
		MaxRetries:         3,
	})
	Expect(err).NotTo(HaveOccurred())

	By("ensuring test namespace exists")
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: e2eConfig.Namespace,
		},
	}
	err = k8sClient.Create(ctx, ns)
	if err != nil && !isAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred(),
			"Failed to create test namespace")
	}
})

var _ = AfterSuite(func() {
	if cancel != nil {
		cancel()
	}
})

func loadConfig() E2EConfig {
	cfg := E2EConfig{
		PrismURL:     requireEnv("NUTANIX_PC_URL"),
		Username:     requireEnv("NUTANIX_USERNAME"),
		Password:     requireEnv("NUTANIX_PASSWORD"),
		Kubeconfig:   os.Getenv("KUBEVIRT_KUBECONFIG"),
		Insecure:     os.Getenv("NUTANIX_INSECURE") == "true",
		TestVMID:     os.Getenv("NUTANIX_TEST_VM_ID"),
		TestVMName:   os.Getenv("NUTANIX_TEST_VM_NAME"),
		SubnetID:     os.Getenv("NUTANIX_SUBNET_ID"),
		SubnetName:   os.Getenv("NUTANIX_SUBNET_NAME"),
		StorageID:    os.Getenv("NUTANIX_STORAGE_ID"),
		StorageName:  os.Getenv("NUTANIX_STORAGE_NAME"),
		StorageClass: os.Getenv("NUTANIX_STORAGE_CLASS"),
		Namespace:    os.Getenv("NUTANIX_E2E_NAMESPACE"),
	}
	if cfg.Namespace == "" {
		cfg.Namespace = "vma-e2e-test"
	}
	if cfg.StorageClass == "" {
		cfg.StorageClass = "local-path"
	}
	return cfg
}

func requireEnv(key string) string {
	val := os.Getenv(key)
	ExpectWithOffset(1, val).NotTo(BeEmpty(),
		fmt.Sprintf("Required env var %s not set", key))
	return val
}

func isAlreadyExists(err error) bool {
	return err != nil &&
		client.IgnoreAlreadyExists(err) == nil
}
