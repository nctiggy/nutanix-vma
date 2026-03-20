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

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

func TestPrintVMInventory(t *testing.T) {
	vms := []nutanix.VM{
		{
			ExtID:             "uuid-001",
			Name:              "web-server-01",
			NumSockets:        2,
			NumCoresPerSocket: 4,
			MemorySizeBytes:   4294967296, // 4 GiB
			PowerState:        "ON",
			Disks: []nutanix.Disk{
				{DeviceType: "DISK"},
				{DeviceType: "DISK"},
				{DeviceType: "CDROM"},
			},
		},
		{
			ExtID:             "uuid-002",
			Name:              "db-server-01",
			NumSockets:        4,
			NumCoresPerSocket: 2,
			MemorySizeBytes:   8589934592, // 8 GiB
			PowerState:        "OFF",
			Disks: []nutanix.Disk{
				{DeviceType: "DISK"},
			},
		},
	}

	var buf bytes.Buffer
	printVMInventory(vms, &buf)
	output := buf.String()

	// Verify header
	if !strings.Contains(output, "UUID") {
		t.Error("missing UUID header")
	}
	if !strings.Contains(output, "MEMORY (MiB)") {
		t.Error("missing MEMORY header")
	}

	// Verify VM data
	if !strings.Contains(output, "uuid-001") {
		t.Error("missing uuid-001")
	}
	if !strings.Contains(output, "web-server-01") {
		t.Error("missing web-server-01")
	}
	// 2 sockets * 4 cores = 8 CPUs
	if !strings.Contains(output, "8") {
		t.Error("missing CPU count 8")
	}
	// 4 GiB = 4096 MiB
	if !strings.Contains(output, "4096") {
		t.Error("missing memory 4096")
	}
	// 2 data disks (CDROM excluded)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 3 { // header + 2 VMs
		t.Errorf("expected 3 lines, got %d", len(lines))
	}

	// Verify second VM
	if !strings.Contains(output, "uuid-002") {
		t.Error("missing uuid-002")
	}
	if !strings.Contains(output, "OFF") {
		t.Error("missing power state OFF")
	}
}

func TestPrintVMInventory_Empty(t *testing.T) {
	var buf bytes.Buffer
	printVMInventory(nil, &buf)
	output := buf.String()

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line (header only), got %d", len(lines))
	}
}

func TestCountDataDisks(t *testing.T) {
	tests := []struct {
		name  string
		disks []nutanix.Disk
		want  int
	}{
		{"no disks", nil, 0},
		{"one disk", []nutanix.Disk{{DeviceType: "DISK"}}, 1},
		{"disk and cdrom", []nutanix.Disk{{DeviceType: "DISK"}, {DeviceType: "CDROM"}}, 1},
		{"cdrom case insensitive", []nutanix.Disk{{DeviceType: "cdrom"}, {DeviceType: "DISK"}}, 1},
		{"multiple disks", []nutanix.Disk{{DeviceType: "DISK"}, {DeviceType: "DISK"}, {DeviceType: "DISK"}}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countDataDisks(tt.disks)
			if got != tt.want {
				t.Errorf("countDataDisks() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFormatConcerns(t *testing.T) {
	tests := []struct {
		name     string
		concerns []vmav1alpha1.Concern
		want     string
	}{
		{"no concerns", nil, "None"},
		{"single error", []vmav1alpha1.Concern{
			{Category: vmav1alpha1.ConcernCategoryError, Message: "unmapped storage"},
		}, "[Error] unmapped storage"},
		{"multiple concerns", []vmav1alpha1.Concern{
			{Category: vmav1alpha1.ConcernCategoryError, Message: "unmapped storage"},
			{Category: vmav1alpha1.ConcernCategoryWarning, Message: "GPU attached"},
		}, "[Error] unmapped storage; [Warning] GPU attached"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatConcerns(tt.concerns)
			if got != tt.want {
				t.Errorf("formatConcerns() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintPlanStatus(t *testing.T) {
	plan := &vmav1alpha1.MigrationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-plan",
			Namespace: "default",
		},
		Spec: vmav1alpha1.MigrationPlanSpec{
			Type:            vmav1alpha1.MigrationTypeCold,
			TargetNamespace: "kubevirt-vms",
			VMs: []vmav1alpha1.PlanVM{
				{ID: "vm-001", Name: "web-01"},
				{ID: "vm-002", Name: "db-01"},
			},
		},
		Status: vmav1alpha1.MigrationPlanStatus{
			Phase: vmav1alpha1.PlanPhaseReady,
			VMs: []vmav1alpha1.VMValidationStatus{
				{ID: "vm-001", Name: "web-01"},
				{ID: "vm-002", Name: "db-01", Concerns: []vmav1alpha1.Concern{
					{Category: vmav1alpha1.ConcernCategoryWarning, Message: "GPU present"},
				}},
			},
		},
	}

	var buf bytes.Buffer
	printPlanStatus(plan, &buf)
	output := buf.String()

	if !strings.Contains(output, "Plan:       test-plan") {
		t.Error("missing plan name")
	}
	if !strings.Contains(output, "Phase:      Ready") {
		t.Error("missing phase")
	}
	if !strings.Contains(output, "VMs:        2") {
		t.Error("missing VM count")
	}
	if !strings.Contains(output, "vm-001") {
		t.Error("missing VM UUID vm-001")
	}
	if !strings.Contains(output, "[Warning] GPU present") {
		t.Error("missing concern")
	}
	if !strings.Contains(output, "web-01") {
		t.Error("missing VM name web-01")
	}
}

func TestPrintMigrationStatus(t *testing.T) {
	started := metav1.NewTime(time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC))
	completed := metav1.NewTime(time.Date(2026, 3, 20, 10, 30, 0, 0, time.UTC))

	migration := &vmav1alpha1.Migration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-migration",
			Namespace: "default",
		},
		Spec: vmav1alpha1.MigrationSpec{},
		Status: vmav1alpha1.MigrationStatus{
			Phase:     vmav1alpha1.MigrationPhaseRunning,
			Started:   &started,
			Completed: nil,
			VMs: []vmav1alpha1.VMMigrationStatus{
				{
					ID:      "vm-001",
					Name:    "web-01",
					Phase:   vmav1alpha1.VMPhaseImportDisks,
					Started: &started,
				},
				{
					ID:        "vm-002",
					Name:      "db-01",
					Phase:     vmav1alpha1.VMPhaseCompleted,
					Started:   &started,
					Completed: &completed,
				},
				{
					ID:    "vm-003",
					Phase: vmav1alpha1.VMPhaseFailed,
					Error: "snapshot creation failed",
				},
			},
		},
	}

	var buf bytes.Buffer
	printMigrationStatus(migration, &buf)
	output := buf.String()

	if !strings.Contains(output, "Migration:  test-migration") {
		t.Error("missing migration name")
	}
	if !strings.Contains(output, "Phase:      Running") {
		t.Error("missing phase")
	}
	if !strings.Contains(output, "Started:    2026-03-20T10:00:00Z") {
		t.Error("missing started time")
	}
	// Should NOT contain Completed line (nil)
	if strings.Contains(output, "Completed:") {
		t.Error("should not show completed when nil")
	}

	// VM table
	if !strings.Contains(output, "vm-001") {
		t.Error("missing vm-001")
	}
	if !strings.Contains(output, "ImportDisks") {
		t.Error("missing ImportDisks phase")
	}
	if !strings.Contains(output, "vm-002") {
		t.Error("missing vm-002")
	}
	if !strings.Contains(output, "Completed") {
		t.Error("missing Completed phase")
	}
	// VM with no name should show "-"
	if !strings.Contains(output, "vm-003") {
		t.Error("missing vm-003")
	}
	if !strings.Contains(output, "snapshot creation failed") {
		t.Error("missing error message")
	}
}

func TestPrintMigrationStatus_NoVMs(t *testing.T) {
	migration := &vmav1alpha1.Migration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "empty-migration",
			Namespace: "default",
		},
		Status: vmav1alpha1.MigrationStatus{
			Phase: vmav1alpha1.MigrationPhasePending,
		},
	}

	var buf bytes.Buffer
	printMigrationStatus(migration, &buf)
	output := buf.String()

	if !strings.Contains(output, "Phase:      Pending") {
		t.Error("missing Pending phase")
	}
	// Should not contain VM table header since no VMs
	if strings.Contains(output, "VM UUID") {
		t.Error("should not show VM table when empty")
	}
}

func TestNewRootCmd(t *testing.T) {
	cmd := newRootCmd()
	if cmd.Use != "kubectl-vma" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}

	// Verify all subcommands exist
	subcommands := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subcommands[sub.Name()] = true
	}
	for _, name := range []string{"inventory", "plan", "migrate", "status", "cancel"} {
		if !subcommands[name] {
			t.Errorf("missing subcommand: %s", name)
		}
	}
}
