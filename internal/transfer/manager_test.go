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

package transfer

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

const (
	testNS       = "target-ns"
	testPrismURL = "https://prism.example.com:9440"
	testUsername = "admin"
	testPassword = "secret123"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = cdiv1beta1.AddToScheme(s)
	return s
}

func newTestManager() *Manager {
	s := testScheme()
	fc := fake.NewClientBuilder().WithScheme(s).Build()
	return &Manager{
		Client:   fc,
		PrismURL: testPrismURL,
		Username: testUsername,
		Password: testPassword,
	}
}

func ownerRef() metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: "vma.nutanix.io/v1alpha1",
		Kind:       "Migration",
		Name:       "test-migration",
		UID:        k8stypes.UID("uid-123"),
	}
}

func TestCreateCredentialSecret(t *testing.T) {
	mgr := newTestManager()
	ctx := context.Background()

	err := mgr.CreateCredentialSecret(
		ctx, "test-creds", testNS, ownerRef())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify secret was created
	secret := &corev1.Secret{}
	if err := mgr.Client.Get(ctx, k8stypes.NamespacedName{
		Name: "test-creds", Namespace: testNS,
	}, secret); err != nil {
		t.Fatalf("secret not found: %v", err)
	}

	if string(secret.Data[SecretKeyAccessKeyID]) != testUsername {
		t.Errorf("expected accessKeyId %q, got %q",
			testUsername,
			string(secret.Data[SecretKeyAccessKeyID]))
	}
	if string(secret.Data[SecretKeySecretKey]) != testPassword {
		t.Errorf("expected secretKey %q, got %q",
			testPassword,
			string(secret.Data[SecretKeySecretKey]))
	}

	// Owner reference
	if len(secret.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner ref, got %d",
			len(secret.OwnerReferences))
	}
	if secret.OwnerReferences[0].Kind != "Migration" {
		t.Errorf("expected owner kind Migration, got %s",
			secret.OwnerReferences[0].Kind)
	}
}

func TestCreateCredentialSecret_Idempotent(t *testing.T) {
	mgr := newTestManager()
	ctx := context.Background()

	// First create
	err := mgr.CreateCredentialSecret(
		ctx, "test-creds", testNS, ownerRef())
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Second create should not error
	err = mgr.CreateCredentialSecret(
		ctx, "test-creds", testNS, ownerRef())
	if err != nil {
		t.Fatalf("second create should be idempotent: %v", err)
	}
}

func TestCreateCAConfigMap(t *testing.T) {
	mgr := newTestManager()
	mgr.CACert = []byte("-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----")
	ctx := context.Background()

	err := mgr.CreateCAConfigMap(
		ctx, "test-ca", testNS, ownerRef())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := mgr.Client.Get(ctx, k8stypes.NamespacedName{
		Name: "test-ca", Namespace: testNS,
	}, cm); err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}

	if cm.Data[ConfigMapKeyCA] == "" {
		t.Error("expected CA cert in ConfigMap")
	}
}

func TestCreateCAConfigMap_NoCert(t *testing.T) {
	mgr := newTestManager()
	ctx := context.Background()

	// No CACert configured -- should be a no-op
	err := mgr.CreateCAConfigMap(
		ctx, "test-ca", testNS, ownerRef())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ConfigMap should not exist
	cm := &corev1.ConfigMap{}
	err = mgr.Client.Get(ctx, k8stypes.NamespacedName{
		Name: "test-ca", Namespace: testNS,
	}, cm)
	if err == nil {
		t.Error("expected ConfigMap to not exist when no CA cert")
	}
}

func TestCreateCAConfigMap_Idempotent(t *testing.T) {
	mgr := newTestManager()
	mgr.CACert = []byte("cert-data")
	ctx := context.Background()

	err := mgr.CreateCAConfigMap(
		ctx, "test-ca", testNS, ownerRef())
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	err = mgr.CreateCAConfigMap(
		ctx, "test-ca", testNS, ownerRef())
	if err != nil {
		t.Fatalf("second create should be idempotent: %v", err)
	}
}

func TestImageDownloadURL(t *testing.T) {
	mgr := &Manager{PrismURL: testPrismURL}
	url := mgr.ImageDownloadURL("img-uuid-001")

	expected := testPrismURL +
		"/api/vmm/v4.0/images/img-uuid-001/file"
	if url != expected {
		t.Errorf("expected %q, got %q", expected, url)
	}
}

func TestImageDownloadURL_TrailingSlash(t *testing.T) {
	mgr := &Manager{PrismURL: testPrismURL + "/"}
	url := mgr.ImageDownloadURL("img-uuid-002")

	expected := testPrismURL +
		"/api/vmm/v4.0/images/img-uuid-002/file"
	if url != expected {
		t.Errorf("expected %q, got %q", expected, url)
	}
}

func TestFindStorageMapping_ByID(t *testing.T) {
	storageMap := &vmav1alpha1.StorageMap{
		Spec: vmav1alpha1.StorageMapSpec{
			Map: []vmav1alpha1.StoragePair{{
				Source: vmav1alpha1.StorageSource{
					ID: "container-001",
				},
				Destination: vmav1alpha1.StorageDestination{
					StorageClass: "fast-ssd",
					VolumeMode:   corev1.PersistentVolumeBlock,
					AccessMode:   corev1.ReadWriteMany,
				},
			}},
		},
	}

	disk := &nutanix.Disk{
		BackingInfo: &nutanix.DiskBackingInfo{
			StorageContainerRef: &nutanix.Reference{
				ExtID: "container-001",
			},
		},
	}

	dest := FindStorageMapping(disk, storageMap)
	if dest == nil {
		t.Fatal("expected storage mapping, got nil")
	}
	if dest.StorageClass != "fast-ssd" {
		t.Errorf("expected fast-ssd, got %s", dest.StorageClass)
	}
	if dest.VolumeMode != corev1.PersistentVolumeBlock {
		t.Errorf("expected Block, got %s", dest.VolumeMode)
	}
}

func TestFindStorageMapping_ByName(t *testing.T) {
	storageMap := &vmav1alpha1.StorageMap{
		Spec: vmav1alpha1.StorageMapSpec{
			Map: []vmav1alpha1.StoragePair{{
				Source: vmav1alpha1.StorageSource{
					Name: "default-container",
				},
				Destination: vmav1alpha1.StorageDestination{
					StorageClass: "standard",
					VolumeMode:   corev1.PersistentVolumeFilesystem,
					AccessMode:   corev1.ReadWriteOnce,
				},
			}},
		},
	}

	disk := &nutanix.Disk{
		BackingInfo: &nutanix.DiskBackingInfo{
			StorageContainerRef: &nutanix.Reference{
				Name: "default-container",
			},
		},
	}

	dest := FindStorageMapping(disk, storageMap)
	if dest == nil {
		t.Fatal("expected storage mapping, got nil")
	}
	if dest.StorageClass != "standard" {
		t.Errorf("expected standard, got %s", dest.StorageClass)
	}
}

func TestFindStorageMapping_NoMatch(t *testing.T) {
	storageMap := &vmav1alpha1.StorageMap{
		Spec: vmav1alpha1.StorageMapSpec{
			Map: []vmav1alpha1.StoragePair{{
				Source: vmav1alpha1.StorageSource{
					ID: "container-other",
				},
				Destination: vmav1alpha1.StorageDestination{
					StorageClass: "other",
				},
			}},
		},
	}

	disk := &nutanix.Disk{
		BackingInfo: &nutanix.DiskBackingInfo{
			StorageContainerRef: &nutanix.Reference{
				ExtID: "container-001",
			},
		},
	}

	dest := FindStorageMapping(disk, storageMap)
	if dest != nil {
		t.Error("expected nil for unmatched mapping")
	}
}

func TestFindStorageMapping_NilBacking(t *testing.T) {
	storageMap := &vmav1alpha1.StorageMap{}
	disk := &nutanix.Disk{BackingInfo: nil}

	dest := FindStorageMapping(disk, storageMap)
	if dest != nil {
		t.Error("expected nil for disk with nil backing info")
	}
}

func TestCreateDataVolume(t *testing.T) {
	mgr := newTestManager()
	ctx := context.Background()

	opts := DataVolumeOptions{
		Name:          "test-dv",
		Namespace:     testNS,
		ImageURL:      testPrismURL + "/api/vmm/v4.0/images/img-001/file",
		DiskSizeBytes: 10 * 1024 * 1024 * 1024,
		StorageClass:  "fast",
		VolumeMode:    corev1.PersistentVolumeFilesystem,
		AccessMode:    corev1.ReadWriteOnce,
		SecretName:    "creds",
		OwnerRef:      ownerRef(),
	}

	err := mgr.CreateDataVolume(ctx, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify DataVolume was created
	dv := &cdiv1beta1.DataVolume{}
	if err := mgr.Client.Get(ctx, k8stypes.NamespacedName{
		Name: "test-dv", Namespace: testNS,
	}, dv); err != nil {
		t.Fatalf("DataVolume not found: %v", err)
	}
	if dv.Spec.Source.HTTP.URL != opts.ImageURL {
		t.Errorf("expected URL %q, got %q",
			opts.ImageURL, dv.Spec.Source.HTTP.URL)
	}
}

func TestCreateDataVolume_Idempotent(t *testing.T) {
	mgr := newTestManager()
	ctx := context.Background()

	opts := DataVolumeOptions{
		Name:          "test-dv",
		Namespace:     testNS,
		ImageURL:      testPrismURL + "/api/vmm/v4.0/images/img-001/file",
		DiskSizeBytes: 1024,
		StorageClass:  "default",
		VolumeMode:    corev1.PersistentVolumeFilesystem,
		AccessMode:    corev1.ReadWriteOnce,
		SecretName:    "creds",
		OwnerRef:      ownerRef(),
	}

	if err := mgr.CreateDataVolume(ctx, opts); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := mgr.CreateDataVolume(ctx, opts); err != nil {
		t.Fatalf("second create should be idempotent: %v", err)
	}
}

func TestGetDataVolumeProgress(t *testing.T) {
	s := testScheme()
	dv := &cdiv1beta1.DataVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-dv",
			Namespace: testNS,
		},
		Status: cdiv1beta1.DataVolumeStatus{
			Phase:    cdiv1beta1.ImportInProgress,
			Progress: "45.2%",
		},
	}

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(dv).
		Build()

	mgr := &Manager{Client: fc}

	progress, err := mgr.GetDataVolumeProgress(
		ctx(), "test-dv", testNS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if progress.Phase != cdiv1beta1.ImportInProgress {
		t.Errorf("expected ImportInProgress, got %s",
			progress.Phase)
	}
	if progress.Progress != "45.2%" {
		t.Errorf("expected 45.2%%, got %s", progress.Progress)
	}
}

func TestGetDataVolumeProgress_Succeeded(t *testing.T) {
	s := testScheme()
	dv := &cdiv1beta1.DataVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-dv",
			Namespace: testNS,
		},
		Status: cdiv1beta1.DataVolumeStatus{
			Phase:    cdiv1beta1.Succeeded,
			Progress: "100.0%",
		},
	}

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(dv).
		Build()

	mgr := &Manager{Client: fc}

	progress, err := mgr.GetDataVolumeProgress(
		ctx(), "test-dv", testNS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if progress.Phase != cdiv1beta1.Succeeded {
		t.Errorf("expected Succeeded, got %s", progress.Phase)
	}
}

func TestGetDataVolumeProgress_NotFound(t *testing.T) {
	s := testScheme()
	fc := fake.NewClientBuilder().WithScheme(s).Build()
	mgr := &Manager{Client: fc}

	_, err := mgr.GetDataVolumeProgress(
		ctx(), "nonexistent", testNS)
	if err == nil {
		t.Error("expected error for nonexistent DataVolume")
	}
}

func TestDeleteCredentialSecret(t *testing.T) {
	mgr := newTestManager()
	c := context.Background()

	// Create then delete
	_ = mgr.CreateCredentialSecret(
		c, "test-creds", testNS, ownerRef())
	err := mgr.DeleteCredentialSecret(c, "test-creds", testNS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Delete again (idempotent)
	err = mgr.DeleteCredentialSecret(c, "test-creds", testNS)
	if err != nil {
		t.Fatalf("delete nonexistent should be idempotent: %v", err)
	}
}

func TestDeleteCAConfigMap(t *testing.T) {
	mgr := newTestManager()
	mgr.CACert = []byte("cert")
	c := context.Background()

	_ = mgr.CreateCAConfigMap(c, "test-ca", testNS, ownerRef())
	err := mgr.DeleteCAConfigMap(c, "test-ca", testNS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Delete again (idempotent)
	err = mgr.DeleteCAConfigMap(c, "test-ca", testNS)
	if err != nil {
		t.Fatalf("delete nonexistent should be idempotent: %v", err)
	}
}

func ctx() context.Context {
	return context.Background()
}
