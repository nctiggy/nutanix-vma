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
	"strings"

	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

// handleVMList handles GET /api/vmm/v4.0/ahv/config/vms (list with pagination).
func (s *Server) handleVMList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	vms := s.Store.GetVMs()
	top, skip := parsePagination(r)

	// Apply pagination
	total := len(vms)
	if skip >= total {
		writeJSON(w, http.StatusOK, paginatedResponse([]nutanix.VM{}, total))
		return
	}

	end := min(skip+top, total)

	writeJSON(w, http.StatusOK, paginatedResponse(vms[skip:end], total))
}

// handleVMByID handles VM requests by UUID:
//   - GET /api/vmm/v4.0/ahv/config/vms/{uuid} - get VM
//   - POST /api/vmm/v4.0/ahv/config/vms/{uuid}/$actions/power-off - power off
//   - POST /api/vmm/v4.0/ahv/config/vms/{uuid}/$actions/power-on - power on
//   - DELETE /api/vmm/v4.0/ahv/config/vms/{uuid} - delete VM
func (s *Server) handleVMByID(w http.ResponseWriter, r *http.Request) {
	// Parse UUID from path: /api/vmm/v4.0/ahv/config/vms/{uuid}[/$actions/...]
	path := strings.TrimPrefix(r.URL.Path, "/api/vmm/v4.0/ahv/config/vms/")
	parts := strings.SplitN(path, "/", 2)
	uuid := parts[0]

	// Check for action paths
	if len(parts) > 1 {
		action := parts[1]
		switch {
		case action == "$actions/power-off" && r.Method == http.MethodPost:
			s.handleVMPowerOff(w, uuid)
			return
		case action == "$actions/power-on" && r.Method == http.MethodPost:
			s.handleVMPowerOn(w, uuid)
			return
		default:
			writeError(w, http.StatusNotFound, "unknown action")
			return
		}
	}

	switch r.Method {
	case http.MethodGet:
		s.handleVMGet(w, uuid)
	case http.MethodDelete:
		s.handleVMDelete(w, uuid)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleVMGet(w http.ResponseWriter, uuid string) {
	vm := s.Store.GetVM(uuid)
	if vm == nil {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}
	writeJSON(w, http.StatusOK, singleResponse(vm))
}

func (s *Server) handleVMPowerOff(w http.ResponseWriter, uuid string) {
	vm := s.Store.GetVM(uuid)
	if vm == nil {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}

	taskUUID := s.Store.CreateTask([]nutanix.TaskEntity{
		{ExtID: uuid, Rel: "vmm:ahv:config:vm"},
	}, 2)

	// Set power state to OFF after task creation (simulates async completion)
	s.Store.SetVMPowerState(uuid, "OFF")

	writeJSON(w, http.StatusAccepted, taskRefBody(taskUUID))
}

func (s *Server) handleVMPowerOn(w http.ResponseWriter, uuid string) {
	vm := s.Store.GetVM(uuid)
	if vm == nil {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}

	taskUUID := s.Store.CreateTask([]nutanix.TaskEntity{
		{ExtID: uuid, Rel: "vmm:ahv:config:vm"},
	}, 2)

	s.Store.SetVMPowerState(uuid, "ON")

	writeJSON(w, http.StatusAccepted, taskRefBody(taskUUID))
}

func (s *Server) handleVMDelete(w http.ResponseWriter, uuid string) {
	vm := s.Store.GetVM(uuid)
	if vm == nil {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}

	taskUUID := s.Store.CreateTask([]nutanix.TaskEntity{
		{ExtID: uuid, Rel: "vmm:ahv:config:vm"},
	}, 2)

	// Remove VM from store
	s.Store.mu.Lock()
	for i := range s.Store.VMs {
		if s.Store.VMs[i].ExtID == uuid {
			s.Store.VMs = append(s.Store.VMs[:i], s.Store.VMs[i+1:]...)
			break
		}
	}
	s.Store.mu.Unlock()

	writeJSON(w, http.StatusAccepted, taskRefBody(taskUUID))
}
