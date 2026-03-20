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

// handleClusterList handles GET /api/clustermgmt/v4.0/config/clusters.
func (s *Server) handleClusterList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	clusters := s.Store.GetClusters()
	top, skip := parsePagination(r)

	total := len(clusters)
	if skip >= total {
		writeJSON(w, http.StatusOK, paginatedResponse([]any{}, total))
		return
	}

	end := min(skip+top, total)

	writeJSON(w, http.StatusOK, paginatedResponse(clusters[skip:end], total))
}
