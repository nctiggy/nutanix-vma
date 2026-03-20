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
	"encoding/json"
	"net/http"
	"strconv"
)

const defaultPageSize = 500

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// parsePagination extracts $top and $skip query parameters.
func parsePagination(r *http.Request) (top, skip int) {
	top = defaultPageSize
	if v := r.URL.Query().Get("$top"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			top = parsed
		}
	}
	if v := r.URL.Query().Get("$skip"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			skip = parsed
		}
	}
	return top, skip
}

// paginatedResponse wraps data in a v4 API list response with pagination metadata.
func paginatedResponse(data any, totalAvailable int) map[string]any {
	return map[string]any{
		"data": data,
		"metadata": map[string]any{
			"totalAvailableResults": totalAvailable,
		},
	}
}

// singleResponse wraps data in a v4 API single-entity response.
func singleResponse(data any) map[string]any {
	return map[string]any{
		"data": data,
	}
}

// taskRefBody returns a task reference response body.
func taskRefBody(taskUUID string) map[string]any {
	return map[string]any{
		"data": map[string]any{
			"extId": taskUUID,
		},
	}
}
