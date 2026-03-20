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
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/controller"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
	"github.com/nctiggy/nutanix-vma/pkg/mock"
)

var (
	cfg         *rest.Config
	k8sClient   client.Client
	testEnv     *envtest.Environment
	mockServer  *mock.Server
	ctx         context.Context
	cancel      context.CancelFunc
	projectRoot string
)

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Integration Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.Background())

	// Resolve project root (test/integration -> project root)
	projectRoot = filepath.Join("..", "..")

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join(projectRoot, "config", "crd", "bases"),
			filepath.Join(projectRoot, "test", "fixtures", "crds"),
		},
		ErrorIfCRDPathMissing: true,
		BinaryAssetsDirectory: filepath.Join(projectRoot, "bin", "k8s",
			fmt.Sprintf("1.35.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	By("registering VMA scheme")
	err = vmav1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	By("creating K8s client")
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	By("starting mock Nutanix server")
	mockServer = mock.NewServer(mock.WithFixtures())
	// Clear cluster external addresses so the controller does not attempt
	// to reach unreachable PE URLs during integration tests.
	for i := range mockServer.Store.Clusters {
		mockServer.Store.Clusters[i].Network = nil
	}

	By("starting controller manager")
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).NotTo(HaveOccurred())

	// Register the Provider controller with a fast-failing client factory.
	// Integration tests use the mock Nutanix server; short timeouts prevent
	// reconciliation from blocking on unreachable PE URLs.
	err = (&controller.ProviderReconciler{
		Client: mgr.GetClient(),
		ClientFactory: func(config nutanix.ClientConfig) (nutanix.NutanixClient, error) {
			config.Timeout = 2 * time.Second
			config.MaxRetries = 0
			return nutanix.NewClient(config)
		},
	}).SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()
})

var _ = AfterSuite(func() {
	By("stopping controller manager")
	cancel()

	By("stopping mock Nutanix server")
	if mockServer != nil {
		mockServer.Close()
	}

	By("tearing down the test environment")
	// envtest Stop() can timeout waiting for kube-apiserver to exit;
	// log but don't fail the suite on teardown errors.
	if err := testEnv.Stop(); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"Warning: envtest teardown error (non-fatal): %v\n", err)
	}
})
