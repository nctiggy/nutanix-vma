# PRD: Nutanix VMA -- VM Migration Appliance for KubeVirt

## 1. Introduction/Overview

Nutanix VMA is an open-source Kubernetes operator that migrates virtual machines from Nutanix AHV to KubeVirt. It fills a gap in the ecosystem: **no tool exists** for Nutanix AHV -> KubeVirt migration. KubeVirt Forklift (Red Hat MTV) supports VMware, oVirt, OpenStack, OVA, EC2, and Hyper-V, but Nutanix is absent from its codebase, community, and roadmap.

**Key advantage**: Nutanix AHV runs on KVM/QEMU -- the same hypervisor as KubeVirt. This eliminates the hardest part of VM migration: guest OS conversion. VM disks are raw files, guests already use VirtIO drivers, and virt-v2v is unnecessary. This makes the migration pipeline dramatically simpler than VMware-to-KubeVirt.

The operator follows the Forklift model: Kubernetes-native CRDs for Provider, Plan, Migration, NetworkMap, and StorageMap, with a controller that orchestrates the full migration lifecycle. It supports cold migration (power off, copy, create) and warm migration (CBT-based incremental sync with minimal downtime cutover).

## 2. Goals

1. **Cold migration of Nutanix AHV VMs to KubeVirt** -- power off source VM, export disk images, import into KubeVirt PVCs via CDI, create VirtualMachine CR, start VM.
2. **Warm migration with minimal downtime** -- use Nutanix v4 CBT/CRT APIs for incremental block sync while the source VM runs, then a brief cutover window for the final delta.
3. **Kubernetes-native declarative API** -- CRDs for Provider, MigrationPlan, Migration, NetworkMap, StorageMap, and Hook. Users define desired state, operator reconciles.
4. **Automated VM metadata translation** -- map Nutanix CPU, memory, firmware, disks, NICs to KubeVirt VirtualMachine spec with no manual configuration.
5. **Pre-flight validation** -- detect incompatible VMs (GPU passthrough, Volume Groups, unsupported configurations) before migration starts.
6. **Comprehensive test suite** -- unit tests, integration tests against a mock Nutanix API server, and flagged E2E tests for real infrastructure. CI via GitHub Actions.
7. **Works on any vanilla Kubernetes** with KubeVirt and CDI installed. No OpenShift or vendor-specific dependencies.
8. **Open source** from day one under Apache 2.0 license.

## 3. User Stories

### Epic 1: Provider & Inventory

- **US-1.1**: As a platform engineer, I can create a `NutanixProvider` CR pointing to my Prism Central instance, and the operator connects, authenticates, and reports a `Ready` status so I know the connection works.
- **US-1.2**: As a platform engineer, I can view the cached inventory of VMs, subnets, and storage containers from my Nutanix cluster through the Provider's status or a CLI command, so I know what's available to migrate.
- **US-1.3**: As a platform engineer, I can configure TLS settings (custom CA, skip verification) on the Provider so it works with self-signed certificates in lab environments.

### Epic 2: Resource Mapping

- **US-2.1**: As a platform engineer, I can create a `NetworkMap` that maps Nutanix subnets to KubeVirt pod networks or Multus NetworkAttachmentDefinitions, so VMs get the correct network configuration after migration.
- **US-2.2**: As a platform engineer, I can create a `StorageMap` that maps Nutanix storage containers to Kubernetes StorageClasses with explicit volume mode and access mode, so disk data lands in the right storage.
- **US-2.3**: As a platform engineer, the operator validates my maps against the provider inventory and reports errors if a referenced subnet or container doesn't exist.

### Epic 3: Migration Planning

- **US-3.1**: As a platform engineer, I can create a `MigrationPlan` selecting specific VMs by UUID, referencing my NetworkMap and StorageMap, and the operator validates each VM's compatibility.
- **US-3.2**: As a platform engineer, I can see per-VM validation results (concerns/warnings) on the plan status -- GPU VMs flagged, Volume Group VMs excluded, unmapped networks/storage reported.
- **US-3.3**: As a platform engineer, I can override the target VM name, target namespace, and power state per VM in the plan.

### Epic 4: Cold Migration

- **US-4.1**: As a platform engineer, I can create a `Migration` CR referencing a validated plan, and the operator executes cold migration: power off source -> snapshot -> export disks -> import to CDI -> create KubeVirt VM -> start VM.
- **US-4.2**: As a platform engineer, I can monitor migration progress per-VM through the Migration status, seeing which pipeline phase each VM is in and disk transfer percentage.
- **US-4.3**: As a platform engineer, I can cancel a migration for specific VMs by adding their IDs to the cancel list, and the operator cleans up partial resources.
- **US-4.4**: As a platform engineer, if a migration fails mid-pipeline, the operator cleans up partial resources (DataVolumes, PVCs, snapshots) and reports the error. The source VM is restored to its original power state.

### Epic 5: Warm Migration

- **US-5.1**: As a platform engineer, I can create a warm migration plan that performs an initial bulk copy while the source VM runs, then incrementally syncs changed blocks using CBT.
- **US-5.2**: As a platform engineer, I can trigger cutover by setting a timestamp on the Migration CR, and the operator powers off the source, transfers the final delta, creates the KubeVirt VM, and starts it.
- **US-5.3**: As a platform engineer, I can see precopy progress (number of sync rounds, data transferred, delta size trend) in the Migration status.

### Epic 6: Hooks

- **US-6.1**: As a platform engineer, I can define pre-migration and post-migration hooks (container images or Ansible playbooks) that run as Kubernetes Jobs before/after each VM migration.

### Epic 7: Observability & CLI

- **US-7.1**: As a platform engineer, I can use a `kubectl vma` plugin to list inventory, create plans, trigger migrations, and check status from the command line.
- **US-7.2**: As a platform engineer, the operator emits Kubernetes events and structured logs for each migration phase transition, so I can monitor via standard K8s tooling.

### Epic 8: CI/CD & Testing

- **US-8.1**: As a developer, every PR runs unit tests and integration tests (against the mock Nutanix API server) via GitHub Actions, and the build must pass before merge.
- **US-8.2**: As a developer, I can run E2E tests against a real Nutanix cluster and KubeVirt cluster by setting environment flags, and these tests validate full migration end-to-end.

## 4. Functional Requirements

### FR-1: Nutanix Prism API Client

1. The system must authenticate to Prism Central using HTTP Basic Auth (username/password).
2. The system must support custom CA certificates and insecure TLS skip for lab environments. The operator must emit a Warning event when `insecureSkipVerify` is enabled.
3. The system must list VMs via the v4 VMM API (`GET /api/vmm/v4.0/ahv/config/vms`) with pagination.
4. The system must get VM details including disk_list, nic_list, boot_config, CPU, memory, cluster_reference.
5. The system must list subnets via the v4 Networking API (`GET /api/networking/v4.0/config/subnets`).
6. The system must list storage containers via the v2 Prism Element API (`GET /api/nutanix/v2.0/storage_containers`).
7. The system must power on/off VMs via the v4 VMM API.
8. The system must create and delete recovery points (snapshots) via the v4 Data Protection API (`POST /api/dataprotection/v4.0/config/recovery-points`).
9. The system must create images from vDisk snapshots and download image data via the v3 Images API (`GET /api/nutanix/v3/images/{uuid}/file`).
10. The system must compute changed block regions via the v4 CBT/CRT API (two-step: PC cluster discovery `POST .../recovery-points/{uuid}/$actions/compute-changed-regions` -> PE data retrieval `GET .../recovery-points/{uuid}/changed-regions`).
11. The system must handle CBT JWT token refresh (15-minute expiry) during paginated region queries.
12. The system must poll Nutanix async task status (`GET /api/prism/v4.0/config/tasks/{uuid}`) for operations that return task UUIDs (power off, snapshot create, image create). This is required because Nutanix API operations are asynchronous.
13. The system must auto-discover Prism Element cluster IPs from the Prism Central cluster list API, since the NutanixProvider CRD only accepts a Prism Central URL but some endpoints (storage containers, CBT data) require Prism Element access.
14. The system must implement client-side rate limiting and honor HTTP 429 responses with exponential backoff.
15. The API client must be built as a custom HTTP client using `net/http` with a clean interface, not the auto-generated Nutanix Go SDK (stability concerns with auto-generated, breaking changes between releases).

### FR-2: Mock Nutanix API Server

16. The system must include a mock Nutanix API server that implements the subset of Prism Central v3/v4 and Prism Element v2 APIs used by the operator.
17. The mock server must serve realistic responses based on the Nutanix API specifications.
18. The mock server must support CRUD operations against in-memory state (create VMs, snapshots, images).
19. The mock server must simulate disk image downloads (returning synthetic data of configurable size) and support HTTP Range headers for partial downloads.
20. The mock server must simulate CBT responses with configurable changed regions.
21. The mock server must simulate the Nutanix async task model -- operations return task UUIDs, task status is queryable, tasks transition through QUEUED -> RUNNING -> SUCCEEDED states.
22. The mock server must be usable both as a Go test helper (in-process) and as a standalone binary for development.
23. The mock server must serve both Prism Central and Prism Element endpoints (on different path prefixes or configurable ports).

### FR-3: CRDs and Controllers

24. The system must define CRDs: `NutanixProvider`, `NetworkMap`, `StorageMap`, `MigrationPlan`, `Migration`, `Hook` in API group `vma.nutanix.io/v1alpha1`.
25. The Provider controller must reconcile `NutanixProvider` CRs: connect to Prism Central, auto-discover PE clusters, refresh inventory periodically, update status with VM/subnet/container counts and Ready condition. The Provider must validate that the referenced Secret exists and contains required keys (`username`, `password`, optionally `ca.crt`).
26. The Plan controller must reconcile `MigrationPlan` CRs: validate VM compatibility against maps, update per-VM status with validation results and concerns.
27. The Migration controller must reconcile `Migration` CRs: execute the migration pipeline per-VM, track phase transitions, handle errors and cancellation.
28. All controllers must use controller-runtime (kubebuilder) conventions with proper RBAC, leader election (configmapsleases resource lock), and health/readiness probes.
29. The Migration CR must have a finalizer to prevent deletion while the pipeline is running (which would leak Nutanix snapshots and images). The Provider CR must have a finalizer to prevent deletion while Plans reference it.
30. DataVolumes and PVCs created by the migration must have owner references to the Migration CR for garbage collection. KubeVirt VirtualMachine CRs must NOT have owner references (they must outlive the Migration CR).
31. All CRDs must define standard `metav1.Condition` types: Provider (`Connected`, `InventoryReady`), Plan (`Valid`, `Ready`), Migration (`Executing`, `Succeeded`, `Failed`).

### FR-4: Cold Migration Pipeline

32. The pipeline phases must execute in order: PreHook -> StorePowerState -> PowerOff -> WaitForPowerOff -> CreateSnapshot -> ExportDisks -> ImportDisks (CDI DataVolume) -> CreateVM -> StartVM -> PostHook -> Cleanup -> Completed.
33. The system must create a Nutanix Image Service credential secret (with `accessKeyId` and `secretKey` fields matching CDI's HTTP Basic Auth format) in the target namespace for CDI to authenticate to Prism Central. This secret must be created before DataVolumes and cleaned up after import.
34. The system must create CDI DataVolumes with HTTP source pointing to the Nutanix Image Service download URL for each VM disk. The DataVolume must propagate `volumeMode` and `accessMode` from the StorageMap.
35. The system must translate Nutanix VM metadata to a KubeVirt VirtualMachine CR:
    - CPU topology preserved: `num_sockets` -> `spec.domain.cpu.sockets`, `num_vcpus_per_socket` -> `spec.domain.cpu.cores`, `num_threads_per_core` -> `spec.domain.cpu.threads` (default 1 if absent)
    - Memory: `memory_size_mib` -> `spec.domain.resources.requests.memory`
    - Firmware: `LEGACY` -> BIOS, `UEFI` -> EFI, `SECURE_BOOT` -> EFI with secureBoot
    - Disks: each DISK-type entry -> PVC volume with `bus: scsi` (matching AHV's virtio-scsi). CDROM-type entries are skipped (not migrated) but must not crash the builder.
    - NICs: mapped per NetworkMap (pod/masquerade or multus/bridge). MAC addresses preserved on bridge/multus interfaces (not possible with masquerade/pod network).
    - Machine type: `Q35` -> `q35`, `PC` -> `pc` (KubeVirt aliases, not versioned strings)
36. The system must set `bootOrder: 1` on the first disk matching the source VM's boot device.
37. The system must sanitize Nutanix VM names to DNS1123 format for K8s resource names.
38. The system must add labels/annotations to both `metadata` and `spec.template.metadata` of the KubeVirt VM recording the source Nutanix UUID, migration ID, and timestamp.
39. On failure at any phase, the system must clean up partial resources (DataVolumes, PVCs, images, snapshots, credential secrets) and restore the source VM to its original power state.
40. The system must respect `maxInFlight` concurrency limits when migrating multiple VMs.

### FR-5: Warm Migration Pipeline

41. The warm migration must create the target PVC/DataVolume at full disk size before the initial bulk copy (not during cutover).
42. The initial bulk copy must use a CDI DataVolume with HTTP source, same as cold migration.
43. After the initial bulk copy completes (CDI DataVolume reaches Succeeded), the system must enter a precopy loop: create snapshot -> compute CBT deltas -> transfer changed blocks -> delete old snapshot. Repeat at configurable intervals (default 30 minutes).
44. When cutover is triggered (via `migration.spec.cutover` timestamp), the system must: exit precopy loop -> power off source -> create final snapshot -> transfer final delta -> create KubeVirt VM -> start VM.
45. The system must track precopy progress: number of rounds, cumulative data transferred, last delta size.
46. **Delta transfer mechanism**: The delta transfer pod must mount the target PVC and write changed blocks at their correct byte offsets. For Block-mode PVCs, write directly to the block device. For Filesystem-mode PVCs, write to the CDI-created `disk.img` file at `/pvc/disk.img`. The delta pod image must contain a minimal binary that reads block data from Nutanix and writes to the PVC.
47. **Delta data source**: The delta transfer reads changed block DATA from the Nutanix Image Service download endpoint (`GET /api/nutanix/v3/images/{uuid}/file`) using HTTP Range headers (if supported) to read only the specific byte ranges identified by CBT. If Range headers are NOT supported by the real Nutanix API (Open Question -- mock server supports them), the fallback is to download the entire recovery point image and extract only the changed ranges. This fallback reduces the benefit of warm migration for large disks.
48. The PVC `accessMode` for warm migration must be `ReadWriteOnce`. The handoff between CDI importer pod (initial bulk copy) and delta transfer pod requires CDI to release the PVC first. The system must wait for the CDI DataVolume to reach `Succeeded` (importer pod terminated) before starting the first precopy delta transfer.

### FR-6: Validation

49. The system must validate that all VM disks reference storage containers present in the StorageMap.
50. The system must validate that all VM NICs reference subnets present in the NetworkMap.
51. The system must flag VMs with GPU passthrough as unsupported (warning, not blocking).
52. The system must flag VMs with Volume Group references as unsupported (error, blocking).
53. The system must flag VMs with unsupported NIC types (NETWORK_FUNCTION_NIC) as error.
54. The system must detect MAC address conflicts with existing KubeVirt VMs in the target namespace.
55. The system must validate that the target namespace exists.
56. The system must validate that disks with `adapter_type: IDE` are mapped to `disk.bus: sata` (since KubeVirt deprecated IDE bus support).

### FR-7: Hooks

57. The system must support pre-migration and post-migration hooks defined as `Hook` CRs.
58. Hooks must run as Kubernetes Jobs with configurable image, service account, and timeout.
59. Hook Jobs must receive migration context (VM details, plan details) via a mounted ConfigMap.
60. Hook failure must be retryable (default 3 attempts) and must block the pipeline.

### FR-8: kubectl Plugin

61. The system must include a `kubectl-vma` plugin providing subcommands: `inventory`, `plan`, `migrate`, `status`, `cancel`.
62. `kubectl vma inventory <provider>` must display VMs, subnets, and storage containers in table format.
63. `kubectl vma status <migration>` must display per-VM pipeline progress.

### FR-9: Observability

64. The operator must emit Kubernetes Events for each migration phase transition (Normal for progress, Warning for errors).
65. The operator must use structured logging (JSON) with fields: migration, vm, phase, duration.
66. The operator must expose Prometheus metrics: `vma_migrations_total`, `vma_migration_duration_seconds`, `vma_disk_transfer_bytes`, `vma_active_migrations`.

### FR-10: CI/CD

67. The project must include GitHub Actions workflows for: lint, build, unit test, integration test (mock API), Docker image build.
68. Unit tests must achieve >80% coverage on the API client, VM builder, and validation packages.
69. Integration tests must run the mock Nutanix API server, create CRs against an envtest (controller-runtime test environment), and verify controller behavior. NOTE: envtest does NOT run CDI or KubeVirt controllers -- integration tests verify CR creation and status updates only, not actual data import or VM start. Full pipeline validation requires E2E tests.
70. E2E tests must be gated behind a `NUTANIX_E2E=true` environment variable and require `NUTANIX_PC_URL`, `NUTANIX_USERNAME`, `NUTANIX_PASSWORD` secrets.
71. The CI must produce a container image pushed to GitHub Container Registry (ghcr.io).
72. The project must include a Makefile with targets: `build`, `test`, `test-integration`, `test-e2e`, `lint`, `generate` (CRD manifests), `docker-build`, `docker-push`, `install` (CRDs to cluster), `deploy` (operator to cluster).

## 5. Non-Goals (Out of Scope)

- **Web UI / Dashboard**: No web interface in this version. kubectl plugin and CRD status are the UX.
- **OpenShift Console Plugin**: No OpenShift-specific integration. Vanilla K8s only.
- **OLM Packaging**: No Operator Lifecycle Manager packaging. Deployed via Makefile/Kustomize.
- **Live Migration**: Cross-hypervisor live migration is not technically possible. Only cold and warm (CBT) migration.
- **GPU Migration**: GPU passthrough VMs are flagged as unsupported. No automated GPU config mapping.
- **Volume Group Migration**: VMs with external iSCSI Volume Groups are flagged as unsupported.
- **Nutanix ESXi**: Only AHV hypervisor is supported. Nutanix clusters running ESXi are out of scope.
- **Multi-destination**: Only KubeVirt is a target. No support for migrating to other hypervisors.
- **Bidirectional migration**: No KubeVirt -> Nutanix migration.
- **Automatic NGT removal**: NGT uninstallation is left to pre-migration hooks or manual action.
- **CD-ROM / ISO migration**: CDROM entries in VM disk_list are skipped. Only DISK-type entries are migrated.

## 6. Design Considerations

### Architecture

```
┌─────────────────────────────────────────┐
│          nutanix-vma-operator            │
│                                          │
│  ┌──────────────┐  ┌─────────────────┐  │
│  │   Inventory   │  │   Migration     │  │
│  │   Controller  │  │   Controller    │  │
│  └───────┬───────┘  └───────┬─────────┘  │
│          │                  │             │
│  ┌───────┴───────┐  ┌──────┴──────────┐  │
│  │  Prism Client  │  │ Transfer Manager│  │
│  │  (net/http)    │  │ (CDI + CBT)     │  │
│  └───────┬───────┘  └──────┬──────────┘  │
└──────────┼─────────────────┼─────────────┘
           │                 │
    ┌──────▼──────┐   ┌──────▼──────────┐
    │Prism Central│   │  KubeVirt + CDI  │
    │  v3/v4 API  │   │  (DataVolumes,   │
    │             │   │   VirtualMachine) │
    │Prism Element│   └─────────────────┘
    │(CBT/Storage)│
    └─────────────┘
```

### CRD API Group

`vma.nutanix.io/v1alpha1`

### Disk Bus Strategy

Default to `disk.bus: scsi` for all migrated SCSI/PCI disks. Map `adapter_type: IDE` to `disk.bus: sata`. Map `adapter_type: SATA` to `disk.bus: sata`. This matches AHV's virtio-scsi presentation, preserving `/dev/sd*` device naming and maximizing guest compatibility. No virt-v2v or driver changes needed.

### API Client Strategy

Build a custom HTTP client using `net/http` with a clean `NutanixClient` interface. Do NOT use the auto-generated Nutanix Go SDK -- it has breaking changes between releases and may lag API changes. The client interface is defined incrementally: Story 3 creates the interface with stub (not-implemented) methods for all operations. Stories 4-7 replace stubs with real implementations. This ensures `make build` passes at every step.

### Nutanix API Response Types (Reference for Implementation)

These Go struct definitions represent the Nutanix API response schemas the client must parse. They are based on the Nutanix v4/v3/v2 API documentation.

```go
// VM (v4 VMM API response)
type VM struct {
    ExtID             string           `json:"extId"`
    Name              string           `json:"name"`
    Description       string           `json:"description,omitempty"`
    NumSockets        int              `json:"numSockets"`
    NumVcpusPerSocket int              `json:"numVcpusPerSocket"`
    NumThreadsPerCore int              `json:"numThreadsPerCore,omitempty"` // default 1
    MemorySizeMiB     int64            `json:"memorySizeBytes"`            // API returns bytes, convert to MiB
    MachineType       string           `json:"machineType"`                // PC, Q35
    PowerState        string           `json:"powerState"`                 // ON, OFF
    BootConfig        *BootConfig      `json:"bootConfig,omitempty"`
    Disks             []Disk           `json:"disks,omitempty"`
    Nics              []NIC            `json:"nics,omitempty"`
    GpuList           []GPU            `json:"gpus,omitempty"`
    ClusterReference  *Reference       `json:"cluster,omitempty"`
    Categories        map[string]string `json:"categories,omitempty"`
}

type BootConfig struct {
    BootType string `json:"bootType"` // LEGACY, UEFI, SECURE_BOOT
}

type Disk struct {
    ExtID              string     `json:"extId"`
    DiskSizeBytes      int64      `json:"diskSizeBytes"`
    DeviceType         string     `json:"deviceType"`   // DISK, CDROM
    AdapterType        string     `json:"adapterType"`  // SCSI, IDE, PCI, SATA
    DeviceIndex        int        `json:"deviceIndex"`
    StorageContainerRef *Reference `json:"storageContainer,omitempty"`
    VolumeGroupRef     *Reference `json:"volumeGroup,omitempty"`
}

type NIC struct {
    ExtID        string      `json:"extId"`
    SubnetRef    *Reference  `json:"subnet,omitempty"`
    MacAddress   string      `json:"macAddress,omitempty"`
    IPAddresses  []IPAddress `json:"ipAddresses,omitempty"`
    NicType      string      `json:"nicType"`  // NORMAL_NIC, DIRECT_NIC, NETWORK_FUNCTION_NIC
    Model        string      `json:"model"`    // virtio
}

type GPU struct {
    Mode     string `json:"mode"`     // PASSTHROUGH_GRAPHICS, PASSTHROUGH_COMPUTE, VIRTUAL
    Vendor   string `json:"vendor"`   // NVIDIA, AMD, Intel
    DeviceID int    `json:"deviceId"`
}

type Reference struct {
    ExtID string `json:"extId"`
}

type IPAddress struct {
    IP   string `json:"ip"`
    Type string `json:"type"` // ASSIGNED, LEARNED
}

// Subnet (v4 Networking API)
type Subnet struct {
    ExtID  string `json:"extId"`
    Name   string `json:"name"`
    VlanID int    `json:"vlanId,omitempty"`
}

// StorageContainer (v2 API)
type StorageContainer struct {
    UUID              string `json:"storage_container_uuid"`
    Name              string `json:"name"`
    MaxCapacityBytes  int64  `json:"max_capacity"`
    UsedBytes         int64  `json:"usage"`
}

// RecoveryPoint (v4 Data Protection API)
type RecoveryPoint struct {
    ExtID  string `json:"extId"`
    Name   string `json:"name"`
    Status string `json:"status"` // COMPLETE, IN_PROGRESS
    VMRecoveryPoints []VMRecoveryPoint `json:"vmRecoveryPoints,omitempty"`
}

type VMRecoveryPoint struct {
    VMExtID   string          `json:"vmExtId"`
    DiskRecoveryPoints []DiskRecoveryPoint `json:"diskRecoveryPoints,omitempty"`
}

type DiskRecoveryPoint struct {
    DiskExtID      string `json:"diskExtId"`
    DiskSizeBytes  int64  `json:"diskSizeBytes"`
}

// Image (v3 API)
type Image struct {
    UUID       string            `json:"uuid"`
    Name       string            `json:"name"`
    ImageType  string            `json:"image_type"` // DISK_IMAGE, ISO_IMAGE
    Status     map[string]string `json:"status"`
}

// Task (v4 Prism API -- async task polling)
type Task struct {
    ExtID          string `json:"extId"`
    Status         string `json:"status"` // QUEUED, RUNNING, SUCCEEDED, FAILED
    PercentComplete int   `json:"percentageComplete,omitempty"`
    ErrorMessages  []string `json:"errorMessages,omitempty"`
}

// CBT (v4 Data Protection)
type CBTClusterInfo struct {
    ClusterIP   string `json:"clusterIp"`
    JWTToken    string `json:"jwtToken"`
    RedirectURI string `json:"redirectUri"`
}

type ChangedRegions struct {
    Regions    []ChangedRegion `json:"changedRegions"`
    NextOffset *int64          `json:"nextOffset,omitempty"` // nil = no more pages
    FileSize   int64           `json:"fileSize"`
}

type ChangedRegion struct {
    Offset int64 `json:"offset"`
    Length int64 `json:"length"`
    IsZero bool  `json:"isZero"`
}
```

### AGENTS.md Content (for Ralph)

The AGENTS.md file must contain the following sections for Ralph to operate effectively:

```markdown
# AGENTS.md -- Ralph Instructions for nutanix-vma

## Build Commands
- `make build` -- compile all binaries (operator, kubectl-vma, mock-nutanix)
- `make test` -- run unit tests
- `make test-integration` -- run integration tests (starts mock server + envtest)
- `make lint` -- run golangci-lint
- `make generate` -- generate DeepCopy methods (run after changing api/v1alpha1/ types)
- `make manifests` -- generate CRD YAML (run after changing api/v1alpha1/ types)
- `make install` -- install CRDs to cluster
- `make deploy` -- deploy operator to cluster
- `make docker-build` -- build container image

## Project Structure
- `api/v1alpha1/` -- CRD type definitions. Run `make generate && make manifests` after changes.
- `internal/controller/` -- Kubernetes controllers (provider, plan, migration)
- `internal/nutanix/` -- Prism API client (custom net/http, NOT the Nutanix Go SDK)
- `internal/builder/` -- Nutanix VM -> KubeVirt VM translation
- `internal/validation/` -- Pre-flight VM compatibility checks
- `internal/transfer/` -- Disk transfer orchestration (CDI DataVolumes, CBT delta)
- `pkg/mock/` -- Mock Nutanix API server for testing
- `cmd/operator/` -- Operator binary entry point
- `cmd/kubectl-vma/` -- kubectl plugin entry point
- `cmd/mock-nutanix/` -- Standalone mock server binary

## Testing Conventions
- Unit tests: same directory as source, `_test.go` suffix
- Integration tests: `test/integration/`, use envtest + mock Nutanix server
- E2E tests: `test/e2e/`, require NUTANIX_E2E=true + real infra
- Use httptest.Server for unit testing the Nutanix client

## Key Design Decisions
- Nutanix API client uses net/http, NOT the auto-generated Nutanix Go SDK
- Disk bus is `scsi` (matching AHV virtio-scsi), not `virtio`
- CPU topology is PRESERVED (sockets/cores/threads), not flattened
- Machine type uses KubeVirt aliases: `q35` and `pc`
- Nutanix operations are async -- always poll task status after mutating calls
- CRD API group: vma.nutanix.io/v1alpha1

## Patterns
- All Nutanix mutating operations (power off, snapshot, image create) return a Task UUID.
  Poll GET /api/prism/v4.0/config/tasks/{uuid} until status is SUCCEEDED or FAILED.
- Migration CR must have a finalizer (vma.nutanix.io/migration-protection).
- DataVolumes/PVCs get owner references to Migration CR.
- KubeVirt VMs do NOT get owner references (they outlive Migration CR).
```

## 7. Technical Considerations

### Dependencies

| Dependency | Version | Purpose |
|-----------|---------|---------|
| Go | 1.22+ | Language |
| controller-runtime | v0.18+ | Operator framework |
| kubebuilder | v4+ | CRD scaffolding |
| KubeVirt API (`kubevirt.io/api`) | v1.2+ | VirtualMachine types |
| CDI API (`kubevirt.io/containerized-data-importer-api`) | v1.58+ | DataVolume types |
| envtest | (bundled) | Integration test K8s env |
| golangci-lint | latest | Linting |
| Ginkgo/Gomega | v2 | Test framework (E2E) |

### Nutanix API Versions Used

| Endpoint | API Version | Path | Available On |
|----------|-------------|------|-------------|
| VM list/get/power | v4 VMM | `/api/vmm/v4.0/ahv/config/vms` | Prism Central |
| Subnets | v4 Networking | `/api/networking/v4.0/config/subnets` | Prism Central |
| Recovery points | v4 Data Protection | `/api/dataprotection/v4.0/config/recovery-points` | Prism Central |
| CBT cluster discovery | v4 Data Protection | `/api/dataprotection/v4.0/config/recovery-points/{uuid}/$actions/compute-changed-regions` | Prism Central |
| CBT changed regions | v4 Data Protection | `/api/dataprotection/v4.0/config/recovery-points/{uuid}/changed-regions` | Prism Element |
| Async task status | v4 Prism | `/api/prism/v4.0/config/tasks/{uuid}` | Prism Central + Element |
| Cluster list | v4 Clustermgmt | `/api/clustermgmt/v4.0/config/clusters` | Prism Central |
| Image create/download | v3 | `/api/nutanix/v3/images/{uuid}/file` | Prism Central |
| Storage containers | v2 | `/api/nutanix/v2.0/storage_containers` | Prism Element |

### Known Constraints & Risks

1. **No Nutanix cluster available yet** -- all development uses the mock API server. Real cluster validation is deferred.
2. **CBT JWT tokens expire in 15 minutes** -- token refresh logic required for large disks.
3. **Prism Central vs Element split** -- some APIs are PC-only, some PE-only. The operator auto-discovers PE IPs from PC cluster list API.
4. **Image Service downloads entire disk as one HTTP response** -- CDI's HTTP importer handles streaming to PVC.
5. **Nutanix Go SDK instability** -- deliberately avoided; custom HTTP client used instead.
6. **CDI importer needs network access to Prism Central** -- CDI pods pull disk data via HTTP from Prism. If K8s worker nodes lack routes to the Nutanix management network, migration will fail. This is a deployment prerequisite, not something the operator can fix.
7. **Warm migration delta data source** (CRITICAL RISK) -- The CBT API tells you WHICH blocks changed but not their contents. Reading block data requires the Image Service download endpoint with HTTP Range headers. If Range headers are NOT supported by the real Nutanix API, warm migration falls back to downloading the full recovery point image and extracting changed ranges, which reduces the benefit for large disks. This must be validated with a real cluster.

### GitHub Repository

- Module path: `github.com/nutanix-vma/nutanix-vma` (placeholder until GitHub org is created)
- License: Apache 2.0
- GitHub Actions for CI (lint, build, test, integration)
- GitHub Container Registry for images (`ghcr.io/nutanix-vma/nutanix-vma`)

## 8. Success Metrics

1. **Cold migration works end-to-end** -- a VM on the mock Nutanix API is fully migrated to a KubeVirt VirtualMachine CR in envtest, with all metadata correctly translated.
2. **Warm migration works end-to-end** -- CBT-based incremental sync completes against the mock API, delta transfer writes correct blocks, cutover produces a working VM.
3. **All tests pass in CI** -- unit test coverage >80% on client, builder, validation. Integration tests verify controller reconciliation. GitHub Actions green.
4. **Operator deploys and runs** -- `make deploy` installs CRDs and starts the operator on any vanilla K8s cluster with KubeVirt + CDI.
5. **kubectl plugin works** -- `kubectl vma inventory`, `kubectl vma status` produce correct output.
6. **Each Ralph iteration produces buildable, testable code** -- `make build && make test` passes after every story.

## 9. Open Questions

1. **Image creation from recovery point vDisks** -- exact API flow needs validation with a real cluster. Mock assumes `POST /api/vmm/v4.0/content/images` with `data_source_reference`.
2. **HTTP Range request support** -- does Nutanix Image Service support Range headers? Critical for warm migration delta reads. Mock supports it; real behavior TBD.
3. **PE credential propagation** -- do PC credentials work for PE API calls? Mock assumes yes.
4. **CBT block granularity** -- minimum block size for changed regions? Mock defaults to 1MB.
5. **Multi-cluster inventory** -- how does PC report VMs from multiple PE clusters? Mock uses `cluster_reference` on each VM.
6. **VM categories to K8s labels** -- deferred to post-cluster-access PRD.
7. **CDI importer network access** -- can CDI pods reach Prism Central port 9440? Deployment prerequisite.
8. **Image creation from snapshot vDisk** -- does creating an image require cloning the VM from the recovery point first, or can you reference the vDisk directly? Mock assumes direct reference.

---

## User Stories for Ralph (Implementation Order)

Stories are executed **by story number** (not by priority). Priority is used to indicate importance -- P2 stories can be skipped in an MVP but should still be implemented in order relative to other stories. Every story must compile (`make build`) and pass tests (`make test`) after completion.

### Story 1: Project Scaffolding
**Priority: P0** | **Depends on: nothing**

Initialize the Go module and kubebuilder project. Create Makefile, Dockerfile, GitHub Actions CI workflow, LICENSE (Apache 2.0), README, AGENTS.md, and golangci-lint config.

**Implementation details:**
- Run: `kubebuilder init --domain nutanix.io --repo github.com/nutanix-vma/nutanix-vma --project-name nutanix-vma`
- Go version: 1.22
- Add `kubevirt.io/api` and `kubevirt.io/containerized-data-importer-api` to `go.mod`
- Makefile must include targets: `build`, `test`, `test-integration`, `lint`, `generate`, `manifests`, `install`, `deploy`, `docker-build`, `docker-push`
- Dockerfile: multi-stage build, `gcr.io/distroless/static:nonroot` base, copy operator binary
- AGENTS.md: use the content from Section 6 "AGENTS.md Content" of this PRD
- GitHub Actions workflow (`.github/workflows/ci.yaml`): on push/PR, run `make lint`, `make build`, `make test`

**Acceptance Criteria:**
- `go build ./...` succeeds
- `make lint` passes
- `make test` runs (exits 0)
- GitHub Actions CI triggers on push
- Apache 2.0 LICENSE, README with build instructions, AGENTS.md present

### Story 2a: CRD Types -- Provider, NetworkMap, StorageMap
**Priority: P0** | **Depends on: Story 1**

Define CRD types for NutanixProvider, NetworkMap, and StorageMap in `api/v1alpha1/`. Include spec, status, and condition types. Run `make generate` and `make manifests`. Create sample CRs in `config/samples/`.

**Implementation details:**
- `provider_types.go`: NutanixProviderSpec (URL, SecretRef, RefreshInterval, InsecureSkipVerify), NutanixProviderStatus (Phase, VMCount, Conditions)
- `networkmap_types.go`: NetworkMapSpec (ProviderRef, Map []NetworkPair), each NetworkPair has Source (ID, Name) and Destination (Type: pod/multus, Name, Namespace)
- `storagemap_types.go`: StorageMapSpec (ProviderRef, Map []StoragePair), each StoragePair has Source (ID, Name) and Destination (StorageClass, VolumeMode, AccessMode)
- `groupversion_info.go`: register scheme for `vma.nutanix.io/v1alpha1`
- Sample CRs: `config/samples/vma_v1alpha1_nutanixprovider.yaml`, etc.

**Acceptance Criteria:**
- 3 CRD types defined with kubebuilder markers
- `make generate && make manifests` succeeds
- CRD YAML generated in `config/crd/bases/`
- Sample CRs in `config/samples/`
- `make build && make test` passes

### Story 2b: CRD Types -- MigrationPlan, Migration, Hook
**Priority: P0** | **Depends on: Story 2a**

Define CRD types for MigrationPlan, Migration, and Hook in `api/v1alpha1/`. These are more complex types with per-VM status tracking, pipeline phase tracking, and hook configuration.

**Implementation details:**
- `plan_types.go`: MigrationPlanSpec (ProviderRef, TargetNamespace, Type cold/warm, NetworkMapRef, StorageMapRef, VMs []PlanVM, MaxInFlight, TargetPowerState, WarmConfig), MigrationPlanStatus (Phase, VMs []VMValidationStatus with Concerns)
- `migration_types.go`: MigrationSpec (PlanRef, Cutover timestamp, Cancel []string), MigrationStatus (Phase, Started, Completed, VMs []VMPipelineStatus with Phase and Pipeline []PipelineStep)
- `hook_types.go`: HookSpec (Image, Playbook base64, Deadline, ServiceAccount), HookStatus (Conditions)
- Add sample CRs

**Acceptance Criteria:**
- 3 CRD types defined with kubebuilder markers
- `make generate && make manifests` succeeds, adding to existing CRD output
- Sample CRs for Plan, Migration, Hook
- `make build && make test` passes

### Story 3: Nutanix API Client -- Core + Auth
**Priority: P0** | **Depends on: Story 1**

Implement the `NutanixClient` interface in `internal/nutanix/client.go` with ALL method signatures. The concrete `httpClient` struct must implement ALL methods -- methods not yet implemented return `errors.New("not implemented: <MethodName>")`. Implement authentication, TLS config, base HTTP request/response handling, retry logic, and async task polling.

**Implementation details:**
- `client.go`: `NutanixClient` interface (all methods from Section 6), `ClientConfig` struct, `NewClient()` factory, `httpClient` struct with stub implementations
- `auth.go`: HTTP Basic Auth, custom TLS transport, InsecureSkipVerify
- `tasks.go`: `pollTask(ctx, taskUUID)` helper that polls `GET /api/prism/v4.0/config/tasks/{uuid}` until SUCCEEDED/FAILED
- Retry: exponential backoff on 429/5xx, configurable max retries
- Error types: `NutanixAPIError` with StatusCode, Message, ErrorCode

**Acceptance Criteria:**
- `NutanixClient` interface fully defined
- `NewClient(config)` returns working client
- Basic Auth, custom CA, InsecureSkipVerify work
- Task polling helper implemented
- All unimplemented methods return clear "not implemented" errors
- Unit tests with httptest mock for auth, retry, task polling

### Story 4: Nutanix API Client -- VM Operations
**Priority: P0** | **Depends on: Story 3**

Replace stub implementations for `ListVMs`, `GetVM`, `PowerOffVM`, `PowerOnVM`, `GetVMPowerState`. Define the Go struct types from Section 6 (VM, Disk, NIC, BootConfig, etc.) in `internal/nutanix/types.go`.

**Implementation details:**
- `types.go`: All Nutanix API response structs from Section 6 "Nutanix API Response Types"
- `vm.go`: ListVMs (paginated via `$top` and `$skip` query params), GetVM, PowerOffVM (returns task UUID -> poll), PowerOnVM (returns task UUID -> poll), GetVMPowerState
- v4 API pagination: response has `metadata.totalAvailableResults`, `metadata.offset`

**Acceptance Criteria:**
- VM list with pagination (handles >500 VMs)
- VM get returns full spec (disks, NICs, boot config, cluster reference)
- Power on/off calls task polling until complete
- Go structs match v4 API schema
- Unit tests for each method with httptest

### Story 5: Nutanix API Client -- Snapshots & Images
**Priority: P0** | **Depends on: Story 4**

Replace stub implementations for recovery point and image operations.

**Implementation details:**
- `snapshot.go`: CreateRecoveryPoint (POST -> task UUID -> poll -> return recovery point), GetRecoveryPoint, DeleteRecoveryPoint (POST delete -> task poll)
- `image.go`: CreateImageFromDisk (POST with data_source_reference -> task poll), GetImage, DownloadImage (GET .../file, stream to io.Writer without buffering entire image), DeleteImage

**Acceptance Criteria:**
- Recovery point CRUD with task polling
- Image creation from vDisk reference
- Image download streams to io.Writer
- Unit tests for each method

### Story 6: Nutanix API Client -- Networking, Storage & Cluster Discovery
**Priority: P0** | **Depends on: Story 4**

Replace stub implementations for subnet listing, storage container listing, and add cluster discovery for PE auto-detection.

**Implementation details:**
- `network.go`: ListSubnets (v4 networking API on PC)
- `storage.go`: ListStorageContainers (v2 API, takes PE URL as param)
- `cluster.go`: ListClusters (v4 clustermgmt API, returns cluster IPs for PE discovery)

**Acceptance Criteria:**
- Subnet list with UUID, name, VLAN ID
- Storage container list via PE URL
- Cluster list returns PE IPs for auto-discovery
- Unit tests for each method

### Story 7: Nutanix API Client -- CBT/CRT
**Priority: P0** | **Depends on: Story 5, Story 6**

Replace stub implementations for CBT operations. Handle the two-step flow, JWT token lifecycle, and pagination.

**Implementation details:**
- `cbt.go`: DiscoverClusterForCBT (POST to PC, returns CBTClusterInfo with PE URL + JWT), GetChangedRegions (GET to PE with JWT in cookie `NTNX_IGW_SESSION`, paginated)
- JWT refresh: if token age > 12 minutes (buffer before 15-min expiry), re-discover to get new token
- Pagination: loop until NextOffset is nil, accumulate all regions

**Acceptance Criteria:**
- Two-step flow: PC discovery -> PE query
- JWT token in cookie header
- Pagination with nextOffset
- Token refresh for long-running queries
- Zero-region identification
- Unit tests covering: single page, multi-page, token refresh, zero regions

### Story 8a: Mock Nutanix API Server -- Core
**Priority: P0** | **Depends on: Story 4, Story 6**

Build the mock server core: HTTP server, in-memory store, VM/subnet/storage/cluster/task endpoints. Usable as in-process test helper. Include default fixture data.

**Implementation details:**
- `pkg/mock/store.go`: In-memory store (VMs, subnets, containers, clusters, tasks, images, snapshots). Thread-safe with sync.RWMutex.
- `pkg/mock/server.go`: `NewServer()` returns `*httptest.Server` for tests, mux routing to handlers. `WithFixtures()` loads default data (3 VMs, 2 subnets, 1 container, 1 cluster).
- `pkg/mock/handlers_vm.go`: GET /api/vmm/v4.0/ahv/config/vms (list, paginated), GET .../vms/{id} (get), POST .../vms/{id}/$actions/power-off (returns task UUID)
- `pkg/mock/handlers_network.go`: GET /api/networking/v4.0/config/subnets
- `pkg/mock/handlers_storage.go`: GET /api/nutanix/v2.0/storage_containers
- `pkg/mock/handlers_task.go`: GET /api/prism/v4.0/config/tasks/{uuid} (simulates QUEUED->RUNNING->SUCCEEDED after 2 polls)
- `pkg/mock/handlers_cluster.go`: GET /api/clustermgmt/v4.0/config/clusters

**Acceptance Criteria:**
- In-process test helper with `mock.NewServer()`
- VM list/get/power-off endpoints work
- Subnet and storage container list endpoints work
- Task polling simulation works
- Default fixture data loads correctly
- Tests verify all endpoints return correct data

### Story 8b: Mock Nutanix API Server -- Snapshots, Images, CBT
**Priority: P0** | **Depends on: Story 8a, Story 7**

Add snapshot, image, and CBT handlers to the mock server. Add standalone binary mode.

**Implementation details:**
- `pkg/mock/handlers_snapshot.go`: POST /api/dataprotection/v4.0/config/recovery-points (create -> task), GET .../recovery-points/{id}, DELETE .../recovery-points/{id} (-> task)
- `pkg/mock/handlers_image.go`: POST /api/nutanix/v3/images (create -> task), GET .../images/{id}, GET .../images/{id}/file (return synthetic bytes of configurable size, support Range header), DELETE .../images/{id}
- `pkg/mock/handlers_cbt.go`: POST .../recovery-points/{id}/$actions/compute-changed-regions (return PE URL + JWT), GET .../recovery-points/{id}/changed-regions (return configurable changed regions, paginated)
- `cmd/mock-nutanix/main.go`: Standalone binary with `--port` flag, serves both PC and PE endpoints

**Acceptance Criteria:**
- Snapshot CRUD via mock
- Image create/download with configurable size + Range header support
- CBT two-step flow with JWT token validation
- CBT pagination with configurable regions
- Standalone binary runs with `go run cmd/mock-nutanix/main.go --port 9440`
- Tests verify snapshot/image/CBT flows

### Story 9: VM Builder -- Metadata Translation
**Priority: P0** | **Depends on: Story 2b, Story 4**

Implement the VM builder that translates Nutanix VM structs to KubeVirt VirtualMachine specs.

**Implementation details:**
- `internal/builder/vm.go`: `Build(vm *nutanix.VM, networkMap *v1alpha1.NetworkMap, storageMap *v1alpha1.StorageMap, opts BuildOptions) (*kubevirtv1.VirtualMachine, error)`
- `internal/builder/sanitize.go`: `SanitizeName(name string) string` -- lowercase, replace spaces/underscores/parens with hyphens, collapse multiples, trim, truncate to 63 chars, append `-N` on collision
- CPU: sockets -> sockets, cores -> cores, threads -> threads (default 1)
- Memory: bytes -> MiB
- Firmware: LEGACY -> bios:{}, UEFI -> efi:{}, SECURE_BOOT -> efi:{secureBoot:true}
- Disks: DISK entries only (skip CDROM). SCSI/PCI -> bus:scsi, IDE -> bus:sata, SATA -> bus:sata. Set bootOrder:1 on boot disk.
- NICs: per NetworkMap. pod -> masquerade. multus -> bridge + macAddress preserved.
- Machine: Q35 -> q35, PC -> pc
- Labels on both metadata and template.metadata: `vma.nutanix.io/source-uuid`, `vma.nutanix.io/migration`
- Annotations: `vma.nutanix.io/source-description`, `vma.nutanix.io/source-cluster`, `vma.nutanix.io/migrated-at`

**Acceptance Criteria:**
- Correct CPU topology preservation (not flattened)
- All firmware types mapped correctly
- CDROM entries skipped without error
- IDE disks mapped to sata bus
- MAC preserved on bridge, not on masquerade
- Machine type uses KubeVirt aliases (q35, pc)
- Name sanitization handles edge cases (spaces, unicode, >63 chars, collisions)
- Test fixtures: single-disk Linux VM, multi-disk Windows VM with CDROM, UEFI VM, multi-NIC VM, VM with IDE disk

### Story 10: Validation Engine
**Priority: P0** | **Depends on: Story 2b, Story 4**

Implement pre-flight validation.

**Implementation details:**
- `internal/validation/validator.go`: `Validate(vm *nutanix.VM, networkMap, storageMap, existingVMs) []Concern`
- `Concern` struct: Category (Error, Warning, Info), Message string
- Rules: unmapped storage container (Error), unmapped subnet (Error), GPU present (Warning), Volume Group present (Error), NETWORK_FUNCTION_NIC (Error), MAC conflict (Warning), target namespace missing (Error), IDE disk present (Info -- will be mapped to sata)

**Acceptance Criteria:**
- Each validation rule implemented and tested
- Concerns have clear human-readable messages
- Error-level concerns block migration, Warning/Info do not
- Unit tests for every rule + combination scenarios

### Story 11: Integration Test Harness + Provider Controller
**Priority: P0** | **Depends on: Story 2a, Story 3, Story 8a**

Set up the integration test harness (envtest + mock server + scheme registration) and implement the Provider controller.

**Implementation details:**
- `test/integration/suite_test.go`: envtest.Environment setup, register all CRD schemes (VMA + KubeVirt + CDI), start mock Nutanix server, cleanup in AfterSuite. Install CRD YAMLs from `config/crd/bases/`. NOTE: KubeVirt and CDI CRDs must be vendored or downloaded as test fixtures.
- `internal/controller/provider_controller.go`: Reconcile NutanixProvider CRs. Create client from spec + Secret. Fetch inventory (VMs, subnets, containers). Update status. Set conditions. Requeue after RefreshInterval. Add finalizer.
- `internal/controller/provider_controller_test.go`: Unit tests
- `test/integration/provider_test.go`: Create Provider CR + Secret -> verify status updated with VM count and Ready condition

**Acceptance Criteria:**
- envtest harness starts/stops cleanly
- Mock Nutanix server starts in-process
- Provider controller reconciles: connects, inventories, updates status
- Handles bad credentials (Error condition)
- Re-syncs on interval
- Finalizer prevents deletion while plans reference it
- Integration test passes

### Story 12: Plan Controller
**Priority: P0** | **Depends on: Story 10, Story 11**

Implement the Plan controller.

**Implementation details:**
- `internal/controller/plan_controller.go`: Reconcile MigrationPlan CRs. Resolve Provider/NetworkMap/StorageMap references. Get VM details from provider inventory. Run validation for each VM. Update per-VM status with concerns. Set plan phase.
- `test/integration/plan_test.go`: Create Plan with valid VMs -> Ready. Create Plan with GPU VM -> Ready with Warning. Create Plan with unmapped storage -> Error.

**Acceptance Criteria:**
- Resolves all references (Provider, NetworkMap, StorageMap)
- Validates each VM
- Per-VM status with concerns
- Phase transitions: Pending -> Validating -> Ready or Error
- Integration test with valid and invalid plans

### Story 13a: Migration Controller -- Pipeline Framework
**Priority: P0** | **Depends on: Story 12**

Implement the migration controller skeleton: state machine, phase tracking, concurrency management, and error/cancellation handling. No actual phase logic yet (phases are stubs that immediately succeed).

**Implementation details:**
- `internal/controller/migration_controller.go`: Reconcile Migration CRs. Create per-VM state machines. Phase enum (Pending, PreHook, StorePowerState, PowerOff, WaitForPowerOff, CreateSnapshot, ExportDisks, ImportDisks, CreateVM, StartVM, PostHook, Cleanup, Completed, Failed). Advance phases, update status. MaxInFlight scheduler. Cancellation (check cancel list before each phase). Finalizer. Error handler (set Failed, trigger cleanup).
- All phases return `PhaseResult{Completed: true}` (stubs)

**Acceptance Criteria:**
- State machine advances through all phases (stubs)
- Per-VM phase tracking in Migration status
- MaxInFlight limits concurrent VMs
- Cancellation stops and cleans up individual VMs
- Finalizer prevents deletion during running migration
- `make build && make test` passes

### Story 13b: Migration Controller -- Disk Transfer Phases
**Priority: P0** | **Depends on: Story 13a, Story 9**

Implement the disk transfer phases: PowerOff, WaitForPowerOff, CreateSnapshot, ExportDisks (create Nutanix image from snapshot), ImportDisks (create CDI DataVolume). Implement the transfer manager.

**Implementation details:**
- Replace PowerOff stub: call `client.PowerOffVM()`, store original power state
- Replace CreateSnapshot stub: call `client.CreateRecoveryPoint()`
- Replace ExportDisks stub: call `client.CreateImageFromDisk()` for each disk
- `internal/transfer/manager.go`: Transfer orchestration
- `internal/transfer/datavolume.go`: Create CDI DataVolume with HTTP source URL pointing to `https://<PC>/api/nutanix/v3/images/{uuid}/file`. Create CDI credential secret (accessKeyId/secretKey format). Set owner reference to Migration CR. Propagate volumeMode/accessMode from StorageMap.
- Replace ImportDisks stub: create DataVolumes, monitor DV progress (poll status), report percentage

**Acceptance Criteria:**
- PowerOff calls Nutanix API with task polling
- Snapshot created and UUID tracked
- Image created from snapshot vDisk
- CDI DataVolume created with correct HTTP source, credentials, storage config
- Owner reference on DataVolume -> Migration CR
- Credential secret created and cleaned up
- Progress percentage tracked from DV status
- On failure: restore source VM power state, clean up snapshots/images
- Unit tests for transfer manager and datavolume builder

### Story 13c: Migration Controller -- VM Creation & Cleanup
**Priority: P0** | **Depends on: Story 13b**

Implement the VM creation and cleanup phases. Wire in the VM builder from Story 9. Add full integration test.

**Implementation details:**
- Replace CreateVM stub: call `builder.Build()` with the Nutanix VM, maps, and options. Create the KubeVirt VirtualMachine CR. NO owner reference (VM outlives Migration).
- Replace StartVM stub: set `spec.running: true` on the VM (or leave as-is per targetPowerState)
- Replace Cleanup stub: delete temporary Nutanix images, delete Nutanix snapshots, delete credential secrets
- Failed phase: clean up DataVolumes, PVCs, snapshots, images, credential secrets. Restore source power state.
- `test/integration/migration_test.go`: Full cold migration test against mock + envtest. Verify: VM created with correct spec, DataVolumes created, status tracks phases.

**Acceptance Criteria:**
- KubeVirt VM created with correct metadata translation
- VM running state matches targetPowerState
- Cleanup deletes temporary Nutanix resources
- Failure cleanup is thorough (no leaked resources)
- Integration test: end-to-end cold migration against mock
- `make test-integration` passes

### Story 14: Migration Controller -- Warm Migration
**Priority: P1** | **Depends on: Story 13c, Story 7, Story 8b**

Extend the migration controller for warm migration.

**Implementation details:**
- Warm migration state machine: BulkCopy -> PrecopyLoop (CreateSnapshot -> ComputeCBT -> TransferDelta -> DeleteOldSnapshot -> repeat) -> Cutover (PowerOff -> FinalSnapshot -> FinalDelta -> CreateVM -> StartVM)
- `internal/transfer/delta.go`: Delta transfer pod spec. Pod image: `ghcr.io/nutanix-vma/vma-transfer:latest` (or use a busybox with dd for MVP). Mount target PVC. Write changed blocks at offsets using the Image Service download with Range headers.
- DataVolume created before BulkCopy (full disk size), not during cutover
- After CDI DataVolume succeeds (importer pod terminates), delta transfer pod can mount PVC
- Precopy interval configurable (default 30m)
- Cutover triggered by `spec.cutover` timestamp

**Acceptance Criteria:**
- Initial bulk copy via CDI DataVolume
- Precopy loop creates snapshots and computes CBT
- Delta transfer writes correct blocks at correct offsets
- Cutover completes with final delta
- Progress: precopy rounds, bytes transferred, last delta size
- Integration test with mock CBT data

### Story 15: Hook System
**Priority: P1** | **Depends on: Story 13a**

Implement hooks in the migration pipeline.

**Implementation details:**
- `internal/controller/hook_runner.go`: Create K8s Job from Hook CR spec. Create ConfigMap with migration context (VM details JSON, plan details JSON). Mount at /tmp/hook/. Watch Job status. Retry on failure (max 3).
- Wire PreHook and PostHook phases in migration state machine

**Acceptance Criteria:**
- Hook Job created with correct image, SA, deadline
- ConfigMap mounted with migration context
- Retry on failure (3 attempts)
- PreHook blocks pipeline
- PostHook runs after CreateVM
- Integration test with busybox hook image

### Story 16: kubectl-vma Plugin
**Priority: P2** | **Depends on: Story 12**

Implement the kubectl plugin.

**Implementation details:**
- `cmd/kubectl-vma/main.go`: cobra-based CLI
- Subcommands: `inventory` (read Provider status), `plan` (read Plan status), `migrate` (create Migration CR), `status` (read Migration status), `cancel` (patch Migration cancel list)
- Table output using `text/tabwriter`
- Uses dynamic client or typed client to read CRs

**Acceptance Criteria:**
- `go build -o kubectl-vma ./cmd/kubectl-vma/` produces binary
- inventory/plan/status commands format output correctly
- migrate command creates Migration CR
- cancel command patches cancel list
- Unit tests for output formatting

### Story 17: Observability
**Priority: P2** | **Depends on: Story 13c**

Add events, structured logging, and Prometheus metrics.

**Implementation details:**
- Events: use `record.EventRecorder` for MigrationStarted, PhaseTransition, MigrationCompleted, MigrationFailed
- Logging: use `logr` with structured fields (controller-runtime standard)
- Metrics: use `prometheus/client_golang` to register and serve metrics on /metrics

**Acceptance Criteria:**
- Events emitted for phase transitions
- Structured log fields: migration name, VM name/UUID, phase, duration
- Prometheus metrics registered and served
- Integration test verifies events are emitted after migration

### Story 18: E2E Test Framework
**Priority: P2** | **Depends on: Story 13c**

Build the E2E test suite for real infrastructure.

**Implementation details:**
- `test/e2e/suite_test.go`: Ginkgo suite, reads NUTANIX_PC_URL, NUTANIX_USERNAME, NUTANIX_PASSWORD, KUBEVIRT_KUBECONFIG from env. Skip if NUTANIX_E2E != "true".
- `test/e2e/cold_migration_test.go`: Find a test VM on Nutanix, create Provider/Maps/Plan/Migration, wait for completion, verify KubeVirt VM exists and boots.
- `.github/workflows/e2e.yaml`: Manual dispatch workflow with secrets for Nutanix credentials.
- Cleanup: delete migrated VMs, snapshots, images after each test.

**Acceptance Criteria:**
- Suite skips cleanly without NUTANIX_E2E=true
- Cold migration test case with real VM
- Warm migration test case
- Cleanup after each test
- GitHub Actions workflow for manual E2E runs

### Story 19: Documentation & Release
**Priority: P2** | **Depends on: Story 16**

Write documentation and set up release pipeline.

**Implementation details:**
- README.md: Overview, architecture diagram (ASCII), prerequisites (K8s + KubeVirt + CDI), installation (`make install && make deploy`), quickstart (create Provider -> Maps -> Plan -> Migration), CRD reference, configuration, troubleshooting
- CONTRIBUTING.md: Dev setup, testing, PR process
- `.github/workflows/release.yaml`: On tag push (v*), build image, push to ghcr.io, create GitHub Release with changelog

**Acceptance Criteria:**
- README covers full user journey
- CONTRIBUTING.md covers dev workflow
- Release workflow builds and pushes on tag
- `make build && make test` still passes
