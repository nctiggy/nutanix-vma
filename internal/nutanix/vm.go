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
	"fmt"
	"strings"
)

const (
	vmBasePath = "/api/vmm/v4.0/ahv/config/vms"
	pageSize   = 500
)

// vmListResponse is the typed v4 API list response for VMs.
type vmListResponse struct {
	Data     []VM          `json:"data"`
	Metadata *ListMetadata `json:"metadata"`
}

// vmResponse is the typed v4 API single-entity response for a VM.
type vmResponse struct {
	Data VM `json:"data"`
}

// taskRefResponse captures the task extId returned by async operations.
type taskRefResponse struct {
	Data struct {
		ExtID string `json:"extId"`
	} `json:"data"`
}

// ListVMs returns all VMs from Prism Central, handling pagination.
func (c *httpClient) ListVMs(ctx context.Context) ([]VM, error) {
	var allVMs []VM
	offset := 0

	for {
		url := fmt.Sprintf("%s%s?$top=%d&$skip=%d", c.host, vmBasePath, pageSize, offset)

		var resp vmListResponse
		if err := c.doJSON(ctx, "GET", url, nil, &resp); err != nil {
			return nil, fmt.Errorf("nutanix client: ListVMs: %w", err)
		}

		allVMs = append(allVMs, resp.Data...)

		// Check if we have all results
		if resp.Metadata == nil || len(allVMs) >= resp.Metadata.TotalAvailableResults {
			break
		}
		offset += pageSize
	}

	return allVMs, nil
}

// GetVM returns a single VM by UUID.
func (c *httpClient) GetVM(ctx context.Context, uuid string) (*VM, error) {
	url := fmt.Sprintf("%s%s/%s", c.host, vmBasePath, uuid)

	var resp vmResponse
	if err := c.doJSON(ctx, "GET", url, nil, &resp); err != nil {
		return nil, fmt.Errorf("nutanix client: GetVM: %w", err)
	}

	return &resp.Data, nil
}

// PowerOffVM powers off a VM and waits for the task to complete.
func (c *httpClient) PowerOffVM(ctx context.Context, uuid string) error {
	url := fmt.Sprintf("%s%s/%s/$actions/power-off", c.host, vmBasePath, uuid)

	var resp taskRefResponse
	if err := c.doJSON(ctx, "POST", url, strings.NewReader("{}"), &resp); err != nil {
		return fmt.Errorf("nutanix client: PowerOffVM: %w", err)
	}

	if resp.Data.ExtID == "" {
		return fmt.Errorf("nutanix client: PowerOffVM: no task ID returned")
	}

	_, err := c.PollTask(ctx, resp.Data.ExtID)
	if err != nil {
		return fmt.Errorf("nutanix client: PowerOffVM: %w", err)
	}

	return nil
}

// PowerOnVM powers on a VM and waits for the task to complete.
func (c *httpClient) PowerOnVM(ctx context.Context, uuid string) error {
	url := fmt.Sprintf("%s%s/%s/$actions/power-on", c.host, vmBasePath, uuid)

	var resp taskRefResponse
	if err := c.doJSON(ctx, "POST", url, strings.NewReader("{}"), &resp); err != nil {
		return fmt.Errorf("nutanix client: PowerOnVM: %w", err)
	}

	if resp.Data.ExtID == "" {
		return fmt.Errorf("nutanix client: PowerOnVM: no task ID returned")
	}

	_, err := c.PollTask(ctx, resp.Data.ExtID)
	if err != nil {
		return fmt.Errorf("nutanix client: PowerOnVM: %w", err)
	}

	return nil
}

// GetVMPowerState returns the current power state of a VM.
func (c *httpClient) GetVMPowerState(ctx context.Context, uuid string) (PowerState, error) {
	vm, err := c.GetVM(ctx, uuid)
	if err != nil {
		return "", fmt.Errorf("nutanix client: GetVMPowerState: %w", err)
	}

	return PowerState(vm.PowerState), nil
}
