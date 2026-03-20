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

package mock

import (
	"net/http"
)

// handleStorageContainerList handles GET /PrismGateway/services/rest/v2.0/storage_containers.
// This uses the v2 API response format (entities + metadata).
func (s *Server) handleStorageContainerList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	containers := s.Store.GetStorageContainers()

	resp := map[string]any{
		"entities": containers,
		"metadata": map[string]any{
			"grandTotalEntities": len(containers),
			"totalEntities":      len(containers),
		},
	}

	writeJSON(w, http.StatusOK, resp)
}
