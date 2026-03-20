# Forklift Architecture Deep Dive

## Overview

Forklift (upstream of Red Hat MTV -- Migration Toolkit for Virtualization) is a Kubernetes-native toolkit for migrating VMs at scale to KubeVirt. Lives at `github.com/kubev2v/forklift`.

## Deployed Components

| Component | Image | Role |
|-----------|-------|------|
| **forklift-operator** | `quay.io/kubev2v/forklift-operator` | Ansible-based OLM operator, deploys all components |
| **forklift-controller** | `quay.io/kubev2v/forklift-controller` | Core controller: reconcilers for Provider, Plan, Migration, Map, Hook, Host, Validation. Embeds inventory API (SQLite) + REST server |
| **forklift-validation** | `quay.io/kubev2v/forklift-validation` | OPA/Rego policy engine. Evaluates rules per VM to produce "concerns" (advisories/warnings) |
| **forklift-console-plugin** | `quay.io/kubev2v/forklift-console-plugin` | OpenShift Console dynamic plugin (migration UI) |
| **forklift-virt-v2v** | `quay.io/kubev2v/forklift-virt-v2v` | Container with virt-v2v / virt-v2v-in-place for guest conversion |
| **populator-controller** | `quay.io/kubev2v/populator-controller` | Volume populator controller for storage-offload transfers |

## CRDs (API group: `forklift.konveyor.io`)

| CRD | Purpose |
|-----|---------|
| `providers` | Source or destination virtualization platform |
| `plans` | Migration plan -- VMs to migrate, resource mappings |
| `migrations` | Running/completed execution of a Plan |
| `networkmaps` | Source network -> KubeVirt network mapping |
| `storagemaps` | Source datastore -> K8s StorageClass mapping |
| `hooks` | Pre/post migration hook definitions |
| `hosts` | ESXi host references (vSphere-specific) |
| `forkliftcontrollers` | Operator CR for installation config |
| `ovirtvolumepopulators` | oVirt volume populator CR |
| `openstackvolumepopulators` | OpenStack volume populator CR |
| `vspherexcopyvolumepopulators` | vSphere XCOPY storage-offload populator CR |

## Controllers (Reconcilers)

All in `pkg/controller/`:

- **provider** -- Reconciles `Provider` CRs. Builds a `Collector` per provider that connects to source API, fetches full inventory (VMs, networks, datastores, clusters, hosts), stores in SQLite. Exposes inventory via REST.
- **plan** -- Reconciles `Plan` CRs. Validates storage/network mappings, VM compatibility. When a `Migration` references the plan, orchestrates the full pipeline.
- **migration** -- Reconciles `Migration` CRs. Links execution to a Plan, tracks per-VM status.
- **map** (NetworkMap, StorageMap) -- Validates mapping references.
- **hook** -- Validates Hook CRs.
- **host** -- Manages ESXi Host CRs.
- **validation** -- Calls OPA service for each VM.

## Migration Workflow (Cold, VMware vSphere)

Driven by an **itinerary** -- a sequence of phases in `pkg/controller/plan/migrator/base/migrator.go`:

```
Started
  -> PreHook (if configured)
  -> StorePowerState
  -> PowerOffSource
  -> WaitForPowerOff
  -> CreateDataVolumes
  -> AllocateDisks (if virt-v2v direct)
  -> CopyDisks (if CDI DataVolumes)
  -> CreateGuestConversionPod (if conversion needed)
  -> ConvertGuest
  -> CopyDisksVirtV2V
  -> CreateVM
  -> PostHook (if configured)
  -> Completed
```

### Phase Details

1. **Started** -- Init VM status, clean up leftovers, validate target name (DNS1123).
2. **PreHook** -- Run user-defined hook (Ansible playbook or custom container) as K8s Job.
3. **StorePowerState** -- Record source VM power state for possible restoration.
4. **PowerOffSource** -- Power off via provider API (govmomi for vSphere).
5. **WaitForPowerOff** -- Poll until confirmed powered off.
6. **CreateDataVolumes** -- Create CDI DataVolumes (or populator PVCs) for each disk.
7. **AllocateDisks / CopyDisks** -- Transfer disk data. For cold VMware local: virt-v2v handles copy+convert in one step. For warm/remote: CDI DataVolumes with VDDK.
8. **CreateGuestConversionPod** -- Pod running `forklift-virt-v2v` image. Mounts target PVCs, runs virt-v2v. Exposes HTTP on :8080 serving `/vm` (config), `/inspection` (OS detection), `/warnings`.
9. **ConvertGuest / CopyDisksVirtV2V** -- Monitor virt-v2v progress. Fetch converted config from pod HTTP API.
10. **CreateVM** -- Create KubeVirt `VirtualMachine` CR. Attach PVCs, set CPU/memory/NICs/firmware from source + virt-v2v output. Clean up DataVolumes.
11. **PostHook** -- Run user-defined post hook.
12. **Completed** -- Mark succeeded or failed.

### Decision: virt-v2v Direct vs CDI + virt-v2v-in-place

`ShouldUseV2vForTransfer()` determines approach:
- **virt-v2v direct** (single-step copy+convert): vSphere + cold + local destination + shared disks + guest conversion enabled
- **CDI DataVolumes + virt-v2v-in-place** (two-step): warm migrations, remote destinations, OVA, selective shared disks

## Warm Migration (vSphere/oVirt Only)

Uses Changed Block Tracking (CBT):

```
Started -> PreHook
  -> CreateInitialSnapshot -> WaitForInitialSnapshot -> StoreInitialSnapshotDeltas
  -> PreflightInspection
  -> CreateDataVolumes
  -> [LOOP: CopyDisks -> CopyingPaused -> RemovePreviousSnapshot
     -> CreateSnapshot -> WaitForSnapshot -> StoreSnapshotDeltas -> AddCheckpoint]
  -> StorePowerState -> PowerOffSource -> WaitForPowerOff
  -> RemovePenultimateSnapshot
  -> CreateFinalSnapshot -> AddFinalCheckpoint
  -> Finalize
  -> RemoveFinalSnapshot
  -> CreateGuestConversionPod -> ConvertGuest
  -> CreateVM -> PostHook -> Completed
```

Precopy interval default: 60 minutes (`PRECOPY_INTERVAL`).

## Provider Model

### Supported Types (from `pkg/apis/forklift/v1beta1/provider.go`)

```go
OpenShift ProviderType = "openshift"
VSphere   ProviderType = "vsphere"
OVirt     ProviderType = "ovirt"
OpenStack ProviderType = "openstack"
Ova       ProviderType = "ova"
EC2       ProviderType = "ec2"
HyperV    ProviderType = "hyperv"
```

### Provider CRD

```go
type ProviderSpec struct {
    Type     *ProviderType
    URL      string
    Secret   core.ObjectReference
    Settings map[string]string  // free-form provider-specific config
}
```

### Core Interfaces (pkg/controller/plan/adapter/base/doc.go)

**Adapter** (factory):
```go
type Adapter interface {
    Builder(ctx *plancontext.Context) (Builder, error)
    Client(ctx *plancontext.Context) (Client, error)
    Validator(ctx *plancontext.Context) (Validator, error)
    DestinationClient(ctx *plancontext.Context) (DestinationClient, error)
    Ensurer(ctx *plancontext.Context) (Ensurer, error)
}
```

**Builder** -- Builds K8s objects: `Secret()`, `ConfigMap()`, `VirtualMachine()`, `DataVolumes()`, `Tasks()`, `PopulatorVolumes()`, `PodEnvironment()`, `ConversionPodConfig()`, `PreferenceName()` (~27 methods).

**Client** -- Source API interaction: `PowerOn()`, `PowerOff()`, `PowerState()`, `CreateSnapshot()`, `RemoveSnapshot()`, `CheckSnapshotReady()`, `SetCheckpoints()`, `DetachDisks()`, `Finalize()`, `PreTransferActions()`, `GetSnapshotDeltas()`.

**Validator** -- VM compatibility: `StorageMapped()`, `NetworksMapped()`, `WarmMigration()`, `MigrationType()`, `ChangeTrackingEnabled()`, `SharedDisks()`, `MacConflicts()`, `PowerState()`, `GuestToolsInstalled()`, `InvalidDiskSizes()`.

**DestinationClient** -- Destination-side: populator data sources, CR ownership.

**Ensurer** -- Shared ConfigMaps/Secrets.

### Inventory System

Poll-and-cache pattern:
1. **Collector** goroutine connects to source API, full-loads all objects into SQLite
2. Periodic refresh (e.g., 10s) re-fetches, computes deltas, applies transactionally
3. **Model adapters** translate raw API responses to internal structs
4. **Web handlers** expose REST endpoints for querying inventory
5. **Watch handlers** monitor DB changes, trigger reconciliation of Maps and Plans

## Network Mapping

```go
type NetworkMapSpec struct {
    Provider provider.Pair
    Map      []NetworkPair
}

type NetworkPair struct {
    Source      ref.Ref
    Destination DestinationNetwork  // type: "pod", "multus", or "ignored"
}
```

Supports: pod network, Multus (NetworkAttachmentDefinition), User-Defined Networks (OVN-K), static IP preservation, transfer network separation.

## Storage Mapping

```go
type StorageMapSpec struct {
    Provider provider.Pair
    Map      []StoragePair
}

type StoragePair struct {
    Source        ref.Ref
    Destination   DestinationStorage  // StorageClass, VolumeMode, AccessMode
    OffloadPlugin *OffloadPlugin      // XCOPY for enterprise storage
}
```

Supports: XCOPY storage offload for FlashSystem, ONTAP, Pure, PowerFlex, etc.

## Hooks

```go
type HookSpec struct {
    ServiceAccount string
    Image          string
    Playbook       string  // base64-encoded Ansible playbook
    Deadline       int64   // seconds
}
```

HookRunner creates a K8s Job with ConfigMap containing workload YAML, plan YAML, and decoded playbook. Runs `ansible-runner` if playbook provided. Retry limit: 3 (configurable via `HOOK_RETRY`).

## Error Handling and Retry

| Setting | Default | Description |
|---------|---------|-------------|
| `MAX_VM_INFLIGHT` | 20 | Max concurrent VM migrations |
| `HOOK_RETRY` | 3 | Hook Job retry limit |
| `IMPORTER_RETRY` | 3 | CDI importer retry limit |
| `CLEANUP_RETRIES` | 10 | Resource cleanup retries |
| `SNAPSHOT_REMOVAL_TIMEOUT` | 120 min | Snapshot removal timeout |
| `MAX_CONCURRENT_RECONCILES` | 10 | Controller concurrency |

- One VM failure does not block others
- Cancellation: add VM refs to `migration.spec.cancel[]`
- Plan controller reconciles on 3-second poll interval

## Data Transfer by Provider

| Provider | Transfer Method | CDI Source | virt-v2v Mode |
|----------|----------------|-----------|---------------|
| vSphere | VDDK | `DataVolumeSourceVDDK` | `-i libvirt -ic vpx://... -it vddk` |
| oVirt | ImageIO API | `DataVolumeSourceImageIO` | In-place on pre-populated disks |
| OpenStack | Volume populator | Blank DV + populator | In-place |
| OVA | NFS mount + virt-v2v | Blank DV | `-i ova <path>` |
| HyperV | SMB mount + virt-v2v | Blank DV | `-i disk <paths>` |
| EC2 | EBS snapshot attach | Blank DV | In-place |

## Not Plugin-Based

Adding a provider requires modifying **~12 hardcoded factory/dispatch files**:

1. `pkg/apis/forklift/v1beta1/provider.go` -- ProviderType constant
2. `pkg/controller/plan/adapter/doc.go` -- `New()` factory
3. `pkg/controller/provider/container/doc.go` -- `Build()` factory
4. `pkg/controller/provider/model/doc.go` -- `Models()` factory
5. `pkg/controller/provider/web/doc.go` -- `All()` handlers
6. `pkg/controller/plan/handler/doc.go` -- `New()` factory
7. `pkg/controller/plan/scheduler/doc.go` -- `New()` factory
8. `pkg/controller/map/network/handler/doc.go` -- case
9. `pkg/controller/map/storage/handler/doc.go` -- case
10. `pkg/controller/host/handler/doc.go` -- case
11. `pkg/virt-v2v/config/variables.go` -- source constant
12. `pkg/virt-v2v/conversion/conversion.go` -- args builder

Plus ~15-20 new files for the provider implementation across plan adapter, inventory collector/model/web, plan handler, scheduler, map handlers, and validation policies.

## Sources

- [kubev2v/forklift GitHub](https://github.com/kubev2v/forklift)
- [How To Migrate VMs to KubeVirt With Forklift -- The New Stack](https://thenewstack.io/how-to-migrate-your-vms-to-kubevirt-with-forklift/)
- [VM migration with virt-v2v, Forklift and KubeVirt -- Spectro Cloud](https://www.spectrocloud.com/blog/how-to-migrate-your-vms-to-kubevirt-with-forklift)
- [Red Hat MTV 2.7 Documentation](https://docs.redhat.com/en/documentation/migration_toolkit_for_virtualization/2.7/)
- [kubectl-mtv CLI plugin](https://github.com/yaacov/kubectl-mtv)
