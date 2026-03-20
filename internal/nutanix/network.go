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
	subnetBasePath = "/api/networking/v4.0/config/subnets"
)

// subnetListResponse is the typed v4 API list response for subnets.
type subnetListResponse struct {
	Data     []Subnet      `json:"data"`
	Metadata *ListMetadata `json:"metadata"`
}

// ListSubnets returns all subnets from Prism Central, handling pagination.
func (c *httpClient) ListSubnets(ctx context.Context) ([]Subnet, error) {
	var allSubnets []Subnet
	offset := 0

	for {
		url := fmt.Sprintf("%s%s?$top=%d&$skip=%d", c.host, subnetBasePath, pageSize, offset)

		var resp subnetListResponse
		if err := c.doJSON(ctx, "GET", url, nil, &resp); err != nil {
			return nil, fmt.Errorf("nutanix client: ListSubnets: %w", err)
		}

		allSubnets = append(allSubnets, resp.Data...)

		if resp.Metadata == nil || len(allSubnets) >= resp.Metadata.TotalAvailableResults {
			break
		}
		offset += pageSize
	}

	return allSubnets, nil
}
