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
	"net/http/httptest"

	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

// Option configures a mock server.
type Option func(*Server)

// Server wraps an httptest.Server with mock Nutanix state.
type Server struct {
	HTTPServer *httptest.Server
	Store      *Store
}

// NewServer creates a new mock Nutanix API server.
// Call Close() when done to shut down the server.
func NewServer(opts ...Option) *Server {
	s := &Server{
		Store: NewStore(),
	}

	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.HTTPServer = httptest.NewServer(mux)

	return s
}

// URL returns the mock server's base URL.
func (s *Server) URL() string {
	return s.HTTPServer.URL
}

// Close shuts down the mock server.
func (s *Server) Close() {
	s.HTTPServer.Close()
}

// registerRoutes sets up all API route handlers.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// VM endpoints
	mux.HandleFunc("/api/vmm/v4.0/ahv/config/vms", s.handleVMList)
	mux.HandleFunc("/api/vmm/v4.0/ahv/config/vms/", s.handleVMByID)

	// Task endpoints
	mux.HandleFunc("/api/prism/v4.0/config/tasks/", s.handleTask)

	// Network endpoints
	mux.HandleFunc("/api/networking/v4.0/config/subnets", s.handleSubnetList)

	// Storage container endpoints (v2 PE API)
	mux.HandleFunc("/PrismGateway/services/rest/v2.0/storage_containers", s.handleStorageContainerList)

	// Cluster endpoints
	mux.HandleFunc("/api/clustermgmt/v4.0/config/clusters", s.handleClusterList)
}

// WithFixtures loads default fixture data into the mock server.
// Default fixtures: 3 VMs, 2 subnets, 1 storage container, 1 cluster.
func WithFixtures() Option {
	return func(s *Server) {
		s.Store.VMs = defaultVMs()
		s.Store.Subnets = defaultSubnets()
		s.Store.StorageContainers = defaultStorageContainers()
		s.Store.Clusters = defaultClusters()
	}
}

func defaultVMs() []nutanix.VM {
	return []nutanix.VM{
		{
			ExtID:             "vm-uuid-001",
			Name:              "test-vm-linux",
			Description:       "Test Linux VM",
			PowerState:        "ON",
			NumSockets:        2,
			NumCoresPerSocket: 4,
			NumThreadsPerCore: 1,
			MemorySizeBytes:   8589934592, // 8 GiB
			MachineType:       "Q35",
			BootConfig:        &nutanix.BootConfig{BootType: "LEGACY"},
			Disks: []nutanix.Disk{
				{
					ExtID:       "disk-001",
					DiskAddress: &nutanix.DiskAddress{BusType: "SCSI", Index: 0},
					BackingInfo: &nutanix.DiskBackingInfo{
						VMDiskUUID:          "vdisk-001",
						StorageContainerRef: &nutanix.Reference{ExtID: "sc-uuid-001", Name: "default-container"},
						DiskSizeBytes:       53687091200,
					},
					DiskSizeBytes: 53687091200, // 50 GiB
					DeviceType:    "DISK",
				},
			},
			Nics: []nutanix.NIC{
				{
					ExtID:      "nic-001",
					NetworkRef: &nutanix.Reference{ExtID: "subnet-uuid-001", Name: "vm-network"},
					NicType:    "NORMAL_NIC",
					MacAddress: "50:6b:8d:aa:bb:01",
				},
			},
			Cluster: &nutanix.Reference{ExtID: "cluster-uuid-001", Name: "test-cluster"},
		},
		{
			ExtID:             "vm-uuid-002",
			Name:              "test-vm-windows",
			Description:       "Test Windows VM",
			PowerState:        "ON",
			NumSockets:        4,
			NumCoresPerSocket: 2,
			NumThreadsPerCore: 2,
			MemorySizeBytes:   17179869184, // 16 GiB
			MachineType:       "Q35",
			BootConfig:        &nutanix.BootConfig{BootType: "UEFI"},
			Disks: []nutanix.Disk{
				{
					ExtID:       "disk-002",
					DiskAddress: &nutanix.DiskAddress{BusType: "SCSI", Index: 0},
					BackingInfo: &nutanix.DiskBackingInfo{
						VMDiskUUID:          "vdisk-002",
						StorageContainerRef: &nutanix.Reference{ExtID: "sc-uuid-001", Name: "default-container"},
						DiskSizeBytes:       107374182400,
					},
					DiskSizeBytes: 107374182400, // 100 GiB
					DeviceType:    "DISK",
				},
				{
					ExtID:         "disk-003",
					DiskAddress:   &nutanix.DiskAddress{BusType: "IDE", Index: 0},
					DiskSizeBytes: 0,
					DeviceType:    "CDROM",
				},
			},
			Nics: []nutanix.NIC{
				{
					ExtID:      "nic-002",
					NetworkRef: &nutanix.Reference{ExtID: "subnet-uuid-001", Name: "vm-network"},
					NicType:    "NORMAL_NIC",
					MacAddress: "50:6b:8d:aa:bb:02",
				},
			},
			Cluster: &nutanix.Reference{ExtID: "cluster-uuid-001", Name: "test-cluster"},
		},
		{
			ExtID:             "vm-uuid-003",
			Name:              "test-vm-gpu",
			Description:       "Test VM with GPU",
			PowerState:        "OFF",
			NumSockets:        2,
			NumCoresPerSocket: 8,
			NumThreadsPerCore: 1,
			MemorySizeBytes:   34359738368, // 32 GiB
			MachineType:       "Q35",
			BootConfig:        &nutanix.BootConfig{BootType: "SECURE_BOOT"},
			Disks: []nutanix.Disk{
				{
					ExtID:       "disk-004",
					DiskAddress: &nutanix.DiskAddress{BusType: "SCSI", Index: 0},
					BackingInfo: &nutanix.DiskBackingInfo{
						VMDiskUUID:          "vdisk-004",
						StorageContainerRef: &nutanix.Reference{ExtID: "sc-uuid-001", Name: "default-container"},
						DiskSizeBytes:       214748364800,
					},
					DiskSizeBytes: 214748364800, // 200 GiB
					DeviceType:    "DISK",
				},
			},
			Nics: []nutanix.NIC{
				{
					ExtID:      "nic-003",
					NetworkRef: &nutanix.Reference{ExtID: "subnet-uuid-002", Name: "gpu-network"},
					NicType:    "NORMAL_NIC",
					MacAddress: "50:6b:8d:aa:bb:03",
				},
			},
			Gpus: []nutanix.GPU{
				{Mode: "PASSTHROUGH_GRAPHICS", DeviceID: 7864, Vendor: "NVIDIA"},
			},
			Cluster: &nutanix.Reference{ExtID: "cluster-uuid-001", Name: "test-cluster"},
		},
	}
}

func defaultSubnets() []nutanix.Subnet {
	return []nutanix.Subnet{
		{
			ExtID:      "subnet-uuid-001",
			Name:       "vm-network",
			VlanID:     100,
			SubnetType: "VLAN",
		},
		{
			ExtID:      "subnet-uuid-002",
			Name:       "gpu-network",
			VlanID:     200,
			SubnetType: "VLAN",
		},
	}
}

func defaultStorageContainers() []nutanix.StorageContainer {
	return []nutanix.StorageContainer{
		{
			UUID: "sc-uuid-001",
			Name: "default-container",
		},
	}
}

func defaultClusters() []nutanix.Cluster {
	return []nutanix.Cluster{
		{
			ExtID: "cluster-uuid-001",
			Name:  "test-cluster",
			Network: &nutanix.ClusterNetwork{
				ExternalAddress:        "10.0.0.10",
				ExternalDataServicesIP: "10.0.0.11",
			},
		},
	}
}
