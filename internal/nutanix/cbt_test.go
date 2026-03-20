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

func TestDiscoverClusterForCBT(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/$actions/discover-cluster") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		// Verify request body
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body["vmExtId"] != testVMUUID {
			t.Errorf("expected vmExtId %s, got %s", testVMUUID, body["vmExtId"])
		}

		w.Header().Set("Content-Type", "application/json")
		resp := cbtDiscoverResponse{
			Data: CBTClusterInfo{
				PrismElementURL: "https://pe.example.com:9440",
				JWTToken:        "eyJhbGciOiJSUzI1NiJ9.test-jwt-token",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := client.DiscoverClusterForCBT(context.Background(), testVMUUID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.PrismElementURL != "https://pe.example.com:9440" {
		t.Fatalf("expected PE URL https://pe.example.com:9440, got %s", info.PrismElementURL)
	}
	if info.JWTToken != "eyJhbGciOiJSUzI1NiJ9.test-jwt-token" {
		t.Fatalf("expected JWT token, got %s", info.JWTToken)
	}
}

func TestDiscoverClusterForCBT_IncompleteResponse(t *testing.T) {
	tests := []struct {
		name string
		resp CBTClusterInfo
	}{
		{
			name: "missing PE URL",
			resp: CBTClusterInfo{JWTToken: "some-token"},
		},
		{
			name: "missing JWT token",
			resp: CBTClusterInfo{PrismElementURL: "https://pe.example.com:9440"},
		},
		{
			name: "both empty",
			resp: CBTClusterInfo{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(cbtDiscoverResponse{Data: tc.resp})
			}))
			defer server.Close()

			client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			_, err = client.DiscoverClusterForCBT(context.Background(), testVMUUID)
			if err == nil {
				t.Fatal("expected error for incomplete response")
			}
			if !strings.Contains(err.Error(), "incomplete response") {
				t.Fatalf("expected 'incomplete response' error, got: %v", err)
			}
		})
	}
}

func TestDiscoverClusterForCBT_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error": "forbidden"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = client.DiscoverClusterForCBT(context.Background(), testVMUUID)
	if err == nil {
		t.Fatal("expected error for API failure")
	}
}

func TestGetChangedRegions_SinglePage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify JWT cookie
		cookie, err := r.Cookie(CBTJWTCookieName)
		if err != nil {
			t.Errorf("expected %s cookie, got error: %v", CBTJWTCookieName, err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if cookie.Value != "test-jwt" {
			t.Errorf("expected JWT value 'test-jwt', got '%s'", cookie.Value)
		}

		// Verify query params
		q := r.URL.Query()
		if q.Get("vmExtId") != testVMUUID {
			t.Errorf("expected vmExtId=%s, got %s", testVMUUID, q.Get("vmExtId"))
		}
		if q.Get("snapshotExtId") != "snap-1" {
			t.Errorf("expected snapshotExtId=snap-1, got %s", q.Get("snapshotExtId"))
		}
		if q.Get("baseSnapshotExtId") != "snap-0" {
			t.Errorf("expected baseSnapshotExtId=snap-0, got %s", q.Get("baseSnapshotExtId"))
		}
		if q.Get("offset") != "0" {
			t.Errorf("expected offset=0, got %s", q.Get("offset"))
		}
		if q.Get("length") != "1073741824" {
			t.Errorf("expected length=1073741824, got %s", q.Get("length"))
		}
		if q.Get("blockSize") != "65536" {
			t.Errorf("expected blockSize=65536, got %s", q.Get("blockSize"))
		}

		w.Header().Set("Content-Type", "application/json")
		resp := changedRegionsResponse{
			Data: ChangedRegions{
				Regions: []ChangedRegion{
					{Offset: 0, Length: 65536},
					{Offset: 131072, Length: 65536},
					{Offset: 524288, Length: 131072},
				},
				NextOffset: nil, // single page
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, err := client.GetChangedRegions(context.Background(), server.URL, "test-jwt", testVMUUID, "snap-1", "snap-0", 0, 1073741824, 65536)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Regions) != 3 {
		t.Fatalf("expected 3 regions, got %d", len(result.Regions))
	}
	if result.Regions[0].Offset != 0 || result.Regions[0].Length != 65536 {
		t.Fatalf("unexpected first region: %+v", result.Regions[0])
	}
	if result.Regions[2].Offset != 524288 || result.Regions[2].Length != 131072 {
		t.Fatalf("unexpected third region: %+v", result.Regions[2])
	}
	if result.NextOffset != nil {
		t.Fatalf("expected nil NextOffset, got %d", *result.NextOffset)
	}
}

func TestGetChangedRegions_MultiPage(t *testing.T) {
	nextOff := int64(10000)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify JWT cookie is present
		_, err := r.Cookie(CBTJWTCookieName)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		resp := changedRegionsResponse{
			Data: ChangedRegions{
				Regions: []ChangedRegion{
					{Offset: 0, Length: 65536},
					{Offset: 65536, Length: 65536},
				},
				NextOffset: &nextOff,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, err := client.GetChangedRegions(context.Background(), server.URL, "jwt-token", testVMUUID, "snap-1", "snap-0", 0, 1073741824, 65536)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Regions) != 2 {
		t.Fatalf("expected 2 regions, got %d", len(result.Regions))
	}
	if result.NextOffset == nil {
		t.Fatal("expected non-nil NextOffset for multi-page")
	}
	if *result.NextOffset != 10000 {
		t.Fatalf("expected NextOffset=10000, got %d", *result.NextOffset)
	}
}

func TestGetChangedRegions_ZeroRegions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := r.Cookie(CBTJWTCookieName)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		resp := changedRegionsResponse{
			Data: ChangedRegions{
				Regions:    []ChangedRegion{},
				NextOffset: nil,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, err := client.GetChangedRegions(context.Background(), server.URL, "jwt-token", testVMUUID, "snap-1", "snap-0", 0, 1073741824, 65536)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Regions) != 0 {
		t.Fatalf("expected 0 regions, got %d", len(result.Regions))
	}
	if result.NextOffset != nil {
		t.Fatalf("expected nil NextOffset, got %d", *result.NextOffset)
	}
}

func TestGetChangedRegions_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = client.GetChangedRegions(context.Background(), server.URL, "jwt-token", testVMUUID, "snap-1", "snap-0", 0, 1073741824, 65536)
	if err == nil {
		t.Fatal("expected error for server error")
	}
}

func TestGetChangedRegions_TokenRefresh(t *testing.T) {
	var discoverCalls atomic.Int32
	refreshedToken := "refreshed-jwt-token"

	// Need server URL in handler closure; use a pointer that gets set after NewServer
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Discovery endpoint (PC)
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/$actions/discover-cluster") {
			discoverCalls.Add(1)
			resp := cbtDiscoverResponse{
				Data: CBTClusterInfo{
					PrismElementURL: serverURL, // point back to same server
					JWTToken:        refreshedToken,
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// Changed regions endpoint (PE)
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/changed-regions") {
			// Verify the refreshed token was used
			cookie, err := r.Cookie(CBTJWTCookieName)
			if err != nil {
				t.Errorf("expected %s cookie: %v", CBTJWTCookieName, err)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if cookie.Value != refreshedToken {
				t.Errorf("expected refreshed token '%s', got '%s'", refreshedToken, cookie.Value)
			}

			resp := changedRegionsResponse{
				Data: ChangedRegions{
					Regions:    []ChangedRegion{{Offset: 0, Length: 4096}},
					NextOffset: nil,
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	serverURL = server.URL

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Access the underlying httpClient to seed a stale token
	hc := client.(*httpClient)
	hc.cbtMu.Lock()
	hc.cbtTokens = map[string]*cbtTokenEntry{
		testVMUUID: {
			prismElementURL: server.URL,
			jwtToken:        "old-stale-jwt",
			discoveredAt:    time.Now().Add(-15 * time.Minute), // 15 min ago = stale
		},
	}
	hc.cbtMu.Unlock()

	// Call GetChangedRegions with the old token -- should trigger refresh
	result, err := client.GetChangedRegions(context.Background(), server.URL, "old-stale-jwt", testVMUUID, "snap-1", "snap-0", 0, 1024, 512)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(result.Regions))
	}

	// Verify discovery was called for token refresh
	if discoverCalls.Load() != 1 {
		t.Fatalf("expected 1 discover call (refresh), got %d", discoverCalls.Load())
	}
}

func TestGetChangedRegions_NoRefreshWhenFresh(t *testing.T) {
	var discoverCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/$actions/discover-cluster") {
			discoverCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/changed-regions") {
			resp := changedRegionsResponse{
				Data: ChangedRegions{
					Regions:    []ChangedRegion{{Offset: 0, Length: 4096}},
					NextOffset: nil,
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Seed a fresh token (just now)
	hc := client.(*httpClient)
	hc.cbtMu.Lock()
	hc.cbtTokens = map[string]*cbtTokenEntry{
		testVMUUID: {
			prismElementURL: server.URL,
			jwtToken:        "fresh-jwt",
			discoveredAt:    time.Now(), // fresh
		},
	}
	hc.cbtMu.Unlock()

	result, err := client.GetChangedRegions(context.Background(), server.URL, "fresh-jwt", testVMUUID, "snap-1", "snap-0", 0, 1024, 512)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(result.Regions))
	}

	// Verify no re-discovery was triggered
	if discoverCalls.Load() != 0 {
		t.Fatalf("expected 0 discover calls (token is fresh), got %d", discoverCalls.Load())
	}
}

func TestGetChangedRegions_PEURLTrailingSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify no double slash in path
		if strings.Contains(r.URL.Path, "//") {
			t.Errorf("URL contains double slash: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		resp := changedRegionsResponse{
			Data: ChangedRegions{
				Regions:    []ChangedRegion{},
				NextOffset: nil,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Pass PE URL with trailing slash
	_, err = client.GetChangedRegions(context.Background(), server.URL+"/", "jwt", testVMUUID, "snap-1", "snap-0", 0, 1024, 512)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
