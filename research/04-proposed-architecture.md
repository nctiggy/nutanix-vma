# Proposed Architecture

## High-Level Design

```
                    ┌─────────────────────────────────────┐
                    │       nutanix-vma-operator           │
                    │                                      │
                    │  ┌─────────────┐  ┌──────────────┐  │
                    │  │  Inventory  │  │  Migration    │  │
                    │  │  Controller │  │  Controller   │  │
                    │  └──────┬──────┘  └──────┬───────┘  │
                    │         │                │           │
                    │  ┌──────┴──────┐  ┌──────┴───────┐  │
                    │  │   Prism     │  │  Transfer     │  │
                    │  │   Client    │  │  Manager      │  │
                    │  └──────┬──────┘  └──────┬───────┘  │
                    └─────────┼────────────────┼──────────┘
                              │                │
               ┌──────────────┼────────────────┼──────────────┐
               │              ▼                ▼              │
               │    ┌──────────────┐  ┌─────────────────┐    │
               │    │Prism Central │  │  CDI / Volume    │    │
               │    │  v3/v4 API   │  │  Populator       │    │
               │    └──────────────┘  └─────────────────┘    │
               │                                              │
               │    ┌──────────────┐  ┌─────────────────┐    │
               │    │Prism Element │  │  KubeVirt        │    │
               │    │(CBT/Storage) │  │  VirtualMachine  │    │
               │    └──────────────┘  └─────────────────┘    │
               └──────────────────────────────────────────────┘
```

## CRDs

### NutanixProvider

Connection to a Prism Central instance.

```yaml
apiVersion: vma.nutanix.io/v1alpha1
kind: NutanixProvider
metadata:
  name: my-nutanix
  namespace: nutanix-vma
spec:
  # Prism Central URL
  url: "https://prism-central.example.com:9440"

  # Secret containing credentials
  # Keys: username, password (basic auth)
  # Optional key: ca.crt (custom CA certificate)
  secretRef:
    name: nutanix-credentials
    namespace: nutanix-vma

  # How often to refresh inventory
  refreshInterval: 60s

  # Skip TLS verification (dev only)
  insecureSkipVerify: false

status:
  phase: Ready  # Connecting, Ready, Error
  lastRefresh: "2026-03-19T10:00:00Z"
  clusterCount: 3
  vmCount: 150
  conditions:
  - type: Connected
    status: "True"
    lastTransitionTime: "2026-03-19T09:55:00Z"
  - type: InventoryReady
    status: "True"
    lastTransitionTime: "2026-03-19T10:00:00Z"
```

### NetworkMap

Maps Nutanix subnets to KubeVirt networks.

```yaml
apiVersion: vma.nutanix.io/v1alpha1
kind: NetworkMap
metadata:
  name: my-network-map
  namespace: nutanix-vma
spec:
  provider:
    name: my-nutanix
  map:
  - source:
      id: "subnet-uuid-1"            # Nutanix subnet UUID
      name: "VLAN-100-Production"     # display name (informational)
    destination:
      type: pod                        # pod network (masquerade)
  - source:
      id: "subnet-uuid-2"
      name: "VLAN-200-Management"
    destination:
      type: multus                     # Multus secondary network
      name: mgmt-net                   # NetworkAttachmentDefinition name
      namespace: default
```

### StorageMap

Maps Nutanix storage containers to K8s StorageClasses.

```yaml
apiVersion: vma.nutanix.io/v1alpha1
kind: StorageMap
metadata:
  name: my-storage-map
  namespace: nutanix-vma
spec:
  provider:
    name: my-nutanix
  map:
  - source:
      id: "container-uuid-1"
      name: "default-container"
    destination:
      storageClass: ceph-block
      volumeMode: Block          # Block or Filesystem
      accessMode: ReadWriteOnce
  - source:
      id: "container-uuid-2"
      name: "ssd-container"
    destination:
      storageClass: local-ssd
      volumeMode: Filesystem
      accessMode: ReadWriteOnce
```

### MigrationPlan

Defines which VMs to migrate and how.

```yaml
apiVersion: vma.nutanix.io/v1alpha1
kind: MigrationPlan
metadata:
  name: migrate-web-tier
  namespace: nutanix-vma
spec:
  provider:
    name: my-nutanix
  targetNamespace: web-tier

  # Migration type
  type: cold  # cold, warm

  # Resource mappings
  networkMap:
    name: my-network-map
  storageMap:
    name: my-storage-map

  # VMs to migrate
  vms:
  - id: "vm-uuid-1"
    targetName: web-server-1        # optional override
    hooks:
    - step: PreMigration
      hook:
        name: remove-ngt
    - step: PostMigration
      hook:
        name: install-qemu-agent
  - id: "vm-uuid-2"
    targetName: web-server-2

  # Concurrency
  maxInFlight: 5

  # Power state after migration
  targetPowerState: "on"  # on, off, preserve

  # Whether to skip guest conversion (virt-v2v)
  # Default: true for AHV (not needed)
  skipGuestConversion: true

  # Whether to delete source VM after successful migration
  deleteSource: false

  # Warm migration settings (only if type: warm)
  warm:
    precopyInterval: 30m
    # Cutover is triggered manually or by schedule
    cutoverDeadline: "2026-03-20T02:00:00Z"

status:
  phase: Ready  # Pending, Validating, Ready, Error
  validationErrors: []
  vms:
  - id: "vm-uuid-1"
    name: "web-server-1"
    phase: Validated
    concerns: []  # warnings from validation
  - id: "vm-uuid-2"
    name: "web-server-2"
    phase: Validated
    concerns:
    - category: Warning
      message: "VM has GPU passthrough configured -- will not be migrated"
```

### Migration

Execution of a Plan. Created when user triggers migration.

```yaml
apiVersion: vma.nutanix.io/v1alpha1
kind: Migration
metadata:
  name: migrate-web-tier-run-1
  namespace: nutanix-vma
spec:
  plan:
    name: migrate-web-tier

  # For warm migration: set to trigger cutover
  cutover: "2026-03-20T02:00:00Z"

  # Cancel specific VMs
  cancel: []

status:
  phase: Running  # Pending, Running, Succeeded, Failed, Cancelled
  started: "2026-03-19T22:00:00Z"
  completed: null
  vms:
  - id: "vm-uuid-1"
    name: "web-server-1"
    phase: CopyingDisks
    pipeline:
    - name: PowerOff
      phase: Completed
      started: "2026-03-19T22:00:05Z"
      completed: "2026-03-19T22:00:15Z"
    - name: CreateSnapshot
      phase: Completed
      started: "2026-03-19T22:00:15Z"
      completed: "2026-03-19T22:00:17Z"
    - name: ExportDisks
      phase: Completed
      started: "2026-03-19T22:00:17Z"
      completed: "2026-03-19T22:01:30Z"
    - name: ImportDisks
      phase: Running
      started: "2026-03-19T22:01:30Z"
      progress: "45%"
    - name: CreateVM
      phase: Pending
    - name: StartVM
      phase: Pending
    error: null
  - id: "vm-uuid-2"
    name: "web-server-2"
    phase: Pending
```

### Hook

Pre/post migration actions.

```yaml
apiVersion: vma.nutanix.io/v1alpha1
kind: Hook
metadata:
  name: remove-ngt
  namespace: nutanix-vma
spec:
  # Container image to run
  image: "registry.example.com/migration-hooks:latest"

  # OR Ansible playbook (base64)
  # playbook: <base64-encoded-playbook>

  # Timeout
  deadline: 300  # seconds

  # Service account
  serviceAccount: hook-runner
```

## Operator Components

### 1. Inventory Controller

Watches `NutanixProvider` CRs. For each:
- Connects to Prism Central API
- Periodically fetches VMs, subnets, storage containers
- Caches in memory (or writes to CRD status / ConfigMap)
- Exposes inventory for Plan validation

### 2. Plan Controller

Watches `MigrationPlan` CRs. For each:
- Validates NetworkMap/StorageMap references
- Validates each VM against maps and compatibility rules
- Updates plan status with per-VM validation results

### 3. Migration Controller

Watches `Migration` CRs. For each:
- Creates a migration state machine per VM
- Executes pipeline phases (PowerOff -> Snapshot -> Export -> Import -> CreateVM -> Start)
- Tracks progress, handles errors, supports cancellation
- Manages concurrency (maxInFlight)
- For warm migrations: manages precopy loop and cutover trigger

### 4. Prism Client

Abstraction layer over Nutanix APIs:
- Auth (basic auth, token refresh)
- VM operations (list, get, power on/off)
- Snapshot operations (create, delete, list)
- Image operations (create from vDisk, download)
- CBT operations (cluster discovery, changed regions)
- Network operations (list subnets)
- Storage operations (list containers)

Consider building directly on `net/http` rather than using the auto-generated Go SDK (stability concerns).

### 5. Transfer Manager

Handles disk data transfer:
- Creates CDI DataVolumes for each disk
- Monitors DataVolume import progress
- For warm migration: creates transfer pods that use CBT to write deltas to PVCs
- Handles retry, cleanup on failure

### 6. VM Builder

Translates Nutanix VM metadata to KubeVirt VirtualMachine spec:
- CPU/memory mapping
- Firmware (BIOS/UEFI/SecureBoot)
- Disk mapping (per StorageMap)
- Network mapping (per NetworkMap)
- Labels/annotations (source UUID, migration ID)

## Migration State Machine

```
          ┌─────────┐
          │ Pending  │
          └────┬─────┘
               │
          ┌────▼─────┐
     ┌────│ PreHook   │ (if configured)
     │    └────┬──────┘
     │         │
     │    ┌────▼──────────┐
     │    │ StorePowerState│
     │    └────┬──────────┘
     │         │
     │    ┌────▼─────┐
     │    │ PowerOff  │
     │    └────┬──────┘
     │         │
     │    ┌────▼──────────┐
     │    │CreateSnapshot  │
     │    └────┬──────────┘
     │         │
     │    ┌────▼──────────┐
     │    │ ExportDisks    │
     │    └────┬──────────┘
     │         │
     │    ┌────▼──────────┐
     │    │ ImportDisks    │ (CDI DataVolume)
     │    └────┬──────────┘
     │         │
     │    ┌────▼──────────┐
     │    │ CreateVM       │ (KubeVirt VirtualMachine CR)
     │    └────┬──────────┘
     │         │
     │    ┌────▼─────┐
     │    │ StartVM   │
     │    └────┬──────┘
     │         │
     │    ┌────▼─────┐
     │    │ PostHook  │ (if configured)
     │    └────┬──────┘
     │         │
     │    ┌────▼──────────┐
     │    │ Cleanup        │ (snapshots, temp images)
     │    └────┬──────────┘
     │         │
     │    ┌────▼──────┐
     └───►│ Completed  │
          └───────────┘
               │
     (on any error)
               │
          ┌────▼─────┐
          │  Failed   │ -> cleanup partial resources
          └──────────┘
```

### Warm Migration State Machine (Additional States)

```
  ... after CreateSnapshot (S1) ...
       │
  ┌────▼──────────┐
  │ BulkCopy       │ (full disk copy from S1)
  └────┬──────────┘
       │
  ┌────▼──────────────────┐
  │ PrecopyLoop           │ ◄──────────────┐
  │  1. CreateSnapshot(Sn)│                │
  │  2. ComputeCBT(Sn-1,Sn)               │
  │  3. TransferDelta     │                │
  │  4. DeleteSnapshot(Sn-1)              │
  └────┬──────────────────┘                │
       │                                   │
       ├─ (cutover not triggered) ─────────┘
       │
       ├─ (cutover triggered)
       │
  ┌────▼──────────┐
  │ PowerOff       │
  └────┬──────────┘
       │
  ┌────▼──────────────┐
  │ FinalSnapshot      │
  └────┬──────────────┘
       │
  ┌────▼──────────────┐
  │ FinalDelta         │ (should be small)
  └────┬──────────────┘
       │
  ┌────▼──────────────┐
  │ CreateVM + StartVM │
  └────┬──────────────┘
       │
  ┌────▼──────────┐
  │ Completed      │
  └───────────────┘
```

## Technology Stack

| Component | Technology | Why |
|-----------|-----------|-----|
| Language | Go | K8s operator standard, Nutanix Go SDK available |
| Operator Framework | controller-runtime (kubebuilder) | Standard, well-documented |
| Nutanix API Client | Custom HTTP client (wrapping v3/v4) | SDK stability concerns |
| Disk Transfer | CDI DataVolumes (HTTP source) | Already in KubeVirt ecosystem |
| Warm Transfer | Custom transfer pods + CBT API | No existing tooling |
| Validation | OPA/Rego (optional) or inline Go | Forklift pattern, or simpler inline |
| CLI | kubectl plugin (optional) | `kubectl vma migrate` UX |
| UI | Web dashboard (optional, later) | Inventory browser, progress tracking |

## Directory Structure (Proposed)

```
nutanix-vma/
├── cmd/
│   ├── operator/          # Main operator binary
│   └── kubectl-vma/       # kubectl plugin (optional)
├── api/
│   └── v1alpha1/          # CRD types
│       ├── provider_types.go
│       ├── networkmap_types.go
│       ├── storagemap_types.go
│       ├── plan_types.go
│       ├── migration_types.go
│       └── hook_types.go
├── internal/
│   ├── controller/
│   │   ├── provider/      # Inventory controller
│   │   ├── plan/          # Plan validation controller
│   │   └── migration/     # Migration execution controller
│   ├── nutanix/
│   │   ├── client.go      # Prism API client
│   │   ├── vm.go          # VM operations
│   │   ├── snapshot.go    # Snapshot/recovery point operations
│   │   ├── image.go       # Image service operations
│   │   ├── cbt.go         # CBT/CRT operations
│   │   ├── network.go     # Subnet operations
│   │   └── storage.go     # Container operations
│   ├── transfer/
│   │   ├── manager.go     # Transfer orchestration
│   │   ├── datavolume.go  # CDI DataVolume creation
│   │   └── delta.go       # CBT delta transfer
│   ├── builder/
│   │   └── vm.go          # Nutanix VM -> KubeVirt VM translation
│   └── validation/
│       └── validator.go   # Pre-flight validation
├── config/
│   ├── crd/               # Generated CRD manifests
│   ├── rbac/              # RBAC roles
│   ├── manager/           # Operator deployment
│   └── samples/           # Example CRs
├── hack/                  # Build/dev scripts
├── test/
│   ├── e2e/               # End-to-end tests
│   └── mock/              # Mock Prism API server
├── research/              # This research documentation
├── Dockerfile
├── Makefile
├── go.mod
└── go.sum
```
