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

func TestListSubnets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/networking/v4.0/config/subnets") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		resp := subnetListResponse{
			Data: []Subnet{
				{ExtID: "subnet-1", Name: "vlan-100", VlanID: 100, SubnetType: "VLAN"},
				{ExtID: "subnet-2", Name: "vlan-200", VlanID: 200, SubnetType: "VLAN"},
				{ExtID: "subnet-3", Name: "overlay-net", VlanID: 0, SubnetType: "OVERLAY"},
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

	subnets, err := client.ListSubnets(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(subnets) != 3 {
		t.Fatalf("expected 3 subnets, got %d", len(subnets))
	}
	if subnets[0].ExtID != "subnet-1" {
		t.Fatalf("expected subnet-1, got %s", subnets[0].ExtID)
	}
	if subnets[0].Name != "vlan-100" {
		t.Fatalf("expected vlan-100, got %s", subnets[0].Name)
	}
	if subnets[0].VlanID != 100 {
		t.Fatalf("expected VLAN 100, got %d", subnets[0].VlanID)
	}
	if subnets[0].SubnetType != "VLAN" {
		t.Fatalf("expected VLAN type, got %s", subnets[0].SubnetType)
	}
	if subnets[2].SubnetType != "OVERLAY" {
		t.Fatalf("expected OVERLAY type, got %s", subnets[2].SubnetType)
	}
}

func TestListSubnets_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := subnetListResponse{
			Data:     []Subnet{},
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

	subnets, err := client.ListSubnets(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(subnets) != 0 {
		t.Fatalf("expected 0 subnets, got %d", len(subnets))
	}
}

func TestListSubnets_Paginated(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")

		if count == 1 {
			// First page: fill to pageSize
			subnets := make([]Subnet, pageSize)
			for i := range pageSize {
				subnets[i] = Subnet{ExtID: "subnet-" + strings.Repeat("a", i%10+1), Name: "net"}
			}
			resp := subnetListResponse{
				Data:     subnets,
				Metadata: &ListMetadata{TotalAvailableResults: pageSize + 2, Offset: 0},
			}
			_ = json.NewEncoder(w).Encode(resp)
		} else {
			// Second page: remaining
			resp := subnetListResponse{
				Data: []Subnet{
					{ExtID: "subnet-last-1", Name: "last-1", VlanID: 301},
					{ExtID: "subnet-last-2", Name: "last-2", VlanID: 302},
				},
				Metadata: &ListMetadata{TotalAvailableResults: pageSize + 2, Offset: pageSize},
			}
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	subnets, err := client.ListSubnets(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(subnets) != pageSize+2 {
		t.Fatalf("expected %d subnets, got %d", pageSize+2, len(subnets))
	}
	if callCount.Load() != 2 {
		t.Fatalf("expected 2 API calls, got %d", callCount.Load())
	}
}

func TestListSubnets_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": "internal error"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass", MaxRetries: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = client.ListSubnets(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ListSubnets") {
		t.Fatalf("expected error to contain 'ListSubnets', got: %v", err)
	}
}
