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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

func TestRegionsToText(t *testing.T) {
	regions := []nutanix.ChangedRegion{
		{Offset: 0, Length: 65536},
		{Offset: 131072, Length: 65536},
	}
	got := RegionsToText(regions)
	expected := "0:65536\n131072:65536"
	if got != expected {
		t.Errorf("RegionsToText() = %q, want %q", got, expected)
	}
}

func TestRegionsToText_Empty(t *testing.T) {
	got := RegionsToText(nil)
	if got != "" {
		t.Errorf("RegionsToText(nil) = %q, want empty", got)
	}
}

func TestDeltaBytes(t *testing.T) {
	regions := []nutanix.ChangedRegion{
		{Offset: 0, Length: 65536},
		{Offset: 131072, Length: 131072},
	}
	got := DeltaBytes(regions)
	if got != 196608 {
		t.Errorf("DeltaBytes() = %d, want 196608", got)
	}
}

func TestDeltaBytes_Empty(t *testing.T) {
	if DeltaBytes(nil) != 0 {
		t.Error("DeltaBytes(nil) should return 0")
	}
}

func TestBuildDeltaPod(t *testing.T) {
	ownerRef := metav1.OwnerReference{
		APIVersion: "vma.nutanix.io/v1alpha1",
		Kind:       "Migration",
		Name:       "test-mig",
		UID:        types.UID("uid-001"),
	}

	regions := []nutanix.ChangedRegion{
		{Offset: 0, Length: 65536},
		{Offset: 131072, Length: 65536},
	}

	opts := DeltaPodOptions{
		Name:       "delta-pod-001",
		Namespace:  "target-ns",
		PVCName:    "my-pvc",
		ImageURL:   "https://prism:9440/api/vmm/v4.0/images/img-001/file",
		SecretName: "vma-creds",
		Regions:    regions,
		OwnerRef:   ownerRef,
	}

	pod, cm := BuildDeltaPod(opts)

	// Verify ConfigMap
	if cm.Name != "delta-pod-001-regions" {
		t.Errorf("ConfigMap name = %s, want delta-pod-001-regions",
			cm.Name)
	}
	if cm.Namespace != "target-ns" {
		t.Errorf("ConfigMap namespace = %s, want target-ns",
			cm.Namespace)
	}
	regionsData, ok := cm.Data["regions.txt"]
	if !ok {
		t.Fatal("ConfigMap missing regions.txt")
	}
	if regionsData != "0:65536\n131072:65536" {
		t.Errorf("regions.txt = %q, unexpected", regionsData)
	}
	if len(cm.OwnerReferences) != 1 {
		t.Error("ConfigMap missing owner reference")
	}

	// Verify Pod
	if pod.Name != "delta-pod-001" {
		t.Errorf("Pod name = %s, want delta-pod-001", pod.Name)
	}
	if pod.Namespace != "target-ns" {
		t.Errorf("Pod namespace = %s, want target-ns",
			pod.Namespace)
	}
	if len(pod.OwnerReferences) != 1 {
		t.Error("Pod missing owner reference")
	}

	// Verify container
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d",
			len(pod.Spec.Containers))
	}
	c := pod.Spec.Containers[0]
	if c.Name != deltaContainerName {
		t.Errorf("container name = %s, want %s",
			c.Name, deltaContainerName)
	}
	if c.Image != deltaImage {
		t.Errorf("container image = %s, want %s",
			c.Image, deltaImage)
	}

	// Verify env vars
	envMap := make(map[string]bool)
	for _, env := range c.Env {
		envMap[env.Name] = true
	}
	for _, name := range []string{
		"PRISM_USERNAME", "PRISM_PASSWORD", "IMAGE_URL",
	} {
		if !envMap[name] {
			t.Errorf("missing env var %s", name)
		}
	}

	// Verify volume mounts
	if len(c.VolumeMounts) != 2 {
		t.Fatalf("expected 2 volume mounts, got %d",
			len(c.VolumeMounts))
	}

	// Verify volumes
	if len(pod.Spec.Volumes) != 2 {
		t.Fatalf("expected 2 volumes, got %d",
			len(pod.Spec.Volumes))
	}
	pvcVol := pod.Spec.Volumes[0]
	if pvcVol.PersistentVolumeClaim == nil ||
		pvcVol.PersistentVolumeClaim.ClaimName != "my-pvc" {
		t.Error("PVC volume not configured correctly")
	}
	cmVol := pod.Spec.Volumes[1]
	if cmVol.ConfigMap == nil ||
		cmVol.ConfigMap.Name != "delta-pod-001-regions" {
		t.Error("ConfigMap volume not configured correctly")
	}
}

func TestBuildDeltaPod_EmptyRegions(t *testing.T) {
	opts := DeltaPodOptions{
		Name:      "delta-empty",
		Namespace: "ns",
		PVCName:   "pvc",
		ImageURL:  "https://example.com/img",
		Regions:   []nutanix.ChangedRegion{},
		OwnerRef: metav1.OwnerReference{
			APIVersion: "v1",
			Kind:       "Migration",
			Name:       "m",
		},
	}

	_, cm := BuildDeltaPod(opts)
	if cm.Data["regions.txt"] != "" {
		t.Errorf("expected empty regions.txt for empty regions")
	}
}
