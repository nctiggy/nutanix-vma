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
	"context"
	"testing"

	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

const (
	testVMUUID1   = "vm-uuid-001"
	testVMUUID2   = "vm-uuid-002"
	testVMUUID3   = "vm-uuid-003"
	powerStateOFF = "OFF"
	powerStateON  = "ON"
)

// newTestClient creates a mock server with fixtures and returns
// a NutanixClient connected to it, plus the server (for Close).
func newTestClient(t *testing.T) (nutanix.NutanixClient, *Server) {
	t.Helper()
	srv := NewServer(WithFixtures())
	client, err := nutanix.NewClient(nutanix.ClientConfig{
		Host:     srv.URL(),
		Username: "admin",
		Password: "password",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	return client, srv
}

// --- VM Tests ---

func TestListVMs_WithFixtures(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	vms, err := client.ListVMs(context.Background())
	if err != nil {
		t.Fatalf("ListVMs failed: %v", err)
	}

	if len(vms) != 3 {
		t.Fatalf("expected 3 VMs, got %d", len(vms))
	}

	// Verify first VM details
	if vms[0].ExtID != testVMUUID1 {
		t.Errorf("expected VM ExtID %s, got %s", testVMUUID1, vms[0].ExtID)
	}
	if vms[0].Name != "test-vm-linux" {
		t.Errorf("expected VM name test-vm-linux, got %s", vms[0].Name)
	}
	if vms[0].NumSockets != 2 {
		t.Errorf("expected 2 sockets, got %d", vms[0].NumSockets)
	}
	if vms[0].MemorySizeBytes != 8589934592 {
		t.Errorf("expected 8589934592 memory bytes, got %d", vms[0].MemorySizeBytes)
	}
}

func TestListVMs_Empty(t *testing.T) {
	srv := NewServer() // no fixtures
	defer srv.Close()

	client, err := nutanix.NewClient(nutanix.ClientConfig{
		Host:     srv.URL(),
		Username: "admin",
		Password: "password",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	vms, err := client.ListVMs(context.Background())
	if err != nil {
		t.Fatalf("ListVMs failed: %v", err)
	}
	if len(vms) != 0 {
		t.Fatalf("expected 0 VMs, got %d", len(vms))
	}
}

func TestGetVM_Found(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	vm, err := client.GetVM(context.Background(), testVMUUID1)
	if err != nil {
		t.Fatalf("GetVM failed: %v", err)
	}

	if vm.ExtID != testVMUUID1 {
		t.Errorf("expected ExtID %s, got %s", testVMUUID1, vm.ExtID)
	}
	if vm.PowerState != powerStateON {
		t.Errorf("expected power state ON, got %s", vm.PowerState)
	}
	if len(vm.Disks) != 1 {
		t.Errorf("expected 1 disk, got %d", len(vm.Disks))
	}
	if len(vm.Nics) != 1 {
		t.Errorf("expected 1 NIC, got %d", len(vm.Nics))
	}
	if vm.Cluster == nil || vm.Cluster.ExtID != "cluster-uuid-001" {
		t.Errorf("expected cluster reference cluster-uuid-001")
	}
}

func TestGetVM_NotFound(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	_, err := client.GetVM(context.Background(), "nonexistent-uuid")
	if err == nil {
		t.Fatal("expected error for nonexistent VM")
	}
}

func TestGetVM_WindowsWithCDROM(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	vm, err := client.GetVM(context.Background(), testVMUUID2)
	if err != nil {
		t.Fatalf("GetVM failed: %v", err)
	}

	if len(vm.Disks) != 2 {
		t.Fatalf("expected 2 disks (1 DISK + 1 CDROM), got %d", len(vm.Disks))
	}
	if vm.Disks[1].DeviceType != "CDROM" {
		t.Errorf("expected second disk to be CDROM, got %s", vm.Disks[1].DeviceType)
	}
	if vm.BootConfig == nil || vm.BootConfig.BootType != "UEFI" {
		t.Error("expected UEFI boot type for Windows VM")
	}
}

func TestGetVM_WithGPU(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	vm, err := client.GetVM(context.Background(), testVMUUID3)
	if err != nil {
		t.Fatalf("GetVM failed: %v", err)
	}

	if len(vm.Gpus) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(vm.Gpus))
	}
	if vm.Gpus[0].Mode != "PASSTHROUGH_GRAPHICS" {
		t.Errorf("expected PASSTHROUGH_GRAPHICS, got %s", vm.Gpus[0].Mode)
	}
	if vm.Gpus[0].Vendor != "NVIDIA" {
		t.Errorf("expected NVIDIA vendor, got %s", vm.Gpus[0].Vendor)
	}
}

func TestPowerOffVM(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	// VM starts ON
	vm, err := client.GetVM(context.Background(), testVMUUID1)
	if err != nil {
		t.Fatalf("GetVM failed: %v", err)
	}
	if vm.PowerState != powerStateON {
		t.Fatalf("expected initial power state ON, got %s", vm.PowerState)
	}

	// Power off
	err = client.PowerOffVM(context.Background(), testVMUUID1)
	if err != nil {
		t.Fatalf("PowerOffVM failed: %v", err)
	}

	// Verify power state changed
	vm, err = client.GetVM(context.Background(), testVMUUID1)
	if err != nil {
		t.Fatalf("GetVM after PowerOff failed: %v", err)
	}
	if vm.PowerState != powerStateOFF {
		t.Errorf("expected power state OFF after PowerOff, got %s", vm.PowerState)
	}
}

func TestPowerOnVM(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	// VM3 starts OFF
	err := client.PowerOnVM(context.Background(), testVMUUID3)
	if err != nil {
		t.Fatalf("PowerOnVM failed: %v", err)
	}

	vm, err := client.GetVM(context.Background(), testVMUUID3)
	if err != nil {
		t.Fatalf("GetVM after PowerOn failed: %v", err)
	}
	if vm.PowerState != powerStateON {
		t.Errorf("expected power state ON after PowerOn, got %s", vm.PowerState)
	}
}

func TestPowerOffVM_NotFound(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	err := client.PowerOffVM(context.Background(), "nonexistent-uuid")
	if err == nil {
		t.Fatal("expected error for nonexistent VM power off")
	}
}

func TestDeleteVM(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	err := client.DeleteVM(context.Background(), testVMUUID1)
	if err != nil {
		t.Fatalf("DeleteVM failed: %v", err)
	}

	// Verify VM is gone
	_, err = client.GetVM(context.Background(), testVMUUID1)
	if err == nil {
		t.Fatal("expected error after deleting VM")
	}

	// Other VMs still present
	vms, err := client.ListVMs(context.Background())
	if err != nil {
		t.Fatalf("ListVMs after delete failed: %v", err)
	}
	if len(vms) != 2 {
		t.Errorf("expected 2 VMs after delete, got %d", len(vms))
	}
}

// --- Subnet Tests ---

func TestListSubnets(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	subnets, err := client.ListSubnets(context.Background())
	if err != nil {
		t.Fatalf("ListSubnets failed: %v", err)
	}

	if len(subnets) != 2 {
		t.Fatalf("expected 2 subnets, got %d", len(subnets))
	}

	if subnets[0].ExtID != "subnet-uuid-001" {
		t.Errorf("expected subnet ExtID subnet-uuid-001, got %s", subnets[0].ExtID)
	}
	if subnets[0].Name != "vm-network" {
		t.Errorf("expected subnet name vm-network, got %s", subnets[0].Name)
	}
	if subnets[0].VlanID != 100 {
		t.Errorf("expected VLAN ID 100, got %d", subnets[0].VlanID)
	}
}

func TestListSubnets_Empty(t *testing.T) {
	srv := NewServer()
	defer srv.Close()

	client, err := nutanix.NewClient(nutanix.ClientConfig{
		Host:     srv.URL(),
		Username: "admin",
		Password: "password",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	subnets, err := client.ListSubnets(context.Background())
	if err != nil {
		t.Fatalf("ListSubnets failed: %v", err)
	}
	if len(subnets) != 0 {
		t.Fatalf("expected 0 subnets, got %d", len(subnets))
	}
}

// --- Storage Container Tests ---

func TestListStorageContainers(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	// Storage containers use PE URL, which in mock is the same server URL
	containers, err := client.ListStorageContainers(context.Background(), srv.URL())
	if err != nil {
		t.Fatalf("ListStorageContainers failed: %v", err)
	}

	if len(containers) != 1 {
		t.Fatalf("expected 1 storage container, got %d", len(containers))
	}

	if containers[0].UUID != "sc-uuid-001" {
		t.Errorf("expected container UUID sc-uuid-001, got %s", containers[0].UUID)
	}
	if containers[0].Name != "default-container" {
		t.Errorf("expected container name default-container, got %s", containers[0].Name)
	}
}

func TestListStorageContainers_TrailingSlash(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	// PE URL with trailing slash should still work
	containers, err := client.ListStorageContainers(context.Background(), srv.URL()+"/")
	if err != nil {
		t.Fatalf("ListStorageContainers with trailing slash failed: %v", err)
	}

	if len(containers) != 1 {
		t.Fatalf("expected 1 storage container, got %d", len(containers))
	}
}

// --- Cluster Tests ---

func TestListClusters(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	clusters, err := client.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("ListClusters failed: %v", err)
	}

	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}

	if clusters[0].ExtID != "cluster-uuid-001" {
		t.Errorf("expected cluster ExtID cluster-uuid-001, got %s", clusters[0].ExtID)
	}
	if clusters[0].Name != "test-cluster" {
		t.Errorf("expected cluster name test-cluster, got %s", clusters[0].Name)
	}
	if clusters[0].Network == nil {
		t.Fatal("expected cluster network info")
	}
	if clusters[0].Network.ExternalAddress != "10.0.0.10" {
		t.Errorf("expected PE address 10.0.0.10, got %s", clusters[0].Network.ExternalAddress)
	}
}

func TestListClusters_Empty(t *testing.T) {
	srv := NewServer()
	defer srv.Close()

	client, err := nutanix.NewClient(nutanix.ClientConfig{
		Host:     srv.URL(),
		Username: "admin",
		Password: "password",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	clusters, err := client.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("ListClusters failed: %v", err)
	}
	if len(clusters) != 0 {
		t.Fatalf("expected 0 clusters, got %d", len(clusters))
	}
}

// --- Task Tests ---

func TestTaskPolling_ViaHTTP(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	// PowerOffVM internally creates a task and polls it to completion.
	// This tests the full task lifecycle through the HTTP API.
	err := client.PowerOffVM(context.Background(), testVMUUID1)
	if err != nil {
		t.Fatalf("PowerOffVM (task polling) failed: %v", err)
	}

	// Verify the task was consumed from the store
	vm, err := client.GetVM(context.Background(), testVMUUID1)
	if err != nil {
		t.Fatalf("GetVM after task-based power off failed: %v", err)
	}
	if vm.PowerState != powerStateOFF {
		t.Errorf("expected OFF after task-based power off, got %s", vm.PowerState)
	}
}

// --- Concurrent Access Test ---

func TestConcurrentAccess(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	// Run multiple concurrent operations
	done := make(chan error, 4)

	go func() {
		_, err := client.ListVMs(context.Background())
		done <- err
	}()
	go func() {
		_, err := client.ListSubnets(context.Background())
		done <- err
	}()
	go func() {
		_, err := client.ListClusters(context.Background())
		done <- err
	}()
	go func() {
		_, err := client.ListStorageContainers(context.Background(), srv.URL())
		done <- err
	}()

	for range 4 {
		if err := <-done; err != nil {
			t.Errorf("concurrent operation failed: %v", err)
		}
	}
}

// --- WithFixtures Test ---

func TestWithFixtures_LoadsDefaultData(t *testing.T) {
	srv := NewServer(WithFixtures())
	defer srv.Close()

	if len(srv.Store.GetVMs()) != 3 {
		t.Errorf("expected 3 fixture VMs, got %d", len(srv.Store.GetVMs()))
	}
	if len(srv.Store.GetSubnets()) != 2 {
		t.Errorf("expected 2 fixture subnets, got %d", len(srv.Store.GetSubnets()))
	}
	if len(srv.Store.GetStorageContainers()) != 1 {
		t.Errorf("expected 1 fixture storage container, got %d", len(srv.Store.GetStorageContainers()))
	}
	if len(srv.Store.GetClusters()) != 1 {
		t.Errorf("expected 1 fixture cluster, got %d", len(srv.Store.GetClusters()))
	}
}

func TestNoFixtures_EmptyState(t *testing.T) {
	srv := NewServer()
	defer srv.Close()

	if len(srv.Store.GetVMs()) != 0 {
		t.Errorf("expected 0 VMs without fixtures, got %d", len(srv.Store.GetVMs()))
	}
	if len(srv.Store.GetSubnets()) != 0 {
		t.Errorf("expected 0 subnets without fixtures, got %d", len(srv.Store.GetSubnets()))
	}
	if len(srv.Store.GetStorageContainers()) != 0 {
		t.Errorf("expected 0 storage containers without fixtures, got %d", len(srv.Store.GetStorageContainers()))
	}
	if len(srv.Store.GetClusters()) != 0 {
		t.Errorf("expected 0 clusters without fixtures, got %d", len(srv.Store.GetClusters()))
	}
}

// --- VM List Pagination Test ---

func TestListVMs_Pagination(t *testing.T) {
	srv := NewServer()
	defer srv.Close()

	// Add more VMs than page size to test pagination
	for i := range 3 {
		srv.Store.VMs = append(srv.Store.VMs, nutanix.VM{
			ExtID:      taskUUIDFromCounter(int64(i + 1)),
			Name:       "paginated-vm",
			PowerState: "ON",
		})
	}

	client, err := nutanix.NewClient(nutanix.ClientConfig{
		Host:     srv.URL(),
		Username: "admin",
		Password: "password",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	vms, err := client.ListVMs(context.Background())
	if err != nil {
		t.Fatalf("ListVMs failed: %v", err)
	}
	if len(vms) != 3 {
		t.Errorf("expected 3 VMs, got %d", len(vms))
	}
}

// --- Store Unit Tests ---

func TestStore_SetVMPowerState(t *testing.T) {
	store := NewStore()
	store.VMs = []nutanix.VM{
		{ExtID: "vm-1", PowerState: "ON"},
	}

	ok := store.SetVMPowerState("vm-1", "OFF")
	if !ok {
		t.Error("expected SetVMPowerState to return true")
	}

	vm := store.GetVM("vm-1")
	if vm.PowerState != powerStateOFF {
		t.Errorf("expected OFF, got %s", vm.PowerState)
	}

	ok = store.SetVMPowerState("nonexistent", "OFF")
	if ok {
		t.Error("expected SetVMPowerState to return false for nonexistent VM")
	}
}

func TestStore_TaskLifecycle(t *testing.T) {
	store := NewStore()

	// Create task that succeeds after 2 polls
	taskUUID := store.CreateTask([]nutanix.TaskEntity{
		{ExtID: "entity-1", Rel: "test"},
	}, 2)

	// Poll 1: should be RUNNING
	task := store.GetTask(taskUUID)
	if task == nil {
		t.Fatal("expected task to exist")
	}
	if task.Status != nutanix.TaskStatusRunning {
		t.Errorf("expected RUNNING after 1 poll, got %s", task.Status)
	}

	// Poll 2: should be SUCCEEDED
	task = store.GetTask(taskUUID)
	if task.Status != nutanix.TaskStatusSucceeded {
		t.Errorf("expected SUCCEEDED after 2 polls, got %s", task.Status)
	}
	if task.PercentComplete != 100 {
		t.Errorf("expected 100%% complete, got %d", task.PercentComplete)
	}

	// Nonexistent task
	task = store.GetTask("nonexistent")
	if task != nil {
		t.Error("expected nil for nonexistent task")
	}
}
