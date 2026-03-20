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
	"fmt"
	"net/http"
	"strings"

	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

// handleRecoveryPointCreate handles POST /api/dataprotection/v4.0/config/recovery-points.
// Creates a recovery point and returns a task reference.
func (s *Server) handleRecoveryPointCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		Name          string `json:"name"`
		VMRecoveryPts []struct {
			VMExtID string `json:"vmExtId"`
		} `json:"vmRecoveryPoints"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	vmUUID := ""
	if len(body.VMRecoveryPts) > 0 {
		vmUUID = body.VMRecoveryPts[0].VMExtID
	}

	rpUUID := s.Store.AddRecoveryPoint(&nutanix.RecoveryPoint{
		Name:              body.Name,
		VMExtID:           vmUUID,
		RecoveryPointType: "APPLICATION_CONSISTENT",
	})

	taskUUID := s.Store.CreateTask([]nutanix.TaskEntity{
		{ExtID: rpUUID, Rel: "dataprotection:config:recovery-point"},
	}, 2)

	writeJSON(w, http.StatusAccepted, taskRefBody(taskUUID))
}

// handleRecoveryPointByID handles requests for individual recovery points:
//   - GET  .../recovery-points/{uuid} - get recovery point
//   - DELETE .../recovery-points/{uuid} - delete recovery point
//   - POST .../recovery-points/{uuid}/$actions/vm-recovery - clone VM from RP
func (s *Server) handleRecoveryPointByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/dataprotection/v4.0/config/recovery-points/")
	parts := strings.SplitN(path, "/", 2)
	uuid := parts[0]

	// Check for action paths
	if len(parts) > 1 {
		action := parts[1]
		if action == "$actions/vm-recovery" && r.Method == http.MethodPost {
			s.handleCloneVMFromRP(w, r, uuid)
			return
		}
		writeError(w, http.StatusNotFound, "unknown action")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleRecoveryPointGet(w, uuid)
	case http.MethodDelete:
		s.handleRecoveryPointDelete(w, uuid)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleRecoveryPointGet(w http.ResponseWriter, uuid string) {
	rp := s.Store.GetRecoveryPoint(uuid)
	if rp == nil {
		writeError(w, http.StatusNotFound, "recovery point not found")
		return
	}
	writeJSON(w, http.StatusOK, singleResponse(rp))
}

func (s *Server) handleRecoveryPointDelete(w http.ResponseWriter, uuid string) {
	if !s.Store.DeleteRecoveryPoint(uuid) {
		writeError(w, http.StatusNotFound, "recovery point not found")
		return
	}

	taskUUID := s.Store.CreateTask([]nutanix.TaskEntity{
		{ExtID: uuid, Rel: "dataprotection:config:recovery-point"},
	}, 2)

	writeJSON(w, http.StatusAccepted, taskRefBody(taskUUID))
}

// handleCloneVMFromRP creates a cloned VM from a recovery point.
func (s *Server) handleCloneVMFromRP(w http.ResponseWriter, r *http.Request, rpUUID string) {
	rp := s.Store.GetRecoveryPoint(rpUUID)
	if rp == nil {
		writeError(w, http.StatusNotFound, "recovery point not found")
		return
	}

	var body struct {
		VMRecoveryPts []struct {
			VMName string `json:"vmName"`
		} `json:"vmRecoveryPoints"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	vmName := "cloned-vm"
	if len(body.VMRecoveryPts) > 0 && body.VMRecoveryPts[0].VMName != "" {
		vmName = body.VMRecoveryPts[0].VMName
	}

	// Create a cloned VM in the store
	cloneID := s.Store.imageCounter.Add(1)
	cloneUUID := fmt.Sprintf("clone-%08d", cloneID)

	s.Store.mu.Lock()
	s.Store.VMs = append(s.Store.VMs, nutanix.VM{
		ExtID:      cloneUUID,
		Name:       vmName,
		PowerState: "OFF",
	})
	s.Store.mu.Unlock()

	taskUUID := s.Store.CreateTask([]nutanix.TaskEntity{
		{ExtID: cloneUUID, Rel: "vmm:ahv:config:vm"},
	}, 2)

	writeJSON(w, http.StatusAccepted, taskRefBody(taskUUID))
}
