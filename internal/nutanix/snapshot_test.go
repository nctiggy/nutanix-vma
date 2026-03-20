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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCreateRecoveryPoint(t *testing.T) {
	var taskPolls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// POST to create recovery point
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/recovery-points") {
			// Verify request body
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if body["name"] != "test-snapshot" {
				t.Errorf("expected name 'test-snapshot', got %v", body["name"])
			}
			vmRPs, ok := body["vmRecoveryPoints"].([]any)
			if !ok || len(vmRPs) == 0 {
				t.Error("expected vmRecoveryPoints in body")
			}

			resp := taskRefResponse{}
			resp.Data.ExtID = "task-create-rp"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// GET task
		if strings.Contains(r.URL.Path, "/tasks/") {
			count := taskPolls.Add(1)
			task := Task{ExtID: "task-create-rp", Status: TaskStatusRunning}
			if count >= 2 {
				task.Status = TaskStatusSucceeded
				task.EntitiesAffected = []TaskEntity{
					{ExtID: "rp-uuid-123", Rel: "dataprotection:config:recovery-point"},
				}
			}
			_ = json.NewEncoder(w).Encode(task)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rpUUID, err := client.CreateRecoveryPoint(context.Background(), testVMUUID, "test-snapshot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rpUUID != "rp-uuid-123" {
		t.Fatalf("expected rp-uuid-123, got %s", rpUUID)
	}
}

func TestCreateRecoveryPoint_FallbackEntity(t *testing.T) {
	// Tests that we fall back to the first entity if no specific rel match
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost {
			resp := taskRefResponse{}
			resp.Data.ExtID = "task-rp"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			task := Task{
				ExtID:  "task-rp",
				Status: TaskStatusSucceeded,
				EntitiesAffected: []TaskEntity{
					{ExtID: "rp-fallback-uuid", Rel: "some-unknown-rel"},
				},
			}
			_ = json.NewEncoder(w).Encode(task)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rpUUID, err := client.CreateRecoveryPoint(context.Background(), "vm-1", "snap")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rpUUID != "rp-fallback-uuid" {
		t.Fatalf("expected rp-fallback-uuid, got %s", rpUUID)
	}
}

func TestCreateRecoveryPoint_NoEntities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost {
			resp := taskRefResponse{}
			resp.Data.ExtID = "task-rp"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			task := Task{ExtID: "task-rp", Status: TaskStatusSucceeded}
			_ = json.NewEncoder(w).Encode(task)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = client.CreateRecoveryPoint(context.Background(), "vm-1", "snap")
	if err == nil {
		t.Fatal("expected error when no entities in task")
	}
	if !strings.Contains(err.Error(), "no recovery point UUID") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateRecoveryPoint_TaskFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost {
			resp := taskRefResponse{}
			resp.Data.ExtID = testTaskFailID
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			task := Task{
				ExtID:         testTaskFailID,
				Status:        TaskStatusFailed,
				ErrorMessages: []TaskError{{Message: "insufficient space for snapshot"}},
			}
			_ = json.NewEncoder(w).Encode(task)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = client.CreateRecoveryPoint(context.Background(), "vm-1", "snap")
	if err == nil {
		t.Fatal("expected error for failed task")
	}
	if !strings.Contains(err.Error(), "insufficient space") {
		t.Fatalf("expected error about 'insufficient space', got: %v", err)
	}
}

func TestGetRecoveryPoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/rp-uuid-123") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		resp := recoveryPointResponse{
			Data: RecoveryPoint{
				ExtID:             "rp-uuid-123",
				Name:              "test-snapshot",
				Status:            "COMPLETE",
				VMExtID:           testVMUUID,
				RecoveryPointType: "APPLICATION_CONSISTENT",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rp, err := client.GetRecoveryPoint(context.Background(), "rp-uuid-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rp.ExtID != "rp-uuid-123" {
		t.Fatalf("expected rp-uuid-123, got %s", rp.ExtID)
	}
	if rp.Name != "test-snapshot" {
		t.Fatalf("expected test-snapshot, got %s", rp.Name)
	}
	if rp.Status != "COMPLETE" {
		t.Fatalf("expected COMPLETE, got %s", rp.Status)
	}
	if rp.VMExtID != testVMUUID {
		t.Fatalf("expected vm-uuid-123, got %s", rp.VMExtID)
	}
}

func TestGetRecoveryPoint_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error": "recovery point not found"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = client.GetRecoveryPoint(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing recovery point")
	}
}

func TestDeleteRecoveryPoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/recovery-points/") {
			resp := taskRefResponse{}
			resp.Data.ExtID = "task-delete-rp"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			task := Task{ExtID: "task-delete-rp", Status: TaskStatusSucceeded}
			_ = json.NewEncoder(w).Encode(task)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = client.DeleteRecoveryPoint(context.Background(), "rp-uuid-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteRecoveryPoint_TaskFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodDelete {
			resp := taskRefResponse{}
			resp.Data.ExtID = testTaskFailID
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			task := Task{
				ExtID:         testTaskFailID,
				Status:        TaskStatusFailed,
				ErrorMessages: []TaskError{{Message: "recovery point in use"}},
			}
			_ = json.NewEncoder(w).Encode(task)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = client.DeleteRecoveryPoint(context.Background(), "rp-uuid-123")
	if err == nil {
		t.Fatal("expected error for failed delete task")
	}
	if !strings.Contains(err.Error(), "recovery point in use") {
		t.Fatalf("expected error about 'in use', got: %v", err)
	}
}
