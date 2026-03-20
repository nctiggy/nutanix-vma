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
	"fmt"
)

const (
	clusterBasePath = "/api/clustermgmt/v4.0/config/clusters"
)

// clusterListResponse is the typed v4 API list response for clusters.
type clusterListResponse struct {
	Data     []Cluster     `json:"data"`
	Metadata *ListMetadata `json:"metadata"`
}

// ListClusters returns all clusters from Prism Central, handling pagination.
// Cluster network info includes PE external addresses for auto-discovery.
func (c *httpClient) ListClusters(ctx context.Context) ([]Cluster, error) {
	var allClusters []Cluster
	offset := 0

	for {
		url := fmt.Sprintf("%s%s?$top=%d&$skip=%d", c.host, clusterBasePath, pageSize, offset)

		var resp clusterListResponse
		if err := c.doJSON(ctx, "GET", url, nil, &resp); err != nil {
			return nil, fmt.Errorf("nutanix client: ListClusters: %w", err)
		}

		allClusters = append(allClusters, resp.Data...)

		if resp.Metadata == nil || len(allClusters) >= resp.Metadata.TotalAvailableResults {
			break
		}
		offset += pageSize
	}

	return allClusters, nil
}
