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
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

const (
	// CBTBlockSize is the block size for CBT changed region queries (64KB).
	CBTBlockSize = int64(65536)

	deltaContainerName = "delta-transfer"
	deltaImage         = "busybox:1.37"
	deltaVolumeName    = "disk-data"
	deltaConfigVolume  = "delta-config"
	deltaMountPath     = "/data/disk"
	deltaConfigMount   = "/config"
)

// deltaScript is the shell script executed inside the delta transfer pod.
// It reads offset:length pairs from /config/regions.txt and for each region:
// 1. Downloads the byte range from the Nutanix image via wget
// 2. Writes the bytes to the PVC at the correct offset via dd
const deltaScript = `#!/bin/sh
set -e
AUTH=$(printf '%s:%s' "${PRISM_USERNAME}" "${PRISM_PASSWORD}" | base64)
while IFS=: read -r offset length; do
  [ -z "$offset" ] && continue
  end=$((offset + length - 1))
  wget --header="Authorization: Basic ${AUTH}" \
       --header="Range: bytes=${offset}-${end}" \
       -q -O /tmp/chunk "${IMAGE_URL}" || exit 1
  dd if=/tmp/chunk of=/data/disk bs=1 seek="${offset}" conv=notrunc 2>/dev/null || exit 1
  rm -f /tmp/chunk
done < /config/regions.txt
echo "Delta transfer complete"
`

// DeltaPodOptions configures a delta transfer pod.
type DeltaPodOptions struct {
	// Name is the pod name.
	Name string

	// Namespace is the target namespace.
	Namespace string

	// PVCName is the PVC to write delta data to.
	PVCName string

	// ImageURL is the Prism image download URL for the new snapshot.
	ImageURL string

	// SecretName is the credential secret name (accessKeyId/secretKey format).
	SecretName string

	// Regions is the list of changed block regions to transfer.
	Regions []nutanix.ChangedRegion

	// OwnerRef is the owner reference for cleanup.
	OwnerRef metav1.OwnerReference
}

// RegionsToText converts changed regions to line-delimited text (offset:length per line).
func RegionsToText(regions []nutanix.ChangedRegion) string {
	lines := make([]string, 0, len(regions))
	for _, r := range regions {
		lines = append(lines, fmt.Sprintf("%d:%d", r.Offset, r.Length))
	}
	return strings.Join(lines, "\n")
}

// DeltaBytes calculates the total bytes to transfer from changed regions.
func DeltaBytes(regions []nutanix.ChangedRegion) int64 {
	var total int64
	for _, r := range regions {
		total += r.Length
	}
	return total
}

// BuildDeltaPod creates a Pod and its regions ConfigMap for delta transfer.
// The pod uses busybox with wget+dd to download changed block regions
// from a Nutanix image and write them to an existing PVC.
func BuildDeltaPod(opts DeltaPodOptions) (*corev1.Pod, *corev1.ConfigMap) {
	regionsText := RegionsToText(opts.Regions)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            opts.Name + "-regions",
			Namespace:       opts.Namespace,
			OwnerReferences: []metav1.OwnerReference{opts.OwnerRef},
		},
		Data: map[string]string{
			"regions.txt": regionsText,
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            opts.Name,
			Namespace:       opts.Namespace,
			OwnerReferences: []metav1.OwnerReference{opts.OwnerRef},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    deltaContainerName,
				Image:   deltaImage,
				Command: []string{"/bin/sh", "-c", deltaScript},
				Env: []corev1.EnvVar{
					{
						Name: "PRISM_USERNAME",
						ValueFrom: &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: opts.SecretName,
								},
								Key: SecretKeyAccessKeyID,
							},
						},
					},
					{
						Name: "PRISM_PASSWORD",
						ValueFrom: &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: opts.SecretName,
								},
								Key: SecretKeySecretKey,
							},
						},
					},
					{
						Name:  "IMAGE_URL",
						Value: opts.ImageURL,
					},
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      deltaVolumeName,
						MountPath: deltaMountPath,
					},
					{
						Name:      deltaConfigVolume,
						MountPath: deltaConfigMount,
						ReadOnly:  true,
					},
				},
			}},
			Volumes: []corev1.Volume{
				{
					Name: deltaVolumeName,
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: opts.PVCName,
						},
					},
				},
				{
					Name: deltaConfigVolume,
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: cm.Name,
							},
						},
					},
				},
			},
		},
	}

	return pod, cm
}
