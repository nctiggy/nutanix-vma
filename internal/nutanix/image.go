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

package nutanix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	imageBasePath = "/api/vmm/v4.0/images"
)

// imageResponse is the typed v4 API response for an image.
type imageResponse struct {
	Data Image `json:"data"`
}

// CreateImageFromDisk creates a disk image from a vDisk reference and returns the image UUID.
// The clusterRef parameter specifies the cluster hosting the source vDisk.
func (c *httpClient) CreateImageFromDisk(ctx context.Context, name string, diskUUID string, clusterRef string) (string, error) {
	url := fmt.Sprintf("%s%s", c.host, imageBasePath)

	body := map[string]any{
		"name": name,
		"type": "DISK_IMAGE",
		"source": map[string]any{
			"$objectType": "vmm.v4.images.VmDiskImageSource",
			"vmDiskExtId": diskUUID,
			"extId":       clusterRef,
		},
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("nutanix client: CreateImageFromDisk: failed to marshal body: %w", err)
	}

	var resp taskRefResponse
	if err := c.doJSON(ctx, "POST", url, strings.NewReader(string(bodyJSON)), &resp); err != nil {
		return "", fmt.Errorf("nutanix client: CreateImageFromDisk: %w", err)
	}

	if resp.Data.ExtID == "" {
		return "", fmt.Errorf("nutanix client: CreateImageFromDisk: no task ID returned")
	}

	task, err := c.PollTask(ctx, resp.Data.ExtID)
	if err != nil {
		return "", fmt.Errorf("nutanix client: CreateImageFromDisk: %w", err)
	}

	// Extract image UUID from task entities
	for _, entity := range task.EntitiesAffected {
		if entity.Rel == "vmm:images:image" || entity.Rel == "image" {
			return entity.ExtID, nil
		}
	}

	if len(task.EntitiesAffected) > 0 {
		return task.EntitiesAffected[0].ExtID, nil
	}

	return "", fmt.Errorf("nutanix client: CreateImageFromDisk: no image UUID in task entities")
}

// GetImage returns an image by UUID.
func (c *httpClient) GetImage(ctx context.Context, uuid string) (*Image, error) {
	url := fmt.Sprintf("%s%s/%s", c.host, imageBasePath, uuid)

	var resp imageResponse
	if err := c.doJSON(ctx, "GET", url, nil, &resp); err != nil {
		return nil, fmt.Errorf("nutanix client: GetImage: %w", err)
	}

	return &resp.Data, nil
}

// DownloadImage streams image data to the provided writer without buffering
// the entire image in memory.
func (c *httpClient) DownloadImage(ctx context.Context, uuid string, w io.Writer) error {
	url := fmt.Sprintf("%s%s/%s/file", c.host, imageBasePath, uuid)

	resp, err := c.doRequest(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("nutanix client: DownloadImage: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("nutanix client: DownloadImage: failed to stream image data: %w", err)
	}

	return nil
}

// DeleteImage deletes an image and waits for the task to complete.
func (c *httpClient) DeleteImage(ctx context.Context, uuid string) error {
	url := fmt.Sprintf("%s%s/%s", c.host, imageBasePath, uuid)

	var resp taskRefResponse
	if err := c.doJSON(ctx, "DELETE", url, nil, &resp); err != nil {
		return fmt.Errorf("nutanix client: DeleteImage: %w", err)
	}

	if resp.Data.ExtID == "" {
		return fmt.Errorf("nutanix client: DeleteImage: no task ID returned")
	}

	_, err := c.PollTask(ctx, resp.Data.ExtID)
	if err != nil {
		return fmt.Errorf("nutanix client: DeleteImage: %w", err)
	}

	return nil
}

// CloneVMFromRecoveryPoint creates a clone of a VM from a recovery point and returns
// the cloned VM's UUID. This is the fallback path when direct image creation from
// a vDisk reference fails.
func (c *httpClient) CloneVMFromRecoveryPoint(ctx context.Context, recoveryPointUUID string, vmName string) (string, error) {
	url := fmt.Sprintf("%s%s/%s/$actions/vm-recovery", c.host, recoveryPointBasePath, recoveryPointUUID)

	body := map[string]any{
		"vmRecoveryPoints": []map[string]any{
			{
				"vmName": vmName,
			},
		},
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("nutanix client: CloneVMFromRecoveryPoint: failed to marshal body: %w", err)
	}

	var resp taskRefResponse
	if err := c.doJSON(ctx, "POST", url, strings.NewReader(string(bodyJSON)), &resp); err != nil {
		return "", fmt.Errorf("nutanix client: CloneVMFromRecoveryPoint: %w", err)
	}

	if resp.Data.ExtID == "" {
		return "", fmt.Errorf("nutanix client: CloneVMFromRecoveryPoint: no task ID returned")
	}

	task, err := c.PollTask(ctx, resp.Data.ExtID)
	if err != nil {
		return "", fmt.Errorf("nutanix client: CloneVMFromRecoveryPoint: %w", err)
	}

	// Extract cloned VM UUID from task entities
	for _, entity := range task.EntitiesAffected {
		if entity.Rel == "vmm:ahv:config:vm" || entity.Rel == "vm" {
			return entity.ExtID, nil
		}
	}

	if len(task.EntitiesAffected) > 0 {
		return task.EntitiesAffected[0].ExtID, nil
	}

	return "", fmt.Errorf("nutanix client: CloneVMFromRecoveryPoint: no VM UUID in task entities")
}

// DeleteVM deletes a VM and waits for the task to complete.
func (c *httpClient) DeleteVM(ctx context.Context, uuid string) error {
	url := fmt.Sprintf("%s%s/%s", c.host, vmBasePath, uuid)

	var resp taskRefResponse
	if err := c.doJSON(ctx, "DELETE", url, nil, &resp); err != nil {
		return fmt.Errorf("nutanix client: DeleteVM: %w", err)
	}

	if resp.Data.ExtID == "" {
		return fmt.Errorf("nutanix client: DeleteVM: no task ID returned")
	}

	_, err := c.PollTask(ctx, resp.Data.ExtID)
	if err != nil {
		return fmt.Errorf("nutanix client: DeleteVM: %w", err)
	}

	return nil
}
