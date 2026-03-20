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
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// NutanixClient defines the interface for all Nutanix Prism API operations.
type NutanixClient interface {
	// VM operations
	ListVMs(ctx context.Context) ([]VM, error)
	GetVM(ctx context.Context, uuid string) (*VM, error)
	PowerOffVM(ctx context.Context, uuid string) error
	PowerOnVM(ctx context.Context, uuid string) error
	GetVMPowerState(ctx context.Context, uuid string) (PowerState, error)

	// Snapshot operations
	CreateRecoveryPoint(ctx context.Context, vmUUID string, name string) (string, error)
	GetRecoveryPoint(ctx context.Context, uuid string) (*RecoveryPoint, error)
	DeleteRecoveryPoint(ctx context.Context, uuid string) error

	// Image operations
	CreateImageFromDisk(ctx context.Context, name string, diskUUID string, clusterRef string) (string, error)
	GetImage(ctx context.Context, uuid string) (*Image, error)
	DownloadImage(ctx context.Context, uuid string, w io.Writer) error
	DeleteImage(ctx context.Context, uuid string) error

	// VM clone/restore
	CloneVMFromRecoveryPoint(ctx context.Context, recoveryPointUUID string, vmName string) (string, error)
	DeleteVM(ctx context.Context, uuid string) error

	// CBT operations (warm migration)
	DiscoverClusterForCBT(ctx context.Context, vmUUID string) (*CBTClusterInfo, error)
	GetChangedRegions(ctx context.Context, peURL string, jwtToken string, vmUUID string, snapshotUUID string, baseSnapshotUUID string, offset int64, length int64, blockSize int64) (*ChangedRegions, error)

	// Networking & storage
	ListSubnets(ctx context.Context) ([]Subnet, error)
	ListStorageContainers(ctx context.Context, peURL string) ([]StorageContainer, error)
	ListClusters(ctx context.Context) ([]Cluster, error)
}

// ClientConfig holds connection parameters for the Nutanix API client.
type ClientConfig struct {
	// Host is the Prism Central URL (e.g. https://prism.example.com:9440).
	Host string

	// Username for Basic Auth.
	Username string

	// Password for Basic Auth.
	Password string

	// InsecureSkipVerify disables TLS cert verification.
	InsecureSkipVerify bool

	// CACert is an optional PEM-encoded CA certificate.
	CACert []byte

	// Timeout for HTTP requests. Defaults to 30s.
	Timeout time.Duration

	// MaxRetries is the maximum number of retries on 429/5xx. Defaults to 3.
	MaxRetries int
}

// httpClient implements NutanixClient using net/http.
type httpClient struct {
	client     *http.Client
	host       string
	username   string
	password   string
	maxRetries int
}

// NewClient creates a new Nutanix API client.
func NewClient(config ClientConfig) (NutanixClient, error) {
	if config.Host == "" {
		return nil, errors.New("nutanix client: host is required")
	}
	if config.Username == "" || config.Password == "" {
		return nil, errors.New("nutanix client: username and password are required")
	}

	// Trim trailing slash from host
	config.Host = strings.TrimRight(config.Host, "/")

	timeout := config.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	maxRetries := config.MaxRetries
	if maxRetries == 0 {
		maxRetries = 3
	}

	transport, err := buildTransport(config.InsecureSkipVerify, config.CACert)
	if err != nil {
		return nil, fmt.Errorf("nutanix client: failed to build transport: %w", err)
	}

	return &httpClient{
		client: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		host:       config.Host,
		username:   config.Username,
		password:   config.Password,
		maxRetries: maxRetries,
	}, nil
}

// NutanixAPIError represents an error from the Nutanix API.
type NutanixAPIError struct {
	StatusCode int
	Message    string
	RequestURL string
}

func (e *NutanixAPIError) Error() string {
	return fmt.Sprintf("nutanix API error: %d %s (url: %s)", e.StatusCode, e.Message, e.RequestURL)
}

// doRequest executes an HTTP request with Basic Auth, retry, and error handling.
func (c *httpClient) doRequest(ctx context.Context, method, url string, body io.Reader) (*http.Response, error) {
	var lastErr error

	for attempt := range c.maxRetries + 1 {
		req, err := http.NewRequestWithContext(ctx, method, url, body)
		if err != nil {
			return nil, fmt.Errorf("nutanix client: failed to create request: %w", err)
		}

		setBasicAuth(req, c.username, c.password)
		req.Header.Set("Accept", "application/json")
		if body != nil && (method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch) {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("nutanix client: request failed: %w", err)
			if !shouldRetry(0, err) || attempt == c.maxRetries {
				return nil, lastErr
			}
			sleepWithContext(ctx, backoffDuration(attempt))
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		apiErr := &NutanixAPIError{
			StatusCode: resp.StatusCode,
			Message:    string(respBody),
			RequestURL: url,
		}

		if shouldRetry(resp.StatusCode, nil) && attempt < c.maxRetries {
			lastErr = apiErr
			sleepWithContext(ctx, backoffDuration(attempt))
			continue
		}

		return nil, apiErr
	}

	return nil, lastErr
}

// doJSON executes an HTTP request and decodes the response into dst.
func (c *httpClient) doJSON(ctx context.Context, method, url string, body io.Reader, dst any) error {
	resp, err := c.doRequest(ctx, method, url, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return fmt.Errorf("nutanix client: failed to decode response: %w", err)
		}
	}
	return nil
}

// shouldRetry returns true if the request should be retried.
func shouldRetry(statusCode int, err error) bool {
	if err != nil {
		return true // network errors are retryable
	}
	return statusCode == http.StatusTooManyRequests || statusCode >= 500
}

// backoffDuration returns the exponential backoff duration for the given attempt.
func backoffDuration(attempt int) time.Duration {
	base := time.Second
	return time.Duration(math.Pow(2, float64(attempt))) * base
}

// sleepWithContext sleeps for the given duration or until the context is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// --- Stub implementations for methods to be implemented in later stories ---

func (c *httpClient) DiscoverClusterForCBT(_ context.Context, _ string) (*CBTClusterInfo, error) {
	return nil, errors.New("not implemented: DiscoverClusterForCBT")
}

func (c *httpClient) GetChangedRegions(_ context.Context, _ string, _ string, _ string, _ string, _ string, _ int64, _ int64, _ int64) (*ChangedRegions, error) {
	return nil, errors.New("not implemented: GetChangedRegions")
}

func (c *httpClient) ListSubnets(_ context.Context) ([]Subnet, error) {
	return nil, errors.New("not implemented: ListSubnets")
}

func (c *httpClient) ListStorageContainers(_ context.Context, _ string) ([]StorageContainer, error) {
	return nil, errors.New("not implemented: ListStorageContainers")
}

func (c *httpClient) ListClusters(_ context.Context) ([]Cluster, error) {
	return nil, errors.New("not implemented: ListClusters")
}
