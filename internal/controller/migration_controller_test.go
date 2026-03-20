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

package controller

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

const (
	migTestPlanName      = "mig-test-plan"
	migTestMigrationName = "test-migration"
	migTestNS            = "default"
	migTestVM1ID         = "mig-vm-001"
	migTestVM2ID         = "mig-vm-002"
	migTestVM3ID         = "mig-vm-003"
)

func newMigrationTestPlan(
	vmIDs []string, maxInFlight int,
) *vmav1alpha1.MigrationPlan {
	vms := make([]vmav1alpha1.PlanVM, 0, len(vmIDs))
	for _, id := range vmIDs {
		vms = append(vms, vmav1alpha1.PlanVM{
			ID: id, Name: "vm-" + id,
		})
	}
	return &vmav1alpha1.MigrationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      migTestPlanName,
			Namespace: migTestNS,
		},
		Spec: vmav1alpha1.MigrationPlanSpec{
			ProviderRef: corev1.LocalObjectReference{
				Name: "provider",
			},
			TargetNamespace: "target",
			NetworkMapRef: corev1.LocalObjectReference{
				Name: "netmap",
			},
			StorageMapRef: corev1.LocalObjectReference{
				Name: "stormap",
			},
			VMs:         vms,
			MaxInFlight: maxInFlight,
		},
	}
}

func newTestMigration() *vmav1alpha1.Migration {
	return &vmav1alpha1.Migration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      migTestMigrationName,
			Namespace: migTestNS,
		},
		Spec: vmav1alpha1.MigrationSpec{
			PlanRef: corev1.LocalObjectReference{
				Name: migTestPlanName,
			},
		},
	}
}

func migrationRequest() ctrl.Request {
	return ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      migTestMigrationName,
			Namespace: migTestNS,
		},
	}
}

func buildMigrationReconciler(
	migration *vmav1alpha1.Migration,
	plan *vmav1alpha1.MigrationPlan,
) *MigrationReconciler {
	s := newTestScheme()
	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(plan, migration).
		WithStatusSubresource(migration).
		Build()
	return &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return &fakeNutanixClient{}, nil
		},
	}
}

func getMigration(
	t *testing.T, r *MigrationReconciler,
) *vmav1alpha1.Migration {
	t.Helper()
	m := &vmav1alpha1.Migration{}
	if err := r.Get(context.Background(), types.NamespacedName{
		Name:      migTestMigrationName,
		Namespace: migTestNS,
	}, m); err != nil {
		t.Fatalf("failed to get migration: %v", err)
	}
	return m
}

func TestMigrationReconcile_FullPipeline(t *testing.T) {
	plan := newMigrationTestPlan([]string{migTestVM1ID}, 1)
	migration := newTestMigration()

	r := buildMigrationReconciler(migration, plan)

	result, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue, got %v", result.RequeueAfter)
	}

	m := getMigration(t, r)
	if m.Status.Phase != vmav1alpha1.MigrationPhaseCompleted {
		t.Errorf("expected Completed, got %s", m.Status.Phase)
	}
	if len(m.Status.VMs) != 1 {
		t.Fatalf("expected 1 VM, got %d", len(m.Status.VMs))
	}
	if m.Status.VMs[0].Phase != vmav1alpha1.VMPhaseCompleted {
		t.Errorf("expected VM Completed, got %s",
			m.Status.VMs[0].Phase)
	}
	if m.Status.VMs[0].Started == nil {
		t.Error("expected VM Started timestamp")
	}
	if m.Status.VMs[0].Completed == nil {
		t.Error("expected VM Completed timestamp")
	}
	if m.Status.Started == nil {
		t.Error("expected migration Started timestamp")
	}
	if m.Status.Completed == nil {
		t.Error("expected migration Completed timestamp")
	}

	// Check Ready condition
	hasReady := false
	for _, c := range m.Status.Conditions {
		if c.Type == conditionTypeMigrationDone &&
			c.Status == metav1.ConditionTrue {
			hasReady = true
		}
	}
	if !hasReady {
		t.Error("expected Ready=True condition")
	}
}

func TestMigrationReconcile_MaxInFlight(t *testing.T) {
	vmIDs := []string{migTestVM1ID, migTestVM2ID, migTestVM3ID}
	plan := newMigrationTestPlan(vmIDs, 1)
	migration := newTestMigration()

	r := buildMigrationReconciler(migration, plan)

	// First reconcile: VM1 completes, VM2 and VM3 still pending
	result, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 1: unexpected error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Error("reconcile 1: expected requeue for pending VMs")
	}

	m := getMigration(t, r)
	if m.Status.Phase != vmav1alpha1.MigrationPhaseRunning {
		t.Errorf("reconcile 1: expected Running, got %s",
			m.Status.Phase)
	}
	if m.Status.VMs[0].Phase != vmav1alpha1.VMPhaseCompleted {
		t.Errorf("VM1 expected Completed, got %s",
			m.Status.VMs[0].Phase)
	}
	if m.Status.VMs[1].Phase != vmav1alpha1.VMPhasePending {
		t.Errorf("VM2 expected Pending, got %s",
			m.Status.VMs[1].Phase)
	}
	if m.Status.VMs[2].Phase != vmav1alpha1.VMPhasePending {
		t.Errorf("VM3 expected Pending, got %s",
			m.Status.VMs[2].Phase)
	}

	// Second reconcile: VM2 completes
	_, err = r.Reconcile(context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 2: unexpected error: %v", err)
	}
	m = getMigration(t, r)
	if m.Status.VMs[1].Phase != vmav1alpha1.VMPhaseCompleted {
		t.Errorf("VM2 expected Completed after reconcile 2, got %s",
			m.Status.VMs[1].Phase)
	}

	// Third reconcile: VM3 completes, overall Completed
	_, err = r.Reconcile(context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 3: unexpected error: %v", err)
	}
	m = getMigration(t, r)
	if m.Status.Phase != vmav1alpha1.MigrationPhaseCompleted {
		t.Errorf("expected Completed after reconcile 3, got %s",
			m.Status.Phase)
	}
	for i, vm := range m.Status.VMs {
		if vm.Phase != vmav1alpha1.VMPhaseCompleted {
			t.Errorf("VM[%d] expected Completed, got %s",
				i, vm.Phase)
		}
	}
}

func TestMigrationReconcile_MultipleVMsHighInFlight(t *testing.T) {
	vmIDs := []string{migTestVM1ID, migTestVM2ID, migTestVM3ID}
	plan := newMigrationTestPlan(vmIDs, 10)
	migration := newTestMigration()

	r := buildMigrationReconciler(migration, plan)

	// All VMs should complete in a single reconcile
	result, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue, got %v", result.RequeueAfter)
	}

	m := getMigration(t, r)
	if m.Status.Phase != vmav1alpha1.MigrationPhaseCompleted {
		t.Errorf("expected Completed, got %s", m.Status.Phase)
	}
	if len(m.Status.VMs) != 3 {
		t.Fatalf("expected 3 VMs, got %d", len(m.Status.VMs))
	}
	for i, vm := range m.Status.VMs {
		if vm.Phase != vmav1alpha1.VMPhaseCompleted {
			t.Errorf("VM[%d] expected Completed, got %s",
				i, vm.Phase)
		}
	}
}

func TestMigrationReconcile_Cancellation(t *testing.T) {
	vmIDs := []string{migTestVM1ID, migTestVM2ID}
	plan := newMigrationTestPlan(vmIDs, 2)
	migration := newTestMigration()
	migration.Spec.Cancel = []string{migTestVM2ID}

	r := buildMigrationReconciler(migration, plan)

	_, err := r.Reconcile(context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := getMigration(t, r)
	if m.Status.VMs[0].Phase != vmav1alpha1.VMPhaseCompleted {
		t.Errorf("VM1 expected Completed, got %s",
			m.Status.VMs[0].Phase)
	}
	if m.Status.VMs[1].Phase != vmav1alpha1.VMPhaseCancelled {
		t.Errorf("VM2 expected Cancelled, got %s",
			m.Status.VMs[1].Phase)
	}
	// 1 completed + 1 cancelled => Completed (not all cancelled)
	if m.Status.Phase != vmav1alpha1.MigrationPhaseCompleted {
		t.Errorf("expected Completed (partial cancel), got %s",
			m.Status.Phase)
	}
}

func TestMigrationReconcile_AllCancelled(t *testing.T) {
	vmIDs := []string{migTestVM1ID, migTestVM2ID}
	plan := newMigrationTestPlan(vmIDs, 2)
	migration := newTestMigration()
	migration.Spec.Cancel = []string{migTestVM1ID, migTestVM2ID}

	r := buildMigrationReconciler(migration, plan)

	_, err := r.Reconcile(context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := getMigration(t, r)
	if m.Status.Phase != vmav1alpha1.MigrationPhaseCancelled {
		t.Errorf("expected Cancelled, got %s", m.Status.Phase)
	}
	for _, vm := range m.Status.VMs {
		if vm.Phase != vmav1alpha1.VMPhaseCancelled {
			t.Errorf("VM %s expected Cancelled, got %s",
				vm.ID, vm.Phase)
		}
		if vm.Completed == nil {
			t.Errorf("VM %s expected Completed timestamp", vm.ID)
		}
	}
}

func TestMigrationReconcile_Finalizer(t *testing.T) {
	plan := newMigrationTestPlan([]string{migTestVM1ID}, 1)
	migration := newTestMigration()

	r := buildMigrationReconciler(migration, plan)

	_, err := r.Reconcile(context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := getMigration(t, r)
	hasFinalizer := false
	for _, f := range m.Finalizers {
		if f == migrationFinalizer {
			hasFinalizer = true
		}
	}
	if !hasFinalizer {
		t.Error("expected finalizer to be added")
	}
}

func TestMigrationReconcile_DeletionBlocked(t *testing.T) {
	s := newTestScheme()
	plan := newMigrationTestPlan([]string{migTestVM1ID}, 1)
	migration := newTestMigration()
	migration.Finalizers = []string{migrationFinalizer}
	now := metav1.Now()
	migration.DeletionTimestamp = &now
	migration.Status.Phase = vmav1alpha1.MigrationPhaseRunning

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(plan, migration).
		WithStatusSubresource(migration).
		Build()

	r := &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return &fakeNutanixClient{}, nil
		},
	}

	result, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected requeue for running migration deletion")
	}

	// Finalizer should still be present
	m := &vmav1alpha1.Migration{}
	_ = fc.Get(context.Background(), types.NamespacedName{
		Name:      migTestMigrationName,
		Namespace: migTestNS,
	}, m)
	hasFinalizer := false
	for _, f := range m.Finalizers {
		if f == migrationFinalizer {
			hasFinalizer = true
		}
	}
	if !hasFinalizer {
		t.Error("expected finalizer to remain on running migration")
	}
}

func TestMigrationReconcile_DeletionAllowed(t *testing.T) {
	s := newTestScheme()
	plan := newMigrationTestPlan([]string{migTestVM1ID}, 1)
	migration := newTestMigration()
	migration.Finalizers = []string{migrationFinalizer}
	now := metav1.Now()
	migration.DeletionTimestamp = &now
	migration.Status.Phase = vmav1alpha1.MigrationPhaseCompleted

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(plan, migration).
		WithStatusSubresource(migration).
		Build()

	r := &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return &fakeNutanixClient{}, nil
		},
	}

	result, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue, got %v", result.RequeueAfter)
	}

	// Finalizer should be removed
	m := &vmav1alpha1.Migration{}
	_ = fc.Get(context.Background(), types.NamespacedName{
		Name:      migTestMigrationName,
		Namespace: migTestNS,
	}, m)
	for _, f := range m.Finalizers {
		if f == migrationFinalizer {
			t.Error("expected finalizer removed for completed migration")
		}
	}
}

func TestMigrationReconcile_NotFound(t *testing.T) {
	s := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(s).Build()

	r := &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return nil, errors.New("factory not called")
		},
	}

	result, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue, got %v", result.RequeueAfter)
	}
}

func TestMigrationReconcile_PlanNotFound(t *testing.T) {
	s := newTestScheme()
	migration := newTestMigration()

	fc := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(migration).
		WithStatusSubresource(migration).
		Build()

	r := &MigrationReconciler{
		Client: fc,
		ClientFactory: func(
			_ nutanix.ClientConfig,
		) (nutanix.NutanixClient, error) {
			return &fakeNutanixClient{}, nil
		},
	}

	_, err := r.Reconcile(context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := &vmav1alpha1.Migration{}
	_ = fc.Get(context.Background(), types.NamespacedName{
		Name:      migTestMigrationName,
		Namespace: migTestNS,
	}, m)

	if m.Status.Phase != vmav1alpha1.MigrationPhaseFailed {
		t.Errorf("expected Failed, got %s", m.Status.Phase)
	}
	if m.Status.Completed == nil {
		t.Error("expected Completed timestamp on failure")
	}
}

func TestMigrationReconcile_Idempotent(t *testing.T) {
	plan := newMigrationTestPlan([]string{migTestVM1ID}, 1)
	migration := newTestMigration()

	r := buildMigrationReconciler(migration, plan)

	// First reconcile: completes
	_, err := r.Reconcile(context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 1: unexpected error: %v", err)
	}

	m := getMigration(t, r)
	if m.Status.Phase != vmav1alpha1.MigrationPhaseCompleted {
		t.Fatalf("expected Completed, got %s", m.Status.Phase)
	}
	completedTime := m.Status.Completed

	// Second reconcile: no-op
	result, err := r.Reconcile(
		context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("reconcile 2: unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue for completed, got %v",
			result.RequeueAfter)
	}

	// Status should be unchanged
	m2 := getMigration(t, r)
	if m2.Status.Phase != vmav1alpha1.MigrationPhaseCompleted {
		t.Errorf("expected Completed, got %s", m2.Status.Phase)
	}
	if !m2.Status.Completed.Equal(completedTime) {
		t.Error("Completed timestamp changed on re-reconcile")
	}
}

func TestMigrationReconcile_VMNamesFromPlan(t *testing.T) {
	plan := newMigrationTestPlan([]string{migTestVM1ID}, 1)
	migration := newTestMigration()

	r := buildMigrationReconciler(migration, plan)

	_, err := r.Reconcile(context.Background(), migrationRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := getMigration(t, r)
	expectedName := "vm-" + migTestVM1ID
	if m.Status.VMs[0].Name != expectedName {
		t.Errorf("expected VM name %q, got %q",
			expectedName, m.Status.VMs[0].Name)
	}
}

func TestNextPhase(t *testing.T) {
	tests := []struct {
		current  vmav1alpha1.VMMigrationPhase
		expected vmav1alpha1.VMMigrationPhase
	}{
		{vmav1alpha1.VMPhasePending, vmav1alpha1.VMPhasePreHook},
		{vmav1alpha1.VMPhasePreHook, vmav1alpha1.VMPhaseStorePowerState},
		{vmav1alpha1.VMPhaseCleanup, vmav1alpha1.VMPhaseCompleted},
		// Terminal phase returns itself
		{vmav1alpha1.VMPhaseCompleted, vmav1alpha1.VMPhaseCompleted},
	}

	for _, tt := range tests {
		got := nextPhase(tt.current)
		if got != tt.expected {
			t.Errorf("nextPhase(%s) = %s, want %s",
				tt.current, got, tt.expected)
		}
	}
}

func TestIsTerminalVMPhase(t *testing.T) {
	if !isTerminalVMPhase(vmav1alpha1.VMPhaseCompleted) {
		t.Error("Completed should be terminal")
	}
	if !isTerminalVMPhase(vmav1alpha1.VMPhaseFailed) {
		t.Error("Failed should be terminal")
	}
	if !isTerminalVMPhase(vmav1alpha1.VMPhaseCancelled) {
		t.Error("Cancelled should be terminal")
	}
	if isTerminalVMPhase(vmav1alpha1.VMPhasePending) {
		t.Error("Pending should not be terminal")
	}
	if isTerminalVMPhase(vmav1alpha1.VMPhaseCreateVM) {
		t.Error("CreateVM should not be terminal")
	}
}

func TestIsActiveVMPhase(t *testing.T) {
	if isActiveVMPhase(vmav1alpha1.VMPhasePending) {
		t.Error("Pending should not be active")
	}
	if isActiveVMPhase(vmav1alpha1.VMPhaseCompleted) {
		t.Error("Completed should not be active")
	}
	if !isActiveVMPhase(vmav1alpha1.VMPhasePreHook) {
		t.Error("PreHook should be active")
	}
	if !isActiveVMPhase(vmav1alpha1.VMPhaseImportDisks) {
		t.Error("ImportDisks should be active")
	}
}
