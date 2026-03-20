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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestListVMs_SinglePage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/api/vmm/v4.0/ahv/config/vms") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		resp := vmListResponse{
			Data: []VM{
				{ExtID: "vm-1", Name: "test-vm-1", PowerState: "ON", NumSockets: 2, NumCoresPerSocket: 4, MemorySizeBytes: 4294967296},
				{ExtID: "vm-2", Name: "test-vm-2", PowerState: "OFF", NumSockets: 1, NumCoresPerSocket: 2, MemorySizeBytes: 2147483648},
			},
			Metadata: &ListMetadata{TotalAvailableResults: 2, Offset: 0},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	vms, err := client.ListVMs(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs, got %d", len(vms))
	}
	if vms[0].ExtID != "vm-1" {
		t.Fatalf("expected vm-1, got %s", vms[0].ExtID)
	}
	if vms[0].NumSockets != 2 {
		t.Fatalf("expected 2 sockets, got %d", vms[0].NumSockets)
	}
}

func TestListVMs_Paginated(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")

		if count == 1 {
			// First page
			vms := make([]VM, pageSize)
			for i := range pageSize {
				vms[i] = VM{ExtID: "vm-" + strings.Repeat("a", i%10+1), Name: "vm"}
			}
			resp := vmListResponse{
				Data:     vms,
				Metadata: &ListMetadata{TotalAvailableResults: pageSize + 3, Offset: 0},
			}
			_ = json.NewEncoder(w).Encode(resp)
		} else {
			// Second page (last 3)
			resp := vmListResponse{
				Data: []VM{
					{ExtID: "vm-last-1"},
					{ExtID: "vm-last-2"},
					{ExtID: "vm-last-3"},
				},
				Metadata: &ListMetadata{TotalAvailableResults: pageSize + 3, Offset: pageSize},
			}
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	vms, err := client.ListVMs(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vms) != pageSize+3 {
		t.Fatalf("expected %d VMs, got %d", pageSize+3, len(vms))
	}
	if callCount.Load() != 2 {
		t.Fatalf("expected 2 API calls, got %d", callCount.Load())
	}
}

func TestListVMs_EmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := vmListResponse{
			Data:     []VM{},
			Metadata: &ListMetadata{TotalAvailableResults: 0},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	vms, err := client.ListVMs(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vms) != 0 {
		t.Fatalf("expected 0 VMs, got %d", len(vms))
	}
}

func TestGetVM(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/vm-uuid-123") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		vm := VM{
			ExtID:             "vm-uuid-123",
			Name:              "test-vm",
			PowerState:        "ON",
			NumSockets:        2,
			NumCoresPerSocket: 4,
			NumThreadsPerCore: 1,
			MemorySizeBytes:   8589934592,
			MachineType:       "Q35",
			BootConfig:        &BootConfig{BootType: "UEFI"},
			Disks: []Disk{
				{
					ExtID:         "disk-1",
					DiskAddress:   &DiskAddress{BusType: "SCSI", Index: 0},
					BackingInfo:   &DiskBackingInfo{VMDiskUUID: "vdisk-1", DiskSizeBytes: 107374182400},
					DiskSizeBytes: 107374182400,
					DeviceType:    "DISK",
				},
			},
			Nics: []NIC{
				{ExtID: "nic-1", NetworkRef: &Reference{ExtID: "subnet-1"}, NicType: "NORMAL_NIC", MacAddress: "50:6b:8d:01:02:03"},
			},
			Cluster: &Reference{ExtID: "cluster-1", Name: "pe-cluster"},
		}
		resp := vmResponse{Data: vm}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	vm, err := client.GetVM(context.Background(), "vm-uuid-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vm.ExtID != "vm-uuid-123" {
		t.Fatalf("expected vm-uuid-123, got %s", vm.ExtID)
	}
	if vm.Name != "test-vm" {
		t.Fatalf("expected test-vm, got %s", vm.Name)
	}
	if vm.NumSockets != 2 || vm.NumCoresPerSocket != 4 {
		t.Fatalf("unexpected CPU topology: %d sockets, %d cores", vm.NumSockets, vm.NumCoresPerSocket)
	}
	if vm.MemorySizeBytes != 8589934592 {
		t.Fatalf("expected 8GB, got %d", vm.MemorySizeBytes)
	}
	if vm.BootConfig == nil || vm.BootConfig.BootType != "UEFI" {
		t.Fatal("expected UEFI boot config")
	}
	if len(vm.Disks) != 1 {
		t.Fatalf("expected 1 disk, got %d", len(vm.Disks))
	}
	if vm.Disks[0].DiskAddress.BusType != "SCSI" {
		t.Fatalf("expected SCSI bus, got %s", vm.Disks[0].DiskAddress.BusType)
	}
	if len(vm.Nics) != 1 {
		t.Fatalf("expected 1 NIC, got %d", len(vm.Nics))
	}
	if vm.Nics[0].MacAddress != "50:6b:8d:01:02:03" {
		t.Fatalf("unexpected MAC: %s", vm.Nics[0].MacAddress)
	}
	if vm.Cluster == nil || vm.Cluster.ExtID != "cluster-1" {
		t.Fatal("expected cluster reference")
	}
}

func TestGetVM_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error": "VM not found"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = client.GetVM(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing VM")
	}
}

func TestPowerOffVM(t *testing.T) {
	var phase atomic.Int32 // 0=power-off, 1+=task poll

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, "$actions/power-off") {
			phase.Store(1)
			resp := taskRefResponse{}
			resp.Data.ExtID = "task-poweroff-1"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			count := phase.Add(1)
			task := Task{ExtID: "task-poweroff-1", Status: TaskStatusRunning}
			if count >= 3 {
				task.Status = TaskStatusSucceeded
			}
			_ = json.NewEncoder(w).Encode(task)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hc := client.(*httpClient)
	err = hc.PowerOffVM(context.Background(), "vm-uuid-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPowerOnVM(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, "$actions/power-on") {
			resp := taskRefResponse{}
			resp.Data.ExtID = "task-poweron-1"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			task := Task{ExtID: "task-poweron-1", Status: TaskStatusSucceeded}
			_ = json.NewEncoder(w).Encode(task)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = client.PowerOnVM(context.Background(), "vm-uuid-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPowerOffVM_TaskFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, "$actions/power-off") {
			resp := taskRefResponse{}
			resp.Data.ExtID = "task-fail"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(r.URL.Path, "/tasks/") {
			task := Task{
				ExtID:         "task-fail",
				Status:        TaskStatusFailed,
				ErrorMessages: []TaskError{{Message: "VM is already powered off"}},
			}
			_ = json.NewEncoder(w).Encode(task)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = client.PowerOffVM(context.Background(), "vm-uuid-123")
	if err == nil {
		t.Fatal("expected error for failed power-off task")
	}
	if !strings.Contains(err.Error(), "already powered off") {
		t.Fatalf("expected error about 'already powered off', got: %v", err)
	}
}

func TestGetVMPowerState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := vmResponse{Data: VM{ExtID: "vm-1", PowerState: "ON"}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	state, err := client.GetVMPowerState(context.Background(), "vm-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != PowerStateOn {
		t.Fatalf("expected ON, got %s", state)
	}
}

func TestGetVMPowerState_Off(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := vmResponse{Data: VM{ExtID: "vm-1", PowerState: "OFF"}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	state, err := client.GetVMPowerState(context.Background(), "vm-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != PowerStateOff {
		t.Fatalf("expected OFF, got %s", state)
	}
}

func TestGetVM_FullSpec(t *testing.T) {
	// Tests a VM with multiple disks (including CDROM), multiple NICs, GPU, boot config
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vm := VM{
			ExtID:             "vm-full",
			Name:              "windows-server",
			PowerState:        "ON",
			NumSockets:        4,
			NumCoresPerSocket: 8,
			NumThreadsPerCore: 2,
			MemorySizeBytes:   17179869184, // 16 GiB
			MachineType:       "Q35",
			BootConfig:        &BootConfig{BootType: "SECURE_BOOT", BootOrder: []string{"DISK", "CDROM", "NETWORK"}},
			Disks: []Disk{
				{ExtID: "disk-os", DiskAddress: &DiskAddress{BusType: "SCSI", Index: 0}, DeviceType: "DISK", DiskSizeBytes: 107374182400},
				{ExtID: "disk-data", DiskAddress: &DiskAddress{BusType: "SCSI", Index: 1}, DeviceType: "DISK", DiskSizeBytes: 536870912000},
				{ExtID: "cdrom-1", DiskAddress: &DiskAddress{BusType: "IDE", Index: 0}, DeviceType: "CDROM"},
			},
			Nics: []NIC{
				{ExtID: "nic-mgmt", NetworkRef: &Reference{ExtID: "subnet-mgmt"}, NicType: "NORMAL_NIC", MacAddress: "50:6b:8d:aa:bb:cc"},
				{ExtID: "nic-data", NetworkRef: &Reference{ExtID: "subnet-data"}, NicType: "DIRECT_NIC", MacAddress: "50:6b:8d:dd:ee:ff"},
			},
			Gpus: []GPU{
				{Mode: "PASSTHROUGH_GRAPHICS", DeviceID: 7864, Vendor: "NVIDIA"},
			},
			Cluster: &Reference{ExtID: "cluster-1", Name: "pe-cluster"},
		}
		resp := vmResponse{Data: vm}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{Host: server.URL, Username: "admin", Password: "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	vm, err := client.GetVM(context.Background(), "vm-full")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// CPU topology
	if vm.NumSockets != 4 || vm.NumCoresPerSocket != 8 || vm.NumThreadsPerCore != 2 {
		t.Fatalf("unexpected CPU: %d/%d/%d", vm.NumSockets, vm.NumCoresPerSocket, vm.NumThreadsPerCore)
	}
	// Memory
	if vm.MemorySizeBytes != 17179869184 {
		t.Fatalf("expected 16GiB, got %d", vm.MemorySizeBytes)
	}
	// Boot config
	if vm.BootConfig.BootType != "SECURE_BOOT" {
		t.Fatalf("expected SECURE_BOOT, got %s", vm.BootConfig.BootType)
	}
	if len(vm.BootConfig.BootOrder) != 3 {
		t.Fatalf("expected 3 boot order entries, got %d", len(vm.BootConfig.BootOrder))
	}
	// Disks: 2 DISK + 1 CDROM
	if len(vm.Disks) != 3 {
		t.Fatalf("expected 3 disks, got %d", len(vm.Disks))
	}
	if vm.Disks[2].DeviceType != "CDROM" {
		t.Fatalf("expected CDROM, got %s", vm.Disks[2].DeviceType)
	}
	// NICs
	if len(vm.Nics) != 2 {
		t.Fatalf("expected 2 NICs, got %d", len(vm.Nics))
	}
	if vm.Nics[1].NicType != "DIRECT_NIC" {
		t.Fatalf("expected DIRECT_NIC, got %s", vm.Nics[1].NicType)
	}
	// GPU
	if len(vm.Gpus) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(vm.Gpus))
	}
	if vm.Gpus[0].Vendor != "NVIDIA" {
		t.Fatalf("expected NVIDIA, got %s", vm.Gpus[0].Vendor)
	}
}
