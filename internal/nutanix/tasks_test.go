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
	"time"
)

func TestPollTask_Succeeds(t *testing.T) {
	var pollCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := pollCount.Add(1)
		task := Task{
			ExtID:  "task-123",
			Status: TaskStatusRunning,
		}
		if count >= 3 {
			task.Status = TaskStatusSucceeded
		} else if count == 1 {
			task.Status = TaskStatusQueued
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(task)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Host:     server.URL,
		Username: "admin",
		Password: "pass",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hc := client.(*httpClient)
	task, err := hc.pollTaskWithOptions(context.Background(), "task-123", 10*time.Millisecond, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.Status != TaskStatusSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s", task.Status)
	}
	if pollCount.Load() < 3 {
		t.Fatalf("expected at least 3 polls, got %d", pollCount.Load())
	}
}

func TestPollTask_Fails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		task := Task{
			ExtID:  "task-456",
			Status: TaskStatusFailed,
			ErrorMessages: []TaskError{
				{Message: "disk not found"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(task)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Host:     server.URL,
		Username: "admin",
		Password: "pass",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hc := client.(*httpClient)
	_, err = hc.pollTaskWithOptions(context.Background(), "task-456", 10*time.Millisecond, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for failed task")
	}
	if !strings.Contains(err.Error(), "disk not found") {
		t.Fatalf("expected error to contain 'disk not found', got: %v", err)
	}
}

func TestPollTask_Cancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		task := Task{
			ExtID:  "task-789",
			Status: TaskStatusCancelled,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(task)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Host:     server.URL,
		Username: "admin",
		Password: "pass",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hc := client.(*httpClient)
	_, err = hc.pollTaskWithOptions(context.Background(), "task-789", 10*time.Millisecond, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for cancelled task")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected error to contain 'cancelled', got: %v", err)
	}
}

func TestPollTask_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		task := Task{
			ExtID:  "task-slow",
			Status: TaskStatusRunning,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(task)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Host:     server.URL,
		Username: "admin",
		Password: "pass",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hc := client.(*httpClient)
	_, err = hc.pollTaskWithOptions(context.Background(), "task-slow", 10*time.Millisecond, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}

func TestPollTask_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		task := Task{
			ExtID:  "task-ctx",
			Status: TaskStatusRunning,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(task)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Host:     server.URL,
		Username: "admin",
		Password: "pass",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	hc := client.(*httpClient)
	_, err = hc.pollTaskWithOptions(ctx, "task-ctx", 10*time.Millisecond, 10*time.Second)
	if err == nil {
		t.Fatal("expected error on context cancellation")
	}
	if !strings.Contains(err.Error(), "context") {
		t.Fatalf("expected context error, got: %v", err)
	}
}

func TestPollTask_EntitiesAffected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		task := Task{
			ExtID:  "task-entity",
			Status: TaskStatusSucceeded,
			EntitiesAffected: []TaskEntity{
				{ExtID: testVMUUID, Rel: "vmware:vm"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(task)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Host:     server.URL,
		Username: "admin",
		Password: "pass",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hc := client.(*httpClient)
	task, err := hc.pollTaskWithOptions(context.Background(), "task-entity", 10*time.Millisecond, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(task.EntitiesAffected) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(task.EntitiesAffected))
	}
	if task.EntitiesAffected[0].ExtID != testVMUUID {
		t.Fatalf("expected entity UUID vm-uuid-123, got %s", task.EntitiesAffected[0].ExtID)
	}
}

func TestPollTask_FailedNoErrorMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		task := Task{
			ExtID:  "task-fail-noemsg",
			Status: TaskStatusFailed,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(task)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Host:     server.URL,
		Username: "admin",
		Password: "pass",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hc := client.(*httpClient)
	_, err = hc.pollTaskWithOptions(context.Background(), "task-fail-noemsg", 10*time.Millisecond, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for failed task")
	}
	if !strings.Contains(err.Error(), "unknown error") {
		t.Fatalf("expected 'unknown error' for failed task without message, got: %v", err)
	}
}
