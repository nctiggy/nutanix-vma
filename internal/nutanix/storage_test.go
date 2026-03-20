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
	"testing"
)

func TestListStorageContainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/PrismGateway/services/rest/v2.0/storage_containers") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		resp := storageContainerV2Response{
			Entities: []StorageContainer{
				{UUID: "sc-uuid-1", Name: "default-container"},
				{UUID: "sc-uuid-2", Name: "nfs-container"},
				{UUID: "sc-uuid-3", Name: "erasure-container"},
			},
			Metadata: &v2Metadata{GrandTotalEntities: 3, TotalEntities: 3},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Client is created with PC URL but ListStorageContainers takes PE URL directly
	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	containers, err := client.ListStorageContainers(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(containers) != 3 {
		t.Fatalf("expected 3 containers, got %d", len(containers))
	}
	if containers[0].UUID != "sc-uuid-1" {
		t.Fatalf("expected sc-uuid-1, got %s", containers[0].UUID)
	}
	if containers[0].Name != "default-container" {
		t.Fatalf("expected default-container, got %s", containers[0].Name)
	}
	if containers[1].Name != "nfs-container" {
		t.Fatalf("expected nfs-container, got %s", containers[1].Name)
	}
}

func TestListStorageContainers_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := storageContainerV2Response{
			Entities: []StorageContainer{},
			Metadata: &v2Metadata{GrandTotalEntities: 0, TotalEntities: 0},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	containers, err := client.ListStorageContainers(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(containers) != 0 {
		t.Fatalf("expected 0 containers, got %d", len(containers))
	}
}

func TestListStorageContainers_PEURLTrailingSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the URL was properly formed (no double slash)
		if strings.Contains(r.URL.Path, "//") {
			t.Errorf("URL contains double slash: %s", r.URL.Path)
		}

		resp := storageContainerV2Response{
			Entities: []StorageContainer{
				{UUID: "sc-1", Name: "container-1"},
			},
			Metadata: &v2Metadata{GrandTotalEntities: 1, TotalEntities: 1},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// PE URL with trailing slash should be handled
	containers, err := client.ListStorageContainers(context.Background(), server.URL+"/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
}

func TestListStorageContainers_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": "PE unavailable"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass", MaxRetries: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = client.ListStorageContainers(context.Background(), server.URL)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ListStorageContainers") {
		t.Fatalf("expected error to contain 'ListStorageContainers', got: %v", err)
	}
}
