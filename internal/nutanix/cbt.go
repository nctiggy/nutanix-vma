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
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	cbtDiscoverPath       = "/api/storage/v4.0/config/changed-regions/$actions/discover-cluster"
	cbtChangedRegionsPath = "/api/storage/v4.0/config/changed-regions"

	// CBTTokenMaxAge is the maximum age of a CBT JWT token before refresh.
	// Nutanix JWT tokens expire after 15 minutes; we refresh at 12 to avoid edge cases.
	CBTTokenMaxAge = 12 * time.Minute

	// CBTJWTCookieName is the cookie name used to pass JWT tokens to Prism Element for CBT.
	CBTJWTCookieName = "NTNX_IGW_SESSION"
)

// cbtTokenEntry caches a CBT discovery result with its timestamp for token refresh tracking.
type cbtTokenEntry struct {
	prismElementURL string
	jwtToken        string
	discoveredAt    time.Time
}

// cbtDiscoverResponse is the v4 API response for CBT cluster discovery.
type cbtDiscoverResponse struct {
	Data CBTClusterInfo `json:"data"`
}

// changedRegionsResponse is the v4 API response for changed regions queries.
type changedRegionsResponse struct {
	Data ChangedRegions `json:"data"`
}

// DiscoverClusterForCBT calls Prism Central to discover the Prism Element URL and JWT token
// needed for CBT changed-region queries on a specific VM.
func (c *httpClient) DiscoverClusterForCBT(ctx context.Context, vmUUID string) (*CBTClusterInfo, error) {
	url := fmt.Sprintf("%s%s", c.host, cbtDiscoverPath)

	body := map[string]string{
		"vmExtId": vmUUID,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("nutanix client: DiscoverClusterForCBT: failed to marshal body: %w", err)
	}

	var resp cbtDiscoverResponse
	if err := c.doJSON(ctx, http.MethodPost, url, strings.NewReader(string(bodyJSON)), &resp); err != nil {
		return nil, fmt.Errorf("nutanix client: DiscoverClusterForCBT: %w", err)
	}

	if resp.Data.PrismElementURL == "" || resp.Data.JWTToken == "" {
		return nil, fmt.Errorf("nutanix client: DiscoverClusterForCBT: incomplete response (missing PE URL or JWT token)")
	}

	// Cache the token for refresh tracking
	c.cbtMu.Lock()
	if c.cbtTokens == nil {
		c.cbtTokens = make(map[string]*cbtTokenEntry)
	}
	c.cbtTokens[vmUUID] = &cbtTokenEntry{
		prismElementURL: resp.Data.PrismElementURL,
		jwtToken:        resp.Data.JWTToken,
		discoveredAt:    time.Now(),
	}
	c.cbtMu.Unlock()

	return &resp.Data, nil
}

// GetChangedRegions queries Prism Element for changed block regions between two snapshots.
// The JWT token is passed via the NTNX_IGW_SESSION cookie. If the cached token is older
// than 12 minutes, a re-discovery is triggered automatically to get a fresh token.
func (c *httpClient) GetChangedRegions(ctx context.Context, peURL string, jwtToken string, vmUUID string, snapshotUUID string, baseSnapshotUUID string, offset int64, length int64, blockSize int64) (*ChangedRegions, error) {
	effectiveToken := jwtToken
	effectivePEURL := peURL

	// Check if token needs refresh (older than 12 minutes)
	c.cbtMu.RLock()
	entry, cached := c.cbtTokens[vmUUID]
	c.cbtMu.RUnlock()

	if cached && time.Since(entry.discoveredAt) > CBTTokenMaxAge {
		info, err := c.DiscoverClusterForCBT(ctx, vmUUID)
		if err != nil {
			return nil, fmt.Errorf("nutanix client: GetChangedRegions: token refresh failed: %w", err)
		}
		effectiveToken = info.JWTToken
		effectivePEURL = info.PrismElementURL
	}

	// Build URL with query params
	effectivePEURL = strings.TrimRight(effectivePEURL, "/")
	reqURL := fmt.Sprintf("%s%s?vmExtId=%s&snapshotExtId=%s&baseSnapshotExtId=%s&offset=%d&length=%d&blockSize=%d",
		effectivePEURL, cbtChangedRegionsPath, vmUUID, snapshotUUID, baseSnapshotUUID, offset, length, blockSize)

	// Create custom request with JWT cookie (PE uses JWT auth, not Basic Auth)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("nutanix client: GetChangedRegions: failed to create request: %w", err)
	}
	req.AddCookie(&http.Cookie{Name: CBTJWTCookieName, Value: effectiveToken})
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nutanix client: GetChangedRegions: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &NutanixAPIError{
			StatusCode: resp.StatusCode,
			Message:    string(respBody),
			RequestURL: reqURL,
		}
	}

	var result changedRegionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("nutanix client: GetChangedRegions: failed to decode response: %w", err)
	}

	return &result.Data, nil
}
