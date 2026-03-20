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
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

const defaultImageSize int64 = 4096

// handleImageCreate handles POST /api/vmm/v4.0/images.
// Creates an image and returns a task reference.
func (s *Server) handleImageCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	imgUUID := s.Store.AddImage(&nutanix.Image{
		Name:      body.Name,
		Type:      body.Type,
		SizeBytes: defaultImageSize,
	})

	taskUUID := s.Store.CreateTask([]nutanix.TaskEntity{
		{ExtID: imgUUID, Rel: "vmm:images:image"},
	}, 2)

	writeJSON(w, http.StatusAccepted, taskRefBody(taskUUID))
}

// handleImageByID routes requests for individual images:
//   - GET    .../images/{uuid}      - get image metadata
//   - GET    .../images/{uuid}/file - download image data
//   - DELETE .../images/{uuid}      - delete image
func (s *Server) handleImageByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/vmm/v4.0/images/")
	parts := strings.SplitN(path, "/", 2)
	uuid := parts[0]

	if len(parts) > 1 && parts[1] == "file" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleImageDownload(w, r, uuid)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleImageGet(w, uuid)
	case http.MethodDelete:
		s.handleImageDelete(w, uuid)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleImageGet(w http.ResponseWriter, uuid string) {
	img := s.Store.GetImage(uuid)
	if img == nil {
		writeError(w, http.StatusNotFound, "image not found")
		return
	}
	writeJSON(w, http.StatusOK, singleResponse(img))
}

func (s *Server) handleImageDelete(w http.ResponseWriter, uuid string) {
	if !s.Store.DeleteImage(uuid) {
		writeError(w, http.StatusNotFound, "image not found")
		return
	}

	taskUUID := s.Store.CreateTask([]nutanix.TaskEntity{
		{ExtID: uuid, Rel: "vmm:images:image"},
	}, 2)

	writeJSON(w, http.StatusAccepted, taskRefBody(taskUUID))
}

// handleImageDownload serves synthetic image data with HTTP Range header support.
// Data is a repeating 0xAA byte pattern of the image's configured size.
func (s *Server) handleImageDownload(w http.ResponseWriter, r *http.Request, uuid string) {
	img := s.Store.GetImage(uuid)
	if img == nil {
		writeError(w, http.StatusNotFound, "image not found")
		return
	}

	totalSize := img.SizeBytes
	if totalSize <= 0 {
		totalSize = defaultImageSize
	}

	// Generate synthetic data (repeating 0xAA pattern)
	data := make([]byte, totalSize)
	for i := range data {
		data[i] = 0xAA
	}

	// Check for Range header
	rangeHeader := r.Header.Get("Range")
	if rangeHeader == "" {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(totalSize, 10))
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}

	// Parse Range header: "bytes=start-end"
	start, end, ok := parseRangeHeader(rangeHeader, totalSize)
	if !ok {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", totalSize))
		writeError(w, http.StatusRequestedRangeNotSatisfiable, "invalid range")
		return
	}

	rangeLen := end - start + 1
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(rangeLen, 10))
	w.Header().Set("Content-Range",
		fmt.Sprintf("bytes %d-%d/%d", start, end, totalSize))
	w.Header().Set("Accept-Ranges", "bytes")
	w.WriteHeader(http.StatusPartialContent)
	_, _ = bytes.NewReader(data[start : end+1]).WriteTo(w)
}

// parseRangeHeader parses a "bytes=start-end" Range header.
// Returns start, end (inclusive), and whether the range is valid.
func parseRangeHeader(header string, totalSize int64) (int64, int64, bool) {
	if !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false
	}

	rangeSpec := strings.TrimPrefix(header, "bytes=")
	parts := strings.SplitN(rangeSpec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}

	var start, end int64

	if parts[0] == "" {
		// Suffix range: -N means last N bytes
		n, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		start = max(totalSize-n, 0)
		end = totalSize - 1
	} else {
		var err error
		start, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil || start < 0 || start >= totalSize {
			return 0, 0, false
		}
		if parts[1] == "" {
			end = totalSize - 1
		} else {
			end, err = strconv.ParseInt(parts[1], 10, 64)
			if err != nil || end < start {
				return 0, 0, false
			}
			if end >= totalSize {
				end = totalSize - 1
			}
		}
	}

	return start, end, true
}
