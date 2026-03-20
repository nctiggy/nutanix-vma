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
	"strconv"

	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

const cbtPageSize = 3

// handleCBTDiscoverCluster handles POST /api/storage/v4.0/config/changed-regions/$actions/discover-cluster.
// Returns the mock PE URL and a JWT token for subsequent CBT queries.
func (s *Server) handleCBTDiscoverCluster(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token := s.Store.CBTConfig.JWTToken
	if token == "" {
		token = "mock-jwt-token"
	}

	resp := map[string]any{
		"data": nutanix.CBTClusterInfo{
			PrismElementURL: s.URL(),
			JWTToken:        token,
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleCBTChangedRegions handles GET /api/storage/v4.0/config/changed-regions.
// Returns changed regions with pagination via nextOffset.
// Validates the JWT token is present in the NTNX_IGW_SESSION cookie.
func (s *Server) handleCBTChangedRegions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Validate JWT cookie
	cookie, err := r.Cookie(nutanix.CBTJWTCookieName)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "missing JWT token in cookie")
		return
	}

	expectedToken := s.Store.CBTConfig.JWTToken
	if expectedToken == "" {
		expectedToken = "mock-jwt-token"
	}
	if cookie.Value != expectedToken {
		writeError(w, http.StatusUnauthorized, "invalid JWT token")
		return
	}

	// Parse offset query param for pagination
	offsetParam := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if parsed, parseErr := strconv.Atoi(v); parseErr == nil && parsed >= 0 {
			offsetParam = parsed
		}
	}

	regions := s.Store.CBTConfig.ChangedRegions
	total := len(regions)

	// Paginate
	start := offsetParam
	if start >= total {
		// No more regions
		writeJSON(w, http.StatusOK, map[string]any{
			"data": nutanix.ChangedRegions{
				Regions:    []nutanix.ChangedRegion{},
				NextOffset: nil,
			},
		})
		return
	}

	end := min(start+cbtPageSize, total)
	page := regions[start:end]

	var nextOffset *int64
	if end < total {
		v := int64(end)
		nextOffset = &v
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data": nutanix.ChangedRegions{
			Regions:    page,
			NextOffset: nextOffset,
		},
	})
}
