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
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

const recoveryPointStatusComplete = "COMPLETE"

// CBTConfig holds configurable CBT mock behavior.
type CBTConfig struct {
	// ChangedRegions to return for CBT queries.
	ChangedRegions []nutanix.ChangedRegion
	// JWTToken to return from cluster discovery.
	JWTToken string
}

// DefaultCBTConfig returns a default CBT configuration with sample regions.
func DefaultCBTConfig() CBTConfig {
	return CBTConfig{
		ChangedRegions: []nutanix.ChangedRegion{
			{Offset: 0, Length: 65536},
			{Offset: 1048576, Length: 131072},
			{Offset: 4194304, Length: 65536},
		},
		JWTToken: "mock-jwt-token-for-cbt",
	}
}

// Store holds in-memory state for the mock Nutanix API server.
// All access is protected by sync.RWMutex for thread safety.
type Store struct {
	mu sync.RWMutex

	VMs               []nutanix.VM
	Subnets           []nutanix.Subnet
	StorageContainers []nutanix.StorageContainer
	Clusters          []nutanix.Cluster
	Tasks             map[string]*TaskState
	RecoveryPoints    map[string]*nutanix.RecoveryPoint
	Images            map[string]*nutanix.Image
	CBTConfig         CBTConfig

	// imageCounter generates unique image/recovery-point IDs.
	imageCounter atomic.Int64
	// taskCounter generates unique task IDs.
	taskCounter atomic.Int64
}

// TaskState tracks a mock task's lifecycle.
type TaskState struct {
	Task      nutanix.Task
	PollCount int
	// TargetPolls is the number of polls before the task transitions to SUCCEEDED.
	TargetPolls int
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{
		Tasks:          make(map[string]*TaskState),
		RecoveryPoints: make(map[string]*nutanix.RecoveryPoint),
		Images:         make(map[string]*nutanix.Image),
	}
}

// --- VM operations ---

// GetVMs returns a copy of all VMs.
func (s *Store) GetVMs() []nutanix.VM {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]nutanix.VM, len(s.VMs))
	copy(result, s.VMs)
	return result
}

// GetVM returns a VM by ExtID, or nil if not found.
func (s *Store) GetVM(extID string) *nutanix.VM {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.VMs {
		if s.VMs[i].ExtID == extID {
			vm := s.VMs[i]
			return &vm
		}
	}
	return nil
}

// SetVMPowerState updates a VM's power state.
func (s *Store) SetVMPowerState(extID string, state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.VMs {
		if s.VMs[i].ExtID == extID {
			s.VMs[i].PowerState = state
			return true
		}
	}
	return false
}

// --- Subnet operations ---

// GetSubnets returns a copy of all subnets.
func (s *Store) GetSubnets() []nutanix.Subnet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]nutanix.Subnet, len(s.Subnets))
	copy(result, s.Subnets)
	return result
}

// --- Storage container operations ---

// GetStorageContainers returns a copy of all storage containers.
func (s *Store) GetStorageContainers() []nutanix.StorageContainer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]nutanix.StorageContainer, len(s.StorageContainers))
	copy(result, s.StorageContainers)
	return result
}

// --- Cluster operations ---

// GetClusters returns a copy of all clusters.
func (s *Store) GetClusters() []nutanix.Cluster {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]nutanix.Cluster, len(s.Clusters))
	copy(result, s.Clusters)
	return result
}

// --- Recovery Point operations ---

// AddRecoveryPoint adds a recovery point to the store and returns its UUID.
func (s *Store) AddRecoveryPoint(rp *nutanix.RecoveryPoint) string {
	id := s.imageCounter.Add(1)
	rp.ExtID = fmt.Sprintf("rp-%08d", id)
	rp.Status = recoveryPointStatusComplete

	s.mu.Lock()
	defer s.mu.Unlock()
	s.RecoveryPoints[rp.ExtID] = rp
	return rp.ExtID
}

// GetRecoveryPoint returns a recovery point by UUID, or nil if not found.
func (s *Store) GetRecoveryPoint(uuid string) *nutanix.RecoveryPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rp, ok := s.RecoveryPoints[uuid]
	if !ok {
		return nil
	}
	cp := *rp
	return &cp
}

// DeleteRecoveryPoint removes a recovery point from the store.
func (s *Store) DeleteRecoveryPoint(uuid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.RecoveryPoints[uuid]; !ok {
		return false
	}
	delete(s.RecoveryPoints, uuid)
	return true
}

// --- Image operations ---

// AddImage adds an image to the store and returns its UUID.
func (s *Store) AddImage(img *nutanix.Image) string {
	id := s.imageCounter.Add(1)
	img.ExtID = fmt.Sprintf("img-%08d", id)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.Images[img.ExtID] = img
	return img.ExtID
}

// GetImage returns an image by UUID, or nil if not found.
func (s *Store) GetImage(uuid string) *nutanix.Image {
	s.mu.RLock()
	defer s.mu.RUnlock()
	img, ok := s.Images[uuid]
	if !ok {
		return nil
	}
	cp := *img
	return &cp
}

// DeleteImage removes an image from the store.
func (s *Store) DeleteImage(uuid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Images[uuid]; !ok {
		return false
	}
	delete(s.Images, uuid)
	return true
}

// --- Task operations ---

// CreateTask creates a new task in QUEUED state and returns its UUID.
// The task will transition to SUCCEEDED after targetPolls polls.
func (s *Store) CreateTask(entities []nutanix.TaskEntity, targetPolls int) string {
	id := s.taskCounter.Add(1)
	taskUUID := taskUUIDFromCounter(id)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.Tasks[taskUUID] = &TaskState{
		Task: nutanix.Task{
			ExtID:            taskUUID,
			Status:           nutanix.TaskStatusQueued,
			EntitiesAffected: entities,
		},
		TargetPolls: targetPolls,
	}
	return taskUUID
}

// GetTask returns the current state of a task, advancing its lifecycle.
// After TargetPolls polls: QUEUED -> RUNNING -> SUCCEEDED.
func (s *Store) GetTask(uuid string) *nutanix.Task {
	s.mu.Lock()
	defer s.mu.Unlock()

	ts, ok := s.Tasks[uuid]
	if !ok {
		return nil
	}

	ts.PollCount++

	switch {
	case ts.PollCount >= ts.TargetPolls:
		ts.Task.Status = nutanix.TaskStatusSucceeded
		ts.Task.PercentComplete = 100
	case ts.PollCount >= 1:
		ts.Task.Status = nutanix.TaskStatusRunning
		ts.Task.PercentComplete = 50
	}

	task := ts.Task
	return &task
}

// taskUUIDFromCounter generates a deterministic task UUID from a counter value.
func taskUUIDFromCounter(id int64) string {
	return fmt.Sprintf("task-%08d", id)
}
