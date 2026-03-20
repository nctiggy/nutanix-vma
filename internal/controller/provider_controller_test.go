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

package controller

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

// fakeNutanixClient implements NutanixClient for testing.
type fakeNutanixClient struct {
	vms               []nutanix.VM
	subnets           []nutanix.Subnet
	clusters          []nutanix.Cluster
	storageContainers []nutanix.StorageContainer
	listVMsErr        error
	listSubnetsErr    error
	listClustersErr   error
	listContainersErr error
	getVMErr          error
}

func (f *fakeNutanixClient) ListVMs(_ context.Context) ([]nutanix.VM, error) {
	return f.vms, f.listVMsErr
}

func (f *fakeNutanixClient) GetVM(_ context.Context, uuid string) (*nutanix.VM, error) {
	if f.getVMErr != nil {
		return nil, f.getVMErr
	}
	for i := range f.vms {
		if f.vms[i].ExtID == uuid {
			return &f.vms[i], nil
		}
	}
	return nil, errors.New("VM not found: " + uuid)
}

func (f *fakeNutanixClient) PowerOffVM(_ context.Context, _ string) error {
	return errors.New("not implemented")
}

func (f *fakeNutanixClient) PowerOnVM(_ context.Context, _ string) error {
	return errors.New("not implemented")
}

func (f *fakeNutanixClient) GetVMPowerState(_ context.Context, _ string) (nutanix.PowerState, error) {
	return "", errors.New("not implemented")
}

func (f *fakeNutanixClient) CreateRecoveryPoint(_ context.Context, _, _ string) (string, error) {
	return "", errors.New("not implemented")
}

func (f *fakeNutanixClient) GetRecoveryPoint(_ context.Context, _ string) (*nutanix.RecoveryPoint, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeNutanixClient) DeleteRecoveryPoint(_ context.Context, _ string) error {
	return errors.New("not implemented")
}

func (f *fakeNutanixClient) CreateImageFromDisk(_ context.Context, _, _, _ string) (string, error) {
	return "", errors.New("not implemented")
}

func (f *fakeNutanixClient) GetImage(_ context.Context, _ string) (*nutanix.Image, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeNutanixClient) DownloadImage(_ context.Context, _ string, _ io.Writer) error {
	return errors.New("not implemented")
}

func (f *fakeNutanixClient) DeleteImage(_ context.Context, _ string) error {
	return errors.New("not implemented")
}

func (f *fakeNutanixClient) CloneVMFromRecoveryPoint(_ context.Context, _, _ string) (string, error) {
	return "", errors.New("not implemented")
}

func (f *fakeNutanixClient) DeleteVM(_ context.Context, _ string) error {
	return errors.New("not implemented")
}

func (f *fakeNutanixClient) DiscoverClusterForCBT(_ context.Context, _ string) (*nutanix.CBTClusterInfo, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeNutanixClient) GetChangedRegions(_ context.Context, _, _, _, _, _ string, _, _, _ int64) (*nutanix.ChangedRegions, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeNutanixClient) ListSubnets(_ context.Context) ([]nutanix.Subnet, error) {
	return f.subnets, f.listSubnetsErr
}

func (f *fakeNutanixClient) ListStorageContainers(_ context.Context, _ string) ([]nutanix.StorageContainer, error) {
	return f.storageContainers, f.listContainersErr
}

func (f *fakeNutanixClient) ListClusters(_ context.Context) ([]nutanix.Cluster, error) {
	return f.clusters, f.listClustersErr
}

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = vmav1alpha1.AddToScheme(s)
	return s
}

func newProviderAndSecret() (*vmav1alpha1.NutanixProvider, *corev1.Secret) {
	ns := "default"
	provider := &vmav1alpha1.NutanixProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-provider",
			Namespace: ns,
		},
		Spec: vmav1alpha1.NutanixProviderSpec{
			URL:             "https://prism.example.com:9440",
			SecretRef:       corev1.LocalObjectReference{Name: "nutanix-creds"},
			RefreshInterval: "5m",
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nutanix-creds",
			Namespace: ns,
		},
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte("secret"),
		},
	}

	return provider, secret
}

func TestReconcile_SuccessfulInventory(t *testing.T) {
	s := newTestScheme()
	provider, secret := newProviderAndSecret()

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(provider, secret).
		WithStatusSubresource(provider).
		Build()

	fakeNX := &fakeNutanixClient{
		vms:     []nutanix.VM{{ExtID: "vm-1"}, {ExtID: "vm-2"}, {ExtID: "vm-3"}},
		subnets: []nutanix.Subnet{{ExtID: "sub-1"}},
		clusters: []nutanix.Cluster{{
			ExtID: "cl-1",
			Name:  "cluster-1",
			Network: &nutanix.ClusterNetwork{
				ExternalAddress: "10.0.0.10",
			},
		}},
		storageContainers: []nutanix.StorageContainer{{UUID: "sc-1"}},
	}

	r := &ProviderReconciler{
		Client: fc,
		ClientFactory: func(_ nutanix.ClientConfig) (nutanix.NutanixClient, error) {
			return fakeNX, nil
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-provider", Namespace: "default"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 5*time.Minute {
		t.Errorf("expected requeue after 5m, got %v", result.RequeueAfter)
	}

	// Verify status was updated
	updated := &vmav1alpha1.NutanixProvider{}
	if err := fc.Get(context.Background(), types.NamespacedName{
		Name: "test-provider", Namespace: "default",
	}, updated); err != nil {
		t.Fatalf("failed to get provider: %v", err)
	}

	if updated.Status.Phase != vmav1alpha1.ProviderPhaseConnected {
		t.Errorf("expected phase Connected, got %s", updated.Status.Phase)
	}
	if updated.Status.VMCount != 3 {
		t.Errorf("expected VMCount 3, got %d", updated.Status.VMCount)
	}

	// Check Connected condition
	foundConnected := false
	for _, c := range updated.Status.Conditions {
		if c.Type == conditionTypeConnected && c.Status == metav1.ConditionTrue {
			foundConnected = true
		}
	}
	if !foundConnected {
		t.Error("expected Connected=True condition")
	}
}

func TestReconcile_SecretNotFound(t *testing.T) {
	s := newTestScheme()
	provider, _ := newProviderAndSecret()

	// Create provider without the secret
	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(provider).
		WithStatusSubresource(provider).
		Build()

	r := &ProviderReconciler{
		Client: fc,
		ClientFactory: func(_ nutanix.ClientConfig) (nutanix.NutanixClient, error) {
			return nil, errors.New("should not be called")
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-provider", Namespace: "default"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("expected requeue after 1m for error, got %v", result.RequeueAfter)
	}

	updated := &vmav1alpha1.NutanixProvider{}
	if err := fc.Get(context.Background(), types.NamespacedName{
		Name: "test-provider", Namespace: "default",
	}, updated); err != nil {
		t.Fatalf("failed to get provider: %v", err)
	}

	if updated.Status.Phase != vmav1alpha1.ProviderPhaseError {
		t.Errorf("expected phase Error, got %s", updated.Status.Phase)
	}
}

func TestReconcile_InvalidCredentials(t *testing.T) {
	s := newTestScheme()
	provider, _ := newProviderAndSecret()

	// Create secret with empty password
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nutanix-creds",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte(""),
		},
	}

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(provider, secret).
		WithStatusSubresource(provider).
		Build()

	r := &ProviderReconciler{
		Client: fc,
		ClientFactory: func(_ nutanix.ClientConfig) (nutanix.NutanixClient, error) {
			return nil, errors.New("should not be called")
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-provider", Namespace: "default"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("expected requeue after 1m, got %v", result.RequeueAfter)
	}

	updated := &vmav1alpha1.NutanixProvider{}
	_ = fc.Get(context.Background(), types.NamespacedName{
		Name: "test-provider", Namespace: "default",
	}, updated)

	if updated.Status.Phase != vmav1alpha1.ProviderPhaseError {
		t.Errorf("expected phase Error, got %s", updated.Status.Phase)
	}
}

func TestReconcile_ListVMsFails(t *testing.T) {
	s := newTestScheme()
	provider, secret := newProviderAndSecret()

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(provider, secret).
		WithStatusSubresource(provider).
		Build()

	fakeNX := &fakeNutanixClient{
		listVMsErr: errors.New("connection refused"),
	}

	r := &ProviderReconciler{
		Client: fc,
		ClientFactory: func(_ nutanix.ClientConfig) (nutanix.NutanixClient, error) {
			return fakeNX, nil
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-provider", Namespace: "default"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("expected requeue after 1m, got %v", result.RequeueAfter)
	}

	updated := &vmav1alpha1.NutanixProvider{}
	_ = fc.Get(context.Background(), types.NamespacedName{
		Name: "test-provider", Namespace: "default",
	}, updated)

	if updated.Status.Phase != vmav1alpha1.ProviderPhaseError {
		t.Errorf("expected phase Error, got %s", updated.Status.Phase)
	}
}

func TestReconcile_ClientFactoryFails(t *testing.T) {
	s := newTestScheme()
	provider, secret := newProviderAndSecret()

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(provider, secret).
		WithStatusSubresource(provider).
		Build()

	r := &ProviderReconciler{
		Client: fc,
		ClientFactory: func(_ nutanix.ClientConfig) (nutanix.NutanixClient, error) {
			return nil, errors.New("TLS handshake failed")
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-provider", Namespace: "default"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("expected requeue after 1m, got %v", result.RequeueAfter)
	}

	updated := &vmav1alpha1.NutanixProvider{}
	_ = fc.Get(context.Background(), types.NamespacedName{
		Name: "test-provider", Namespace: "default",
	}, updated)

	if updated.Status.Phase != vmav1alpha1.ProviderPhaseError {
		t.Errorf("expected phase Error, got %s", updated.Status.Phase)
	}
}

func TestReconcile_NotFound(t *testing.T) {
	s := newTestScheme()

	fc := fake.NewClientBuilder().
		WithScheme(s).
		Build()

	r := &ProviderReconciler{
		Client: fc,
		ClientFactory: func(_ nutanix.ClientConfig) (nutanix.NutanixClient, error) {
			return nil, errors.New("should not be called")
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "missing", Namespace: "default"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue for not found, got %v", result.RequeueAfter)
	}
}

func TestReconcile_Finalizer(t *testing.T) {
	s := newTestScheme()
	provider, secret := newProviderAndSecret()

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(provider, secret).
		WithStatusSubresource(provider).
		Build()

	fakeNX := &fakeNutanixClient{
		vms: []nutanix.VM{{ExtID: "vm-1"}},
	}

	r := &ProviderReconciler{
		Client: fc,
		ClientFactory: func(_ nutanix.ClientConfig) (nutanix.NutanixClient, error) {
			return fakeNX, nil
		},
	}

	// First reconcile should add finalizer
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-provider", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &vmav1alpha1.NutanixProvider{}
	_ = fc.Get(context.Background(), types.NamespacedName{
		Name: "test-provider", Namespace: "default",
	}, updated)

	hasFinalizer := false
	for _, f := range updated.Finalizers {
		if f == providerFinalizer {
			hasFinalizer = true
		}
	}
	if !hasFinalizer {
		t.Error("expected finalizer to be added")
	}
}

func TestReconcile_DeletionWithPlanReference(t *testing.T) {
	s := newTestScheme()
	provider, _ := newProviderAndSecret()
	provider.Finalizers = []string{providerFinalizer}
	now := metav1.Now()
	provider.DeletionTimestamp = &now

	plan := &vmav1alpha1.MigrationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-plan",
			Namespace: "default",
		},
		Spec: vmav1alpha1.MigrationPlanSpec{
			ProviderRef:     corev1.LocalObjectReference{Name: "test-provider"},
			TargetNamespace: "target",
			NetworkMapRef:   corev1.LocalObjectReference{Name: "net-map"},
			StorageMapRef:   corev1.LocalObjectReference{Name: "stor-map"},
			VMs:             []vmav1alpha1.PlanVM{{ID: "vm-1"}},
		},
	}

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(provider, plan).
		WithStatusSubresource(provider).
		Build()

	r := &ProviderReconciler{
		Client: fc,
		ClientFactory: func(_ nutanix.ClientConfig) (nutanix.NutanixClient, error) {
			return nil, errors.New("should not be called")
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-provider", Namespace: "default"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should requeue because provider is still in use
	if result.RequeueAfter != 30*time.Second {
		t.Errorf("expected requeue after 30s, got %v", result.RequeueAfter)
	}

	// Finalizer should still be present
	updated := &vmav1alpha1.NutanixProvider{}
	_ = fc.Get(context.Background(), types.NamespacedName{
		Name: "test-provider", Namespace: "default",
	}, updated)

	hasFinalizer := false
	for _, f := range updated.Finalizers {
		if f == providerFinalizer {
			hasFinalizer = true
		}
	}
	if !hasFinalizer {
		t.Error("expected finalizer to remain when provider is in use")
	}
}

func TestReconcile_PartialInventoryFailure(t *testing.T) {
	s := newTestScheme()
	provider, secret := newProviderAndSecret()

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(provider, secret).
		WithStatusSubresource(provider).
		Build()

	fakeNX := &fakeNutanixClient{
		vms:             []nutanix.VM{{ExtID: "vm-1"}},
		listSubnetsErr:  errors.New("subnet API error"),
		listClustersErr: errors.New("cluster API error"),
	}

	r := &ProviderReconciler{
		Client: fc,
		ClientFactory: func(_ nutanix.ClientConfig) (nutanix.NutanixClient, error) {
			return fakeNX, nil
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-provider", Namespace: "default"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should still succeed (best-effort inventory)
	if result.RequeueAfter != 5*time.Minute {
		t.Errorf("expected requeue after 5m, got %v", result.RequeueAfter)
	}

	updated := &vmav1alpha1.NutanixProvider{}
	_ = fc.Get(context.Background(), types.NamespacedName{
		Name: "test-provider", Namespace: "default",
	}, updated)

	if updated.Status.Phase != vmav1alpha1.ProviderPhaseConnected {
		t.Errorf("expected phase Connected with partial inventory, got %s", updated.Status.Phase)
	}
	if updated.Status.VMCount != 1 {
		t.Errorf("expected VMCount 1, got %d", updated.Status.VMCount)
	}
}

func TestReconcile_CustomRefreshInterval(t *testing.T) {
	s := newTestScheme()
	provider, secret := newProviderAndSecret()
	provider.Spec.RefreshInterval = "10m"

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(provider, secret).
		WithStatusSubresource(provider).
		Build()

	fakeNX := &fakeNutanixClient{
		vms: []nutanix.VM{{ExtID: "vm-1"}},
	}

	r := &ProviderReconciler{
		Client: fc,
		ClientFactory: func(_ nutanix.ClientConfig) (nutanix.NutanixClient, error) {
			return fakeNX, nil
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-provider", Namespace: "default"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 10*time.Minute {
		t.Errorf("expected requeue after 10m, got %v", result.RequeueAfter)
	}
}
