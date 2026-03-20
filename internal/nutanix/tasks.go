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
	"time"
)

const (
	// Task status values from Nutanix v4 API.
	TaskStatusQueued    = "QUEUED"
	TaskStatusRunning   = "RUNNING"
	TaskStatusSucceeded = "SUCCEEDED"
	TaskStatusFailed    = "FAILED"
	TaskStatusCancelled = "CANCELLED"

	// Default polling interval and timeout for task polling.
	defaultPollInterval = 2 * time.Second
	defaultPollTimeout  = 30 * time.Minute
)

// pollTask polls a Nutanix async task until it reaches a terminal state.
// Returns the completed task or an error if the task failed or timed out.
func (c *httpClient) pollTask(ctx context.Context, taskUUID string) (*Task, error) {
	return c.pollTaskWithOptions(ctx, taskUUID, defaultPollInterval, defaultPollTimeout)
}

// pollTaskWithOptions polls a task with configurable interval and timeout.
func (c *httpClient) pollTaskWithOptions(ctx context.Context, taskUUID string, interval, timeout time.Duration) (*Task, error) {
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("%s/api/prism/v4.0/config/tasks/%s", c.host, taskUUID)

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("nutanix client: task %s timed out after %s", taskUUID, timeout)
		}

		var task Task
		if err := c.doJSON(ctx, "GET", url, nil, &task); err != nil {
			return nil, fmt.Errorf("nutanix client: failed to poll task %s: %w", taskUUID, err)
		}

		switch task.Status {
		case TaskStatusSucceeded:
			return &task, nil
		case TaskStatusFailed:
			msg := "unknown error"
			if len(task.ErrorMessages) > 0 {
				msg = task.ErrorMessages[0].Message
			}
			return &task, fmt.Errorf("nutanix client: task %s failed: %s", taskUUID, msg)
		case TaskStatusCancelled:
			return &task, fmt.Errorf("nutanix client: task %s was cancelled", taskUUID)
		case TaskStatusQueued, TaskStatusRunning:
			// continue polling
		default:
			return &task, fmt.Errorf("nutanix client: task %s has unknown status: %s", taskUUID, task.Status)
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("nutanix client: context cancelled while polling task %s: %w", taskUUID, ctx.Err())
		case <-time.After(interval):
		}
	}
}
