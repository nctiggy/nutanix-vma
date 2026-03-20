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

// PowerState represents a VM's power state.
type PowerState string

const (
	PowerStateOn  PowerState = "ON"
	PowerStateOff PowerState = "OFF"
)

// VM represents a Nutanix AHV virtual machine (v4 API).
type VM struct {
	ExtID             string      `json:"extId,omitempty"`
	Name              string      `json:"name,omitempty"`
	Description       string      `json:"description,omitempty"`
	PowerState        string      `json:"powerState,omitempty"`
	NumSockets        int         `json:"numSockets,omitempty"`
	NumCoresPerSocket int         `json:"numCoresPerSocket,omitempty"`
	NumThreadsPerCore int         `json:"numThreadsPerCore,omitempty"`
	MemorySizeBytes   int64       `json:"memorySizeBytes,omitempty"`
	MachineType       string      `json:"machineType,omitempty"`
	BootConfig        *BootConfig `json:"bootConfig,omitempty"`
	Disks             []Disk      `json:"disks,omitempty"`
	Nics              []NIC       `json:"nics,omitempty"`
	Gpus              []GPU       `json:"gpus,omitempty"`
	Cluster           *Reference  `json:"cluster,omitempty"`
}

// Disk represents a VM disk attachment.
type Disk struct {
	ExtID         string           `json:"extId,omitempty"`
	DiskAddress   *DiskAddress     `json:"diskAddress,omitempty"`
	BackingInfo   *DiskBackingInfo `json:"backingInfo,omitempty"`
	DiskSizeBytes int64            `json:"diskSizeBytes,omitempty"`
	DeviceType    string           `json:"deviceType,omitempty"`
}

// DiskAddress holds the bus type and index for a disk.
type DiskAddress struct {
	BusType string `json:"busType,omitempty"`
	Index   int    `json:"index,omitempty"`
}

// DiskBackingInfo holds the backing storage reference for a disk.
type DiskBackingInfo struct {
	VMDiskUUID          string     `json:"vmDiskUuid,omitempty"`
	StorageContainerRef *Reference `json:"storageContainerReference,omitempty"`
	DiskSizeBytes       int64      `json:"diskSizeBytes,omitempty"`
}

// NIC represents a VM network interface.
type NIC struct {
	ExtID       string      `json:"extId,omitempty"`
	NetworkRef  *Reference  `json:"subnetReference,omitempty"`
	NicType     string      `json:"nicType,omitempty"`
	MacAddress  string      `json:"macAddress,omitempty"`
	IPAddresses []IPAddress `json:"ipAddresses,omitempty"`
}

// BootConfig describes the VM boot configuration.
type BootConfig struct {
	BootType  string   `json:"bootType,omitempty"`
	BootOrder []string `json:"bootOrder,omitempty"`
}

// GPU represents a GPU passthrough/vGPU assignment.
type GPU struct {
	Mode     string `json:"mode,omitempty"`
	DeviceID int    `json:"deviceId,omitempty"`
	Vendor   string `json:"vendor,omitempty"`
}

// Reference is a generic Nutanix entity reference.
type Reference struct {
	ExtID string `json:"extId,omitempty"`
	Name  string `json:"name,omitempty"`
}

// IPAddress represents an IP address assigned to a NIC.
type IPAddress struct {
	IP   string `json:"value,omitempty"`
	Type string `json:"type,omitempty"`
}

// Subnet represents a Nutanix subnet/network.
type Subnet struct {
	ExtID      string `json:"extId,omitempty"`
	Name       string `json:"name,omitempty"`
	VlanID     int    `json:"vlanId,omitempty"`
	SubnetType string `json:"subnetType,omitempty"`
}

// StorageContainer represents a Nutanix storage container.
type StorageContainer struct {
	UUID string `json:"containerUuid,omitempty"`
	Name string `json:"name,omitempty"`
}

// RecoveryPoint represents a Nutanix recovery point (snapshot).
type RecoveryPoint struct {
	ExtID             string `json:"extId,omitempty"`
	Name              string `json:"name,omitempty"`
	Status            string `json:"status,omitempty"`
	VMExtID           string `json:"vmExtId,omitempty"`
	ExpirationTime    string `json:"expirationTime,omitempty"`
	RecoveryPointType string `json:"recoveryPointType,omitempty"`
}

// Image represents a Nutanix image (disk export).
type Image struct {
	ExtID     string `json:"extId,omitempty"`
	Name      string `json:"name,omitempty"`
	Type      string `json:"type,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
	SourceURI string `json:"sourceUri,omitempty"`
}

// Task represents a Nutanix async task.
type Task struct {
	ExtID            string       `json:"extId,omitempty"`
	Status           string       `json:"status,omitempty"`
	OperationType    string       `json:"operation,omitempty"`
	PercentComplete  int          `json:"percentageComplete,omitempty"`
	ErrorMessages    []TaskError  `json:"errorMessages,omitempty"`
	EntitiesAffected []TaskEntity `json:"entitiesAffected,omitempty"`
}

// TaskError holds an error message from a task.
type TaskError struct {
	Message string `json:"message,omitempty"`
}

// TaskEntity represents an entity affected by a task.
type TaskEntity struct {
	ExtID string `json:"extId,omitempty"`
	Rel   string `json:"rel,omitempty"`
}

// Cluster represents a Nutanix cluster.
type Cluster struct {
	ExtID   string          `json:"extId,omitempty"`
	Name    string          `json:"name,omitempty"`
	Network *ClusterNetwork `json:"network,omitempty"`
}

// ClusterNetwork holds the network config for a cluster.
type ClusterNetwork struct {
	ExternalAddress        string `json:"externalAddress,omitempty"`
	ExternalDataServicesIP string `json:"externalDataServicesIp,omitempty"`
}

// CBTClusterInfo holds the response from CBT cluster discovery.
type CBTClusterInfo struct {
	PrismElementURL string `json:"prismElementUrl,omitempty"`
	JWTToken        string `json:"jwtToken,omitempty"`
}

// ChangedRegions holds the response from a CBT changed regions query.
type ChangedRegions struct {
	Regions    []ChangedRegion `json:"changedRegions,omitempty"`
	NextOffset *int64          `json:"nextOffset,omitempty"`
}

// ChangedRegion represents a single changed block region.
type ChangedRegion struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

// --- API response wrappers ---

// ListResponse is a generic v4 API list response wrapper.
type ListResponse struct {
	Data     []any         `json:"data,omitempty"`
	Metadata *ListMetadata `json:"metadata,omitempty"`
}

// ListMetadata holds pagination info for list responses.
type ListMetadata struct {
	TotalAvailableResults int `json:"totalAvailableResults,omitempty"`
	Offset                int `json:"offset,omitempty"`
}

// SingleResponse is a generic v4 API single-entity response wrapper.
type SingleResponse struct {
	Data any `json:"data,omitempty"`
}
