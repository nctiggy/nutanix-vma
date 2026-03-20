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
	"strings"
)

const (
	recoveryPointBasePath = "/api/dataprotection/v4.0/config/recovery-points"
)

// recoveryPointResponse is the typed v4 API response for a recovery point.
type recoveryPointResponse struct {
	Data RecoveryPoint `json:"data"`
}

// CreateRecoveryPoint creates a VM recovery point (snapshot) and returns its UUID.
// It posts to the v4 data protection API, polls the resulting task, and extracts the
// recovery point UUID from the task's affected entities.
func (c *httpClient) CreateRecoveryPoint(ctx context.Context, vmUUID string, name string) (string, error) {
	url := fmt.Sprintf("%s%s", c.host, recoveryPointBasePath)

	body := map[string]any{
		"name":              name,
		"recoveryPointType": "APPLICATION_CONSISTENT",
		"vmRecoveryPoints": []map[string]any{
			{
				"vmExtId": vmUUID,
			},
		},
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("nutanix client: CreateRecoveryPoint: failed to marshal body: %w", err)
	}

	var resp taskRefResponse
	if err := c.doJSON(ctx, "POST", url, strings.NewReader(string(bodyJSON)), &resp); err != nil {
		return "", fmt.Errorf("nutanix client: CreateRecoveryPoint: %w", err)
	}

	if resp.Data.ExtID == "" {
		return "", fmt.Errorf("nutanix client: CreateRecoveryPoint: no task ID returned")
	}

	task, err := c.PollTask(ctx, resp.Data.ExtID)
	if err != nil {
		return "", fmt.Errorf("nutanix client: CreateRecoveryPoint: %w", err)
	}

	// Extract recovery point UUID from task entities
	for _, entity := range task.EntitiesAffected {
		if entity.Rel == "dataprotection:config:recovery-point" || entity.Rel == "recovery-point" {
			return entity.ExtID, nil
		}
	}

	// If no specific rel match, return first entity
	if len(task.EntitiesAffected) > 0 {
		return task.EntitiesAffected[0].ExtID, nil
	}

	return "", fmt.Errorf("nutanix client: CreateRecoveryPoint: no recovery point UUID in task entities")
}

// GetRecoveryPoint returns a recovery point by UUID.
func (c *httpClient) GetRecoveryPoint(ctx context.Context, uuid string) (*RecoveryPoint, error) {
	url := fmt.Sprintf("%s%s/%s", c.host, recoveryPointBasePath, uuid)

	var resp recoveryPointResponse
	if err := c.doJSON(ctx, "GET", url, nil, &resp); err != nil {
		return nil, fmt.Errorf("nutanix client: GetRecoveryPoint: %w", err)
	}

	return &resp.Data, nil
}

// DeleteRecoveryPoint deletes a recovery point and waits for the task to complete.
func (c *httpClient) DeleteRecoveryPoint(ctx context.Context, uuid string) error {
	url := fmt.Sprintf("%s%s/%s", c.host, recoveryPointBasePath, uuid)

	var resp taskRefResponse
	if err := c.doJSON(ctx, "DELETE", url, nil, &resp); err != nil {
		return fmt.Errorf("nutanix client: DeleteRecoveryPoint: %w", err)
	}

	if resp.Data.ExtID == "" {
		return fmt.Errorf("nutanix client: DeleteRecoveryPoint: no task ID returned")
	}

	_, err := c.PollTask(ctx, resp.Data.ExtID)
	if err != nil {
		return fmt.Errorf("nutanix client: DeleteRecoveryPoint: %w", err)
	}

	return nil
}
