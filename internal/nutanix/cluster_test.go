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

func TestListClusters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/clustermgmt/v4.0/config/clusters") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		resp := clusterListResponse{
			Data: []Cluster{
				{
					ExtID: "cluster-1",
					Name:  "pe-cluster-1",
					Network: &ClusterNetwork{
						ExternalAddress:        "10.0.0.1",
						ExternalDataServicesIP: "10.0.0.2",
					},
				},
			},
			Metadata: &ListMetadata{TotalAvailableResults: 1, Offset: 0},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clusters, err := client.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}
	if clusters[0].ExtID != "cluster-1" {
		t.Fatalf("expected cluster-1, got %s", clusters[0].ExtID)
	}
	if clusters[0].Name != "pe-cluster-1" {
		t.Fatalf("expected pe-cluster-1, got %s", clusters[0].Name)
	}
	if clusters[0].Network == nil {
		t.Fatal("expected network info")
	}
	if clusters[0].Network.ExternalAddress != "10.0.0.1" {
		t.Fatalf("expected 10.0.0.1, got %s", clusters[0].Network.ExternalAddress)
	}
	if clusters[0].Network.ExternalDataServicesIP != "10.0.0.2" {
		t.Fatalf("expected 10.0.0.2, got %s", clusters[0].Network.ExternalDataServicesIP)
	}
}

func TestListClusters_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := clusterListResponse{
			Data:     []Cluster{},
			Metadata: &ListMetadata{TotalAvailableResults: 0},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clusters, err := client.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 0 {
		t.Fatalf("expected 0 clusters, got %d", len(clusters))
	}
}

func TestListClusters_MultipleClusters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := clusterListResponse{
			Data: []Cluster{
				{
					ExtID: "cluster-1",
					Name:  "pe-cluster-prod",
					Network: &ClusterNetwork{
						ExternalAddress:        "10.0.1.10",
						ExternalDataServicesIP: "10.0.1.11",
					},
				},
				{
					ExtID: "cluster-2",
					Name:  "pe-cluster-dev",
					Network: &ClusterNetwork{
						ExternalAddress:        "10.0.2.10",
						ExternalDataServicesIP: "10.0.2.11",
					},
				},
				{
					ExtID:   "cluster-3",
					Name:    "pe-cluster-no-network",
					Network: nil,
				},
			},
			Metadata: &ListMetadata{TotalAvailableResults: 3, Offset: 0},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clusters, err := client.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 3 {
		t.Fatalf("expected 3 clusters, got %d", len(clusters))
	}

	// Verify PE IPs for auto-discovery
	if clusters[0].Network.ExternalAddress != "10.0.1.10" {
		t.Fatalf("expected 10.0.1.10, got %s", clusters[0].Network.ExternalAddress)
	}
	if clusters[1].Network.ExternalAddress != "10.0.2.10" {
		t.Fatalf("expected 10.0.2.10, got %s", clusters[1].Network.ExternalAddress)
	}

	// Cluster without network info should be handled gracefully
	if clusters[2].Network != nil {
		t.Fatalf("expected nil network for cluster-3, got %+v", clusters[2].Network)
	}
}

func TestListClusters_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error": "access denied"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = client.ListClusters(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ListClusters") {
		t.Fatalf("expected error to contain 'ListClusters', got: %v", err)
	}
}
