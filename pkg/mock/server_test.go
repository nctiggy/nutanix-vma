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
	"context"
	"fmt"
	"io"
	"net/http"
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

// --- Recovery Point (Snapshot) Tests ---

func TestCreateRecoveryPoint(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	rpUUID, err := client.CreateRecoveryPoint(context.Background(), testVMUUID1, "test-snapshot")
	if err != nil {
		t.Fatalf("CreateRecoveryPoint failed: %v", err)
	}

	if rpUUID == "" {
		t.Fatal("expected non-empty recovery point UUID")
	}

	// Verify recovery point exists in store
	rp := srv.Store.GetRecoveryPoint(rpUUID)
	if rp == nil {
		t.Fatal("expected recovery point to exist in store")
	}
	if rp.Name != "test-snapshot" {
		t.Errorf("expected name test-snapshot, got %s", rp.Name)
	}
	if rp.VMExtID != testVMUUID1 {
		t.Errorf("expected VM ext ID %s, got %s", testVMUUID1, rp.VMExtID)
	}
	if rp.Status != recoveryPointStatusComplete {
		t.Errorf("expected status COMPLETE, got %s", rp.Status)
	}
}

func TestGetRecoveryPoint(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	// Create a RP first
	rpUUID, err := client.CreateRecoveryPoint(context.Background(), testVMUUID1, "get-test-rp")
	if err != nil {
		t.Fatalf("CreateRecoveryPoint failed: %v", err)
	}

	rp, err := client.GetRecoveryPoint(context.Background(), rpUUID)
	if err != nil {
		t.Fatalf("GetRecoveryPoint failed: %v", err)
	}

	if rp.ExtID != rpUUID {
		t.Errorf("expected ExtID %s, got %s", rpUUID, rp.ExtID)
	}
	if rp.Name != "get-test-rp" {
		t.Errorf("expected name get-test-rp, got %s", rp.Name)
	}
}

func TestGetRecoveryPoint_NotFound(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	_, err := client.GetRecoveryPoint(context.Background(), "nonexistent-rp")
	if err == nil {
		t.Fatal("expected error for nonexistent recovery point")
	}
}

func TestDeleteRecoveryPoint(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	rpUUID, err := client.CreateRecoveryPoint(context.Background(), testVMUUID1, "delete-test-rp")
	if err != nil {
		t.Fatalf("CreateRecoveryPoint failed: %v", err)
	}

	err = client.DeleteRecoveryPoint(context.Background(), rpUUID)
	if err != nil {
		t.Fatalf("DeleteRecoveryPoint failed: %v", err)
	}

	// Verify it's gone
	rp := srv.Store.GetRecoveryPoint(rpUUID)
	if rp != nil {
		t.Error("expected recovery point to be deleted from store")
	}
}

func TestDeleteRecoveryPoint_NotFound(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	err := client.DeleteRecoveryPoint(context.Background(), "nonexistent-rp")
	if err == nil {
		t.Fatal("expected error for deleting nonexistent recovery point")
	}
}

func TestCloneVMFromRecoveryPoint(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	rpUUID, err := client.CreateRecoveryPoint(context.Background(), testVMUUID1, "clone-test-rp")
	if err != nil {
		t.Fatalf("CreateRecoveryPoint failed: %v", err)
	}

	cloneUUID, err := client.CloneVMFromRecoveryPoint(context.Background(), rpUUID, "my-clone")
	if err != nil {
		t.Fatalf("CloneVMFromRecoveryPoint failed: %v", err)
	}

	if cloneUUID == "" {
		t.Fatal("expected non-empty cloned VM UUID")
	}

	// Verify cloned VM exists
	vm, err := client.GetVM(context.Background(), cloneUUID)
	if err != nil {
		t.Fatalf("GetVM for clone failed: %v", err)
	}
	if vm.Name != "my-clone" {
		t.Errorf("expected clone name my-clone, got %s", vm.Name)
	}
	if vm.PowerState != powerStateOFF {
		t.Errorf("expected clone power state OFF, got %s", vm.PowerState)
	}
}

func TestCloneVMFromRecoveryPoint_NotFound(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	_, err := client.CloneVMFromRecoveryPoint(context.Background(), "nonexistent-rp", "clone")
	if err == nil {
		t.Fatal("expected error for cloning from nonexistent recovery point")
	}
}

// --- Image Tests ---

func TestCreateImageFromDisk(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	imgUUID, err := client.CreateImageFromDisk(
		context.Background(),
		"test-image",
		"vdisk-001",
		"cluster-uuid-001",
	)
	if err != nil {
		t.Fatalf("CreateImageFromDisk failed: %v", err)
	}

	if imgUUID == "" {
		t.Fatal("expected non-empty image UUID")
	}

	// Verify image exists in store
	img := srv.Store.GetImage(imgUUID)
	if img == nil {
		t.Fatal("expected image to exist in store")
	}
	if img.Name != "test-image" {
		t.Errorf("expected name test-image, got %s", img.Name)
	}
}

func TestGetImage(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	imgUUID, err := client.CreateImageFromDisk(
		context.Background(),
		"get-test-img",
		"vdisk-001",
		"cluster-uuid-001",
	)
	if err != nil {
		t.Fatalf("CreateImageFromDisk failed: %v", err)
	}

	img, err := client.GetImage(context.Background(), imgUUID)
	if err != nil {
		t.Fatalf("GetImage failed: %v", err)
	}

	if img.ExtID != imgUUID {
		t.Errorf("expected ExtID %s, got %s", imgUUID, img.ExtID)
	}
	if img.Name != "get-test-img" {
		t.Errorf("expected name get-test-img, got %s", img.Name)
	}
}

func TestGetImage_NotFound(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	_, err := client.GetImage(context.Background(), "nonexistent-img")
	if err == nil {
		t.Fatal("expected error for nonexistent image")
	}
}

func TestDownloadImage(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	imgUUID, err := client.CreateImageFromDisk(
		context.Background(),
		"download-test-img",
		"vdisk-001",
		"cluster-uuid-001",
	)
	if err != nil {
		t.Fatalf("CreateImageFromDisk failed: %v", err)
	}

	var buf bytes.Buffer
	err = client.DownloadImage(context.Background(), imgUUID, &buf)
	if err != nil {
		t.Fatalf("DownloadImage failed: %v", err)
	}

	if int64(buf.Len()) != defaultImageSize {
		t.Errorf("expected %d bytes, got %d", defaultImageSize, buf.Len())
	}

	// Verify all bytes are 0xAA
	for i, b := range buf.Bytes() {
		if b != 0xAA {
			t.Errorf("expected byte 0xAA at offset %d, got 0x%02X", i, b)
			break
		}
	}
}

func TestDownloadImage_NotFound(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	var buf bytes.Buffer
	err := client.DownloadImage(context.Background(), "nonexistent-img", &buf)
	if err == nil {
		t.Fatal("expected error for downloading nonexistent image")
	}
}

func TestDownloadImage_RangeHeader(t *testing.T) {
	srv := NewServer(WithFixtures())
	defer srv.Close()

	// Add a known image to the store
	imgUUID := srv.Store.AddImage(&nutanix.Image{
		Name:      "range-test-img",
		Type:      "DISK_IMAGE",
		SizeBytes: 1024,
	})

	// Make a raw HTTP request with Range header
	url := fmt.Sprintf("%s/api/vmm/v4.0/images/%s/file", srv.URL(), imgUUID)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.SetBasicAuth("admin", "password")
	req.Header.Set("Range", "bytes=0-99")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("expected 206 Partial Content, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	if len(body) != 100 {
		t.Errorf("expected 100 bytes, got %d", len(body))
	}

	// Verify Content-Range header
	contentRange := resp.Header.Get("Content-Range")
	if contentRange != "bytes 0-99/1024" {
		t.Errorf("expected Content-Range 'bytes 0-99/1024', got '%s'", contentRange)
	}
}

func TestDownloadImage_SuffixRange(t *testing.T) {
	srv := NewServer(WithFixtures())
	defer srv.Close()

	imgUUID := srv.Store.AddImage(&nutanix.Image{
		Name:      "suffix-range-img",
		Type:      "DISK_IMAGE",
		SizeBytes: 1024,
	})

	url := fmt.Sprintf("%s/api/vmm/v4.0/images/%s/file", srv.URL(), imgUUID)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.SetBasicAuth("admin", "password")
	req.Header.Set("Range", "bytes=-256")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("expected 206 Partial Content, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	if len(body) != 256 {
		t.Errorf("expected 256 bytes for suffix range, got %d", len(body))
	}
}

func TestDeleteImage(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	imgUUID, err := client.CreateImageFromDisk(
		context.Background(),
		"delete-test-img",
		"vdisk-001",
		"cluster-uuid-001",
	)
	if err != nil {
		t.Fatalf("CreateImageFromDisk failed: %v", err)
	}

	err = client.DeleteImage(context.Background(), imgUUID)
	if err != nil {
		t.Fatalf("DeleteImage failed: %v", err)
	}

	// Verify it's gone
	img := srv.Store.GetImage(imgUUID)
	if img != nil {
		t.Error("expected image to be deleted from store")
	}
}

func TestDeleteImage_NotFound(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	err := client.DeleteImage(context.Background(), "nonexistent-img")
	if err == nil {
		t.Fatal("expected error for deleting nonexistent image")
	}
}

// --- CBT Tests ---

func newCBTTestClient(t *testing.T) (nutanix.NutanixClient, *Server) {
	t.Helper()
	srv := NewServer(WithFixtures(), WithCBTConfig(DefaultCBTConfig()))
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

func TestCBTDiscoverCluster(t *testing.T) {
	client, srv := newCBTTestClient(t)
	defer srv.Close()

	info, err := client.DiscoverClusterForCBT(context.Background(), testVMUUID1)
	if err != nil {
		t.Fatalf("DiscoverClusterForCBT failed: %v", err)
	}

	if info.PrismElementURL == "" {
		t.Error("expected non-empty PE URL")
	}
	if info.JWTToken != "mock-jwt-token-for-cbt" {
		t.Errorf("expected mock JWT token, got %s", info.JWTToken)
	}
	// PE URL should point to the same mock server
	if info.PrismElementURL != srv.URL() {
		t.Errorf("expected PE URL %s, got %s", srv.URL(), info.PrismElementURL)
	}
}

func TestCBTChangedRegions_SinglePage(t *testing.T) {
	client, srv := newCBTTestClient(t)
	defer srv.Close()

	// Discover first to cache the token
	info, err := client.DiscoverClusterForCBT(context.Background(), testVMUUID1)
	if err != nil {
		t.Fatalf("DiscoverClusterForCBT failed: %v", err)
	}

	regions, err := client.GetChangedRegions(
		context.Background(),
		info.PrismElementURL,
		info.JWTToken,
		testVMUUID1,
		"snap-uuid-1",
		"snap-uuid-0",
		0,
		1048576,
		65536,
	)
	if err != nil {
		t.Fatalf("GetChangedRegions failed: %v", err)
	}

	// Default CBT config has 3 regions, page size is 3, so all fit in one page
	if len(regions.Regions) != 3 {
		t.Fatalf("expected 3 regions, got %d", len(regions.Regions))
	}
	if regions.NextOffset != nil {
		t.Error("expected nil NextOffset for single page")
	}

	// Verify first region
	if regions.Regions[0].Offset != 0 || regions.Regions[0].Length != 65536 {
		t.Errorf("unexpected first region: offset=%d length=%d",
			regions.Regions[0].Offset, regions.Regions[0].Length)
	}
}

func TestCBTChangedRegions_Paginated(t *testing.T) {
	// Use more regions than cbtPageSize (3) to test pagination
	manyRegions := []nutanix.ChangedRegion{
		{Offset: 0, Length: 4096},
		{Offset: 8192, Length: 4096},
		{Offset: 16384, Length: 4096},
		{Offset: 24576, Length: 4096},
		{Offset: 32768, Length: 4096},
	}

	srv := NewServer(
		WithFixtures(),
		WithCBTConfig(CBTConfig{
			ChangedRegions: manyRegions,
			JWTToken:       "paginated-jwt",
		}),
	)
	defer srv.Close()

	client, err := nutanix.NewClient(nutanix.ClientConfig{
		Host:     srv.URL(),
		Username: "admin",
		Password: "password",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	info, err := client.DiscoverClusterForCBT(context.Background(), testVMUUID1)
	if err != nil {
		t.Fatalf("DiscoverClusterForCBT failed: %v", err)
	}

	// First page: offset=0, should return 3 regions with nextOffset
	regions, err := client.GetChangedRegions(
		context.Background(),
		info.PrismElementURL,
		info.JWTToken,
		testVMUUID1,
		"snap-1",
		"snap-0",
		0,
		65536,
		4096,
	)
	if err != nil {
		t.Fatalf("GetChangedRegions page 1 failed: %v", err)
	}
	if len(regions.Regions) != 3 {
		t.Fatalf("expected 3 regions in page 1, got %d", len(regions.Regions))
	}
	if regions.NextOffset == nil {
		t.Fatal("expected non-nil NextOffset for page 1")
	}
	if *regions.NextOffset != 3 {
		t.Errorf("expected NextOffset 3, got %d", *regions.NextOffset)
	}

	// Second page: offset=3, should return 2 regions with nil nextOffset
	regions, err = client.GetChangedRegions(
		context.Background(),
		info.PrismElementURL,
		info.JWTToken,
		testVMUUID1,
		"snap-1",
		"snap-0",
		*regions.NextOffset,
		65536,
		4096,
	)
	if err != nil {
		t.Fatalf("GetChangedRegions page 2 failed: %v", err)
	}
	if len(regions.Regions) != 2 {
		t.Fatalf("expected 2 regions in page 2, got %d", len(regions.Regions))
	}
	if regions.NextOffset != nil {
		t.Error("expected nil NextOffset for last page")
	}
}

func TestCBTChangedRegions_ZeroRegions(t *testing.T) {
	srv := NewServer(
		WithFixtures(),
		WithCBTConfig(CBTConfig{
			ChangedRegions: []nutanix.ChangedRegion{},
			JWTToken:       "zero-jwt",
		}),
	)
	defer srv.Close()

	client, err := nutanix.NewClient(nutanix.ClientConfig{
		Host:     srv.URL(),
		Username: "admin",
		Password: "password",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	info, err := client.DiscoverClusterForCBT(context.Background(), testVMUUID1)
	if err != nil {
		t.Fatalf("DiscoverClusterForCBT failed: %v", err)
	}

	regions, err := client.GetChangedRegions(
		context.Background(),
		info.PrismElementURL,
		info.JWTToken,
		testVMUUID1,
		"snap-1",
		"snap-0",
		0,
		65536,
		4096,
	)
	if err != nil {
		t.Fatalf("GetChangedRegions failed: %v", err)
	}

	if len(regions.Regions) != 0 {
		t.Errorf("expected 0 regions, got %d", len(regions.Regions))
	}
	if regions.NextOffset != nil {
		t.Error("expected nil NextOffset for zero regions")
	}
}

func TestCBTChangedRegions_InvalidJWT(t *testing.T) {
	srv := NewServer(
		WithFixtures(),
		WithCBTConfig(DefaultCBTConfig()),
	)
	defer srv.Close()

	// Make raw HTTP request with wrong JWT cookie
	url := fmt.Sprintf(
		"%s/api/storage/v4.0/config/changed-regions"+
			"?vmExtId=%s&snapshotExtId=s1&baseSnapshotExtId=s0"+
			"&offset=0&length=65536&blockSize=4096",
		srv.URL(), testVMUUID1)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: nutanix.CBTJWTCookieName, Value: "wrong-token"})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid JWT, got %d", resp.StatusCode)
	}
}

func TestCBTChangedRegions_MissingJWT(t *testing.T) {
	srv := NewServer(
		WithFixtures(),
		WithCBTConfig(DefaultCBTConfig()),
	)
	defer srv.Close()

	// Make raw HTTP request without JWT cookie
	url := fmt.Sprintf("%s/api/storage/v4.0/config/changed-regions?vmExtId=%s", srv.URL(), testVMUUID1)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing JWT, got %d", resp.StatusCode)
	}
}

// --- Full Snapshot -> Image -> Download Flow Test ---

func TestFullSnapshotImageFlow(t *testing.T) {
	client, srv := newTestClient(t)
	defer srv.Close()

	// 1. Create recovery point
	rpUUID, err := client.CreateRecoveryPoint(context.Background(), testVMUUID1, "full-flow-rp")
	if err != nil {
		t.Fatalf("step 1 CreateRecoveryPoint failed: %v", err)
	}

	// 2. Get recovery point
	rp, err := client.GetRecoveryPoint(context.Background(), rpUUID)
	if err != nil {
		t.Fatalf("step 2 GetRecoveryPoint failed: %v", err)
	}
	if rp.Status != recoveryPointStatusComplete {
		t.Errorf("expected RP status COMPLETE, got %s", rp.Status)
	}

	// 3. Create image from disk
	imgUUID, err := client.CreateImageFromDisk(
		context.Background(),
		"full-flow-img",
		"vdisk-001",
		"cluster-uuid-001",
	)
	if err != nil {
		t.Fatalf("step 3 CreateImageFromDisk failed: %v", err)
	}

	// 4. Download image
	var buf bytes.Buffer
	err = client.DownloadImage(context.Background(), imgUUID, &buf)
	if err != nil {
		t.Fatalf("step 4 DownloadImage failed: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("expected non-empty image download")
	}

	// 5. Delete image
	err = client.DeleteImage(context.Background(), imgUUID)
	if err != nil {
		t.Fatalf("step 5 DeleteImage failed: %v", err)
	}

	// 6. Delete recovery point
	err = client.DeleteRecoveryPoint(context.Background(), rpUUID)
	if err != nil {
		t.Fatalf("step 6 DeleteRecoveryPoint failed: %v", err)
	}

	// Verify cleanup
	if srv.Store.GetRecoveryPoint(rpUUID) != nil {
		t.Error("recovery point should be deleted")
	}
	if srv.Store.GetImage(imgUUID) != nil {
		t.Error("image should be deleted")
	}
}

// --- Store Recovery Point / Image Unit Tests ---

func TestStore_RecoveryPointLifecycle(t *testing.T) {
	store := NewStore()

	rpUUID := store.AddRecoveryPoint(&nutanix.RecoveryPoint{
		Name:    "store-test-rp",
		VMExtID: "vm-1",
	})

	rp := store.GetRecoveryPoint(rpUUID)
	if rp == nil {
		t.Fatal("expected recovery point to exist")
	}
	if rp.Name != "store-test-rp" {
		t.Errorf("expected name store-test-rp, got %s", rp.Name)
	}

	ok := store.DeleteRecoveryPoint(rpUUID)
	if !ok {
		t.Error("expected delete to return true")
	}

	rp = store.GetRecoveryPoint(rpUUID)
	if rp != nil {
		t.Error("expected recovery point to be nil after delete")
	}

	ok = store.DeleteRecoveryPoint("nonexistent")
	if ok {
		t.Error("expected delete to return false for nonexistent")
	}
}

func TestStore_ImageLifecycle(t *testing.T) {
	store := NewStore()

	imgUUID := store.AddImage(&nutanix.Image{
		Name:      "store-test-img",
		Type:      "DISK_IMAGE",
		SizeBytes: 1024,
	})

	img := store.GetImage(imgUUID)
	if img == nil {
		t.Fatal("expected image to exist")
	}
	if img.Name != "store-test-img" {
		t.Errorf("expected name store-test-img, got %s", img.Name)
	}
	if img.SizeBytes != 1024 {
		t.Errorf("expected size 1024, got %d", img.SizeBytes)
	}

	ok := store.DeleteImage(imgUUID)
	if !ok {
		t.Error("expected delete to return true")
	}

	img = store.GetImage(imgUUID)
	if img != nil {
		t.Error("expected image to be nil after delete")
	}

	ok = store.DeleteImage("nonexistent")
	if ok {
		t.Error("expected delete to return false for nonexistent")
	}
}

// --- Standalone Binary Build Test ---

func TestHandler_ReturnsNonNil(t *testing.T) {
	srv := NewServer(WithFixtures())
	defer srv.Close()

	if srv.Handler() == nil {
		t.Error("expected non-nil Handler()")
	}
}
