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
	"strings"
)

const (
	storageContainerV2Path = "/PrismGateway/services/rest/v2.0/storage_containers"
)

// storageContainerV2Response is the v2 API response for storage containers.
type storageContainerV2Response struct {
	Entities []StorageContainer `json:"entities"`
	Metadata *v2Metadata        `json:"metadata"`
}

// v2Metadata holds pagination info for Nutanix v2 API responses.
type v2Metadata struct {
	GrandTotalEntities int `json:"grandTotalEntities"`
	TotalEntities      int `json:"totalEntities"`
}

// ListStorageContainers returns all storage containers from a Prism Element node.
// The peURL parameter is the base URL of the Prism Element (e.g. https://pe-ip:9440).
func (c *httpClient) ListStorageContainers(ctx context.Context, peURL string) ([]StorageContainer, error) {
	peURL = strings.TrimRight(peURL, "/")
	url := fmt.Sprintf("%s%s", peURL, storageContainerV2Path)

	var resp storageContainerV2Response
	if err := c.doJSON(ctx, "GET", url, nil, &resp); err != nil {
		return nil, fmt.Errorf("nutanix client: ListStorageContainers: %w", err)
	}

	return resp.Entities, nil
}
