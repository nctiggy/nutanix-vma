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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

const (
	testDVName      = "test-dv"
	testDVNamespace = "target-ns"
	testImageURL    = "https://prism:9440/api/vmm/v4.0/images/img-001/file"
	testSecretName  = "vma-creds"
)

func testOwnerRef() metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: "vma.nutanix.io/v1alpha1",
		Kind:       "Migration",
		Name:       "test-migration",
		UID:        k8stypes.UID("uid-123"),
	}
}

func TestBuildDataVolume_Basic(t *testing.T) {
	opts := DataVolumeOptions{
		Name:          testDVName,
		Namespace:     testDVNamespace,
		ImageURL:      testImageURL,
		DiskSizeBytes: 10 * 1024 * 1024 * 1024, // 10 GiB
		StorageClass:  "fast-storage",
		VolumeMode:    corev1.PersistentVolumeFilesystem,
		AccessMode:    corev1.ReadWriteOnce,
		SecretName:    testSecretName,
		OwnerRef:      testOwnerRef(),
	}

	dv := BuildDataVolume(opts)

	if dv.Name != testDVName {
		t.Errorf("expected name %q, got %q", testDVName, dv.Name)
	}
	if dv.Namespace != testDVNamespace {
		t.Errorf("expected namespace %q, got %q",
			testDVNamespace, dv.Namespace)
	}

	// Owner reference
	if len(dv.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner ref, got %d",
			len(dv.OwnerReferences))
	}
	if dv.OwnerReferences[0].Kind != "Migration" {
		t.Errorf("expected owner kind Migration, got %s",
			dv.OwnerReferences[0].Kind)
	}

	// HTTP source
	if dv.Spec.Source == nil || dv.Spec.Source.HTTP == nil {
		t.Fatal("expected HTTP source")
	}
	if dv.Spec.Source.HTTP.URL != testImageURL {
		t.Errorf("expected URL %q, got %q",
			testImageURL, dv.Spec.Source.HTTP.URL)
	}
	if dv.Spec.Source.HTTP.SecretRef != testSecretName {
		t.Errorf("expected secret %q, got %q",
			testSecretName, dv.Spec.Source.HTTP.SecretRef)
	}

	// No cert config map
	if dv.Spec.Source.HTTP.CertConfigMap != "" {
		t.Errorf("expected empty CertConfigMap, got %q",
			dv.Spec.Source.HTTP.CertConfigMap)
	}

	// PVC spec
	if dv.Spec.PVC == nil {
		t.Fatal("expected PVC spec")
	}
	if *dv.Spec.PVC.StorageClassName != "fast-storage" {
		t.Errorf("expected storage class fast-storage, got %s",
			*dv.Spec.PVC.StorageClassName)
	}
	if *dv.Spec.PVC.VolumeMode != corev1.PersistentVolumeFilesystem {
		t.Errorf("expected Filesystem, got %s",
			*dv.Spec.PVC.VolumeMode)
	}
	if len(dv.Spec.PVC.AccessModes) != 1 ||
		dv.Spec.PVC.AccessModes[0] != corev1.ReadWriteOnce {
		t.Errorf("expected ReadWriteOnce access mode")
	}

	expectedSize := resource.NewQuantity(
		10*1024*1024*1024, resource.BinarySI)
	actualSize := dv.Spec.PVC.Resources.Requests[corev1.ResourceStorage]
	if !actualSize.Equal(*expectedSize) {
		t.Errorf("expected storage %s, got %s",
			expectedSize.String(), actualSize.String())
	}
}

func TestBuildDataVolume_WithCertConfigMap(t *testing.T) {
	opts := DataVolumeOptions{
		Name:          testDVName,
		Namespace:     testDVNamespace,
		ImageURL:      testImageURL,
		DiskSizeBytes: 1024 * 1024 * 1024,
		StorageClass:  "default",
		VolumeMode:    corev1.PersistentVolumeBlock,
		AccessMode:    corev1.ReadWriteMany,
		SecretName:    testSecretName,
		CertConfigMap: "my-ca-cert",
		OwnerRef:      testOwnerRef(),
	}

	dv := BuildDataVolume(opts)

	if dv.Spec.Source.HTTP.CertConfigMap != "my-ca-cert" {
		t.Errorf("expected CertConfigMap my-ca-cert, got %q",
			dv.Spec.Source.HTTP.CertConfigMap)
	}

	if *dv.Spec.PVC.VolumeMode != corev1.PersistentVolumeBlock {
		t.Errorf("expected Block volume mode, got %s",
			*dv.Spec.PVC.VolumeMode)
	}
	if dv.Spec.PVC.AccessModes[0] != corev1.ReadWriteMany {
		t.Errorf("expected ReadWriteMany, got %s",
			dv.Spec.PVC.AccessModes[0])
	}
}

func TestBuildDataVolume_SmallDisk(t *testing.T) {
	opts := DataVolumeOptions{
		Name:          testDVName,
		Namespace:     testDVNamespace,
		ImageURL:      testImageURL,
		DiskSizeBytes: 512 * 1024 * 1024, // 512 MiB
		StorageClass:  "slow",
		VolumeMode:    corev1.PersistentVolumeFilesystem,
		AccessMode:    corev1.ReadWriteOnce,
		SecretName:    testSecretName,
		OwnerRef:      testOwnerRef(),
	}

	dv := BuildDataVolume(opts)

	expectedSize := resource.NewQuantity(
		512*1024*1024, resource.BinarySI)
	actualSize := dv.Spec.PVC.Resources.Requests[corev1.ResourceStorage]
	if !actualSize.Equal(*expectedSize) {
		t.Errorf("expected %s, got %s",
			expectedSize.String(), actualSize.String())
	}
}
