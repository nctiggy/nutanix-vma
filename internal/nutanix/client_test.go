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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewClient_Validation(t *testing.T) {
	tests := []struct {
		name    string
		config  ClientConfig
		wantErr string
	}{
		{
			name:    "missing host",
			config:  ClientConfig{Username: "admin", Password: "pass"},
			wantErr: "host is required",
		},
		{
			name:    "missing credentials",
			config:  ClientConfig{Host: "https://prism:9440"},
			wantErr: "username and password are required",
		},
		{
			name:    "missing password",
			config:  ClientConfig{Host: "https://prism:9440", Username: "admin"},
			wantErr: "username and password are required",
		},
		{
			name:   "valid config",
			config: ClientConfig{Host: "https://prism:9440", Username: "admin", Password: "pass"},
		},
		{
			name:    "invalid CA cert",
			config:  ClientConfig{Host: "https://prism:9440", Username: "admin", Password: "pass", CACert: []byte("not-a-cert")},
			wantErr: "failed to parse CA certificate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.config)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if client == nil {
				t.Fatal("expected non-nil client")
			}
		})
	}
}

func TestNewClient_TrimsTrailingSlash(t *testing.T) {
	c, err := NewClient(ClientConfig{
		Host:     "https://prism:9440/",
		Username: "admin",
		Password: "pass",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hc := c.(*httpClient)
	if strings.HasSuffix(hc.host, "/") {
		t.Fatalf("expected trailing slash to be trimmed, got %q", hc.host)
	}
}

func TestBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Host:     server.URL,
		Username: "admin",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	hc := client.(*httpClient)
	resp, err := hc.doRequest(context.Background(), "GET", server.URL+"/test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestBasicAuth_BadCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Host:     server.URL,
		Username: "admin",
		Password: "wrong",
	})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	hc := client.(*httpClient)
	_, err = hc.doRequest(context.Background(), "GET", server.URL+"/test", nil)
	if err == nil {
		t.Fatal("expected error for bad credentials")
	}

	apiErr, ok := err.(*NutanixAPIError)
	if !ok {
		t.Fatalf("expected NutanixAPIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", apiErr.StatusCode)
	}
}

func TestRetry_On429(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := callCount.Add(1)
		if count <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error": "rate limited"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data": "ok"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Host:       server.URL,
		Username:   "admin",
		Password:   "pass",
		MaxRetries: 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hc := client.(*httpClient)
	resp, err := hc.doRequest(context.Background(), "GET", server.URL+"/test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if callCount.Load() != 3 {
		t.Fatalf("expected 3 calls, got %d", callCount.Load())
	}
}

func TestRetry_On500(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := callCount.Add(1)
		if count <= 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error": "internal"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Host:       server.URL,
		Username:   "admin",
		Password:   "pass",
		MaxRetries: 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hc := client.(*httpClient)
	resp, err := hc.doRequest(context.Background(), "GET", server.URL+"/test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if callCount.Load() != 2 {
		t.Fatalf("expected 2 calls, got %d", callCount.Load())
	}
}

func TestRetry_ExhaustedReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error": "unavailable"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Host:       server.URL,
		Username:   "admin",
		Password:   "pass",
		MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hc := client.(*httpClient)
	_, err = hc.doRequest(context.Background(), "GET", server.URL+"/test", nil)
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}

	apiErr, ok := err.(*NutanixAPIError)
	if !ok {
		t.Fatalf("expected NutanixAPIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", apiErr.StatusCode)
	}
}

func TestNoRetry_On4xx(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error": "not found"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Host:       server.URL,
		Username:   "admin",
		Password:   "pass",
		MaxRetries: 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hc := client.(*httpClient)
	_, err = hc.doRequest(context.Background(), "GET", server.URL+"/test", nil)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	// 404 is not retryable, so only 1 call
	if callCount.Load() != 1 {
		t.Fatalf("expected 1 call (no retry on 404), got %d", callCount.Load())
	}
}

func TestDoJSON(t *testing.T) {
	type testResp struct {
		Name string `json:"name"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(testResp{Name: "test-vm"})
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
	var result testResp
	err = hc.doJSON(context.Background(), "GET", server.URL+"/test", nil, &result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != "test-vm" {
		t.Fatalf("expected name 'test-vm', got %q", result.Name)
	}
}

func TestContentTypeHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Error("expected Accept: application/json header")
		}
		if r.Method == http.MethodPost {
			if r.Header.Get("Content-Type") != "application/json" {
				t.Error("expected Content-Type: application/json on POST")
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
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

	// Test GET (no Content-Type)
	resp, err := hc.doRequest(context.Background(), "GET", server.URL+"/test", nil)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	_ = resp.Body.Close()

	// Test POST (should have Content-Type)
	resp, err = hc.doRequest(context.Background(), "POST", server.URL+"/test", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	_ = resp.Body.Close()
}

func TestStubMethods_ReturnNotImplemented(t *testing.T) {
	client, err := NewClient(ClientConfig{
		Host:     "https://prism:9440",
		Username: "admin",
		Password: "pass",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()

	// VM methods are now implemented in vm.go -- skip them here

	_, err = client.CreateRecoveryPoint(ctx, "uuid", "name")
	assertNotImplemented(t, err, "CreateRecoveryPoint")

	_, err = client.GetRecoveryPoint(ctx, "uuid")
	assertNotImplemented(t, err, "GetRecoveryPoint")

	err = client.DeleteRecoveryPoint(ctx, "uuid")
	assertNotImplemented(t, err, "DeleteRecoveryPoint")

	_, err = client.CreateImageFromDisk(ctx, "name", "disk", "cluster")
	assertNotImplemented(t, err, "CreateImageFromDisk")

	_, err = client.GetImage(ctx, "uuid")
	assertNotImplemented(t, err, "GetImage")

	err = client.DownloadImage(ctx, "uuid", io.Discard)
	assertNotImplemented(t, err, "DownloadImage")

	err = client.DeleteImage(ctx, "uuid")
	assertNotImplemented(t, err, "DeleteImage")

	_, err = client.CloneVMFromRecoveryPoint(ctx, "rp", "name")
	assertNotImplemented(t, err, "CloneVMFromRecoveryPoint")

	err = client.DeleteVM(ctx, "uuid")
	assertNotImplemented(t, err, "DeleteVM")

	_, err = client.DiscoverClusterForCBT(ctx, "uuid")
	assertNotImplemented(t, err, "DiscoverClusterForCBT")

	_, err = client.GetChangedRegions(ctx, "url", "jwt", "vm", "snap", "base", 0, 0, 0)
	assertNotImplemented(t, err, "GetChangedRegions")

	_, err = client.ListSubnets(ctx)
	assertNotImplemented(t, err, "ListSubnets")

	_, err = client.ListStorageContainers(ctx, "url")
	assertNotImplemented(t, err, "ListStorageContainers")

	_, err = client.ListClusters(ctx)
	assertNotImplemented(t, err, "ListClusters")
}

func assertNotImplemented(t *testing.T, err error, methodName string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error, got nil", methodName)
	}
	expected := "not implemented: " + methodName
	if err.Error() != expected {
		t.Fatalf("%s: expected %q, got %q", methodName, expected, err.Error())
	}
}

func TestInsecureSkipVerify(t *testing.T) {
	client, err := NewClient(ClientConfig{
		Host:               "https://prism:9440",
		Username:           "admin",
		Password:           "pass",
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestCustomTimeout(t *testing.T) {
	client, err := NewClient(ClientConfig{
		Host:     "https://prism:9440",
		Username: "admin",
		Password: "pass",
		Timeout:  60 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hc := client.(*httpClient)
	if hc.client.Timeout != 60*time.Second {
		t.Fatalf("expected 60s timeout, got %v", hc.client.Timeout)
	}
}

func TestDefaultTimeout(t *testing.T) {
	client, err := NewClient(ClientConfig{
		Host:     "https://prism:9440",
		Username: "admin",
		Password: "pass",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hc := client.(*httpClient)
	if hc.client.Timeout != 30*time.Second {
		t.Fatalf("expected 30s default timeout, got %v", hc.client.Timeout)
	}
}

func TestNutanixAPIError_Error(t *testing.T) {
	err := &NutanixAPIError{
		StatusCode: 404,
		Message:    "not found",
		RequestURL: "https://prism:9440/api/test",
	}
	got := err.Error()
	if !strings.Contains(got, "404") || !strings.Contains(got, "not found") {
		t.Fatalf("unexpected error string: %s", got)
	}
}

func TestBackoffDuration(t *testing.T) {
	d0 := backoffDuration(0)
	if d0 != 1*time.Second {
		t.Fatalf("expected 1s for attempt 0, got %v", d0)
	}
	d1 := backoffDuration(1)
	if d1 != 2*time.Second {
		t.Fatalf("expected 2s for attempt 1, got %v", d1)
	}
	d2 := backoffDuration(2)
	if d2 != 4*time.Second {
		t.Fatalf("expected 4s for attempt 2, got %v", d2)
	}
}
