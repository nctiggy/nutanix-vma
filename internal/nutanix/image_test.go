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
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Shared test constants for the nutanix package tests.
const (
	testTaskFailID = "task-fail"
	testVMUUID     = "vm-uuid-123"
)

func TestCreateImageFromDisk(t *testing.T) {
	var taskPolls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/images") {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if body["name"] != "migration-disk-0" {
				t.Errorf("expected name 'migration-disk-0', got %v", body["name"])
			}
			if body["type"] != "DISK_IMAGE" {
				t.Errorf("expected type 'DISK_IMAGE', got %v", body["type"])
			}
			source, ok := body["source"].(map[string]any)
			if !ok {
				t.Error("expected source in body")
			} else if source["vmDiskExtId"] != "vdisk-uuid-1" {
				t.Errorf("expected vmDiskExtId 'vdisk-uuid-1', got %v", source["vmDiskExtId"])
			}

			resp := taskRefResponse{}
			resp.Data.ExtID = "task-create-img"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			count := taskPolls.Add(1)
			task := Task{ExtID: "task-create-img", Status: TaskStatusRunning}
			if count >= 2 {
				task.Status = TaskStatusSucceeded
				task.EntitiesAffected = []TaskEntity{
					{ExtID: "img-uuid-456", Rel: "vmm:images:image"},
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

	imgUUID, err := client.CreateImageFromDisk(context.Background(), "migration-disk-0", "vdisk-uuid-1", "cluster-ref-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if imgUUID != "img-uuid-456" {
		t.Fatalf("expected img-uuid-456, got %s", imgUUID)
	}
}

func TestCreateImageFromDisk_TaskFails(t *testing.T) {
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
				ErrorMessages: []TaskError{{Message: "disk not found"}},
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

	_, err = client.CreateImageFromDisk(context.Background(), "img", "vdisk", "cluster")
	if err == nil {
		t.Fatal("expected error for failed task")
	}
	if !strings.Contains(err.Error(), "disk not found") {
		t.Fatalf("expected error about 'disk not found', got: %v", err)
	}
}

func TestCreateImageFromDisk_NoEntities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost {
			resp := taskRefResponse{}
			resp.Data.ExtID = "task-img"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			task := Task{ExtID: "task-img", Status: TaskStatusSucceeded}
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

	_, err = client.CreateImageFromDisk(context.Background(), "img", "vdisk", "cluster")
	if err == nil {
		t.Fatal("expected error when no entities in task")
	}
	if !strings.Contains(err.Error(), "no image UUID") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/img-uuid-456") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		resp := imageResponse{
			Data: Image{
				ExtID:     "img-uuid-456",
				Name:      "migration-disk-0",
				Type:      "DISK_IMAGE",
				SizeBytes: 107374182400,
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

	img, err := client.GetImage(context.Background(), "img-uuid-456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if img.ExtID != "img-uuid-456" {
		t.Fatalf("expected img-uuid-456, got %s", img.ExtID)
	}
	if img.Name != "migration-disk-0" {
		t.Fatalf("expected migration-disk-0, got %s", img.Name)
	}
	if img.Type != "DISK_IMAGE" {
		t.Fatalf("expected DISK_IMAGE, got %s", img.Type)
	}
	if img.SizeBytes != 107374182400 {
		t.Fatalf("expected 100GB, got %d", img.SizeBytes)
	}
}

func TestGetImage_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error": "image not found"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = client.GetImage(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestDownloadImage(t *testing.T) {
	imageData := make([]byte, 4096)
	_, _ = rand.Read(imageData)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/file") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(imageData)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	err = client.DownloadImage(context.Background(), "img-uuid-456", &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.Len() != len(imageData) {
		t.Fatalf("expected %d bytes, got %d", len(imageData), buf.Len())
	}
	if !bytes.Equal(buf.Bytes(), imageData) {
		t.Fatal("downloaded data does not match original")
	}
}

func TestDownloadImage_Streaming(t *testing.T) {
	largeData := make([]byte, 1024*1024) // 1MB
	_, _ = rand.Read(largeData)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(largeData)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	err = client.DownloadImage(context.Background(), "img-1", &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.Len() != len(largeData) {
		t.Fatalf("expected %d bytes, got %d", len(largeData), buf.Len())
	}
}

func TestDownloadImage_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error": "image not found"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = client.DownloadImage(context.Background(), "nonexistent", io.Discard)
	if err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestDeleteImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/images/") {
			resp := taskRefResponse{}
			resp.Data.ExtID = "task-delete-img"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			task := Task{ExtID: "task-delete-img", Status: TaskStatusSucceeded}
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

	err = client.DeleteImage(context.Background(), "img-uuid-456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteImage_TaskFails(t *testing.T) {
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
				ErrorMessages: []TaskError{{Message: "image in use by VM"}},
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

	err = client.DeleteImage(context.Background(), "img-1")
	if err == nil {
		t.Fatal("expected error for failed delete task")
	}
	if !strings.Contains(err.Error(), "image in use") {
		t.Fatalf("expected error about 'image in use', got: %v", err)
	}
}

func TestCloneVMFromRecoveryPoint(t *testing.T) {
	var taskPolls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/$actions/vm-recovery") {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			vmRPs, ok := body["vmRecoveryPoints"].([]any)
			if !ok || len(vmRPs) == 0 {
				t.Error("expected vmRecoveryPoints in body")
			}

			resp := taskRefResponse{}
			resp.Data.ExtID = "task-clone-vm"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			count := taskPolls.Add(1)
			task := Task{ExtID: "task-clone-vm", Status: TaskStatusRunning}
			if count >= 2 {
				task.Status = TaskStatusSucceeded
				task.EntitiesAffected = []TaskEntity{
					{ExtID: "cloned-vm-uuid", Rel: "vmm:ahv:config:vm"},
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

	vmUUID, err := client.CloneVMFromRecoveryPoint(context.Background(), "rp-uuid-123", "clone-name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vmUUID != "cloned-vm-uuid" {
		t.Fatalf("expected cloned-vm-uuid, got %s", vmUUID)
	}
}

func TestCloneVMFromRecoveryPoint_TaskFails(t *testing.T) {
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
				ErrorMessages: []TaskError{{Message: "recovery point expired"}},
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

	_, err = client.CloneVMFromRecoveryPoint(context.Background(), "rp-1", "clone")
	if err == nil {
		t.Fatal("expected error for failed task")
	}
	if !strings.Contains(err.Error(), "recovery point expired") {
		t.Fatalf("expected error about 'expired', got: %v", err)
	}
}

func TestCloneVMFromRecoveryPoint_NoEntities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost {
			resp := taskRefResponse{}
			resp.Data.ExtID = "task-clone"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			task := Task{ExtID: "task-clone", Status: TaskStatusSucceeded}
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

	_, err = client.CloneVMFromRecoveryPoint(context.Background(), "rp-1", "clone")
	if err == nil {
		t.Fatal("expected error when no entities in task")
	}
	if !strings.Contains(err.Error(), "no VM UUID") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteVM(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/vms/") {
			resp := taskRefResponse{}
			resp.Data.ExtID = "task-delete-vm"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			task := Task{ExtID: "task-delete-vm", Status: TaskStatusSucceeded}
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

	err = client.DeleteVM(context.Background(), testVMUUID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteVM_TaskFails(t *testing.T) {
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
				ErrorMessages: []TaskError{{Message: "VM is running"}},
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

	err = client.DeleteVM(context.Background(), "vm-1")
	if err == nil {
		t.Fatal("expected error for failed delete task")
	}
	if !strings.Contains(err.Error(), "VM is running") {
		t.Fatalf("expected error about 'VM is running', got: %v", err)
	}
}

func TestFallbackPath_CloneImageDelete(t *testing.T) {
	var phase atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/$actions/vm-recovery") {
			phase.Store(1)
			resp := taskRefResponse{}
			resp.Data.ExtID = "task-clone"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/images") {
			phase.Store(2)
			resp := taskRefResponse{}
			resp.Data.ExtID = "task-create-img"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/vms/") {
			phase.Store(3)
			resp := taskRefResponse{}
			resp.Data.ExtID = "task-delete-vm"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			switch currentPhase := phase.Load(); currentPhase {
			case 1:
				task := Task{
					ExtID:  "task-clone",
					Status: TaskStatusSucceeded,
					EntitiesAffected: []TaskEntity{
						{ExtID: "cloned-vm-uuid", Rel: "vmm:ahv:config:vm"},
					},
				}
				_ = json.NewEncoder(w).Encode(task)
			case 2:
				task := Task{
					ExtID:  "task-create-img",
					Status: TaskStatusSucceeded,
					EntitiesAffected: []TaskEntity{
						{ExtID: "fallback-img-uuid", Rel: "vmm:images:image"},
					},
				}
				_ = json.NewEncoder(w).Encode(task)
			case 3:
				task := Task{ExtID: "task-delete-vm", Status: TaskStatusSucceeded}
				_ = json.NewEncoder(w).Encode(task)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()

	clonedVMUUID, err := client.CloneVMFromRecoveryPoint(ctx, "rp-uuid", "temp-clone")
	if err != nil {
		t.Fatalf("clone failed: %v", err)
	}
	if clonedVMUUID != "cloned-vm-uuid" {
		t.Fatalf("expected cloned-vm-uuid, got %s", clonedVMUUID)
	}

	imgUUID, err := client.CreateImageFromDisk(ctx, "fallback-img", "cloned-vdisk", "cluster-ref")
	if err != nil {
		t.Fatalf("create image failed: %v", err)
	}
	if imgUUID != "fallback-img-uuid" {
		t.Fatalf("expected fallback-img-uuid, got %s", imgUUID)
	}

	err = client.DeleteVM(ctx, clonedVMUUID)
	if err != nil {
		t.Fatalf("delete clone failed: %v", err)
	}

	if phase.Load() != 3 {
		t.Fatalf("expected all 3 phases, ended at phase %d", phase.Load())
	}
}
