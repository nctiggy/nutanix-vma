# Nutanix VMA -- VM Migration Appliance for KubeVirt

A Kubernetes-native operator that migrates virtual machines from **Nutanix AHV** to **KubeVirt**. Open source under Apache 2.0.

## Why This Exists

There is no tool for migrating Nutanix AHV VMs to KubeVirt. [KubeVirt Forklift](https://github.com/kubev2v/forklift) (Red Hat MTV) supports VMware vSphere, oVirt/RHV, OpenStack, OVA, EC2, and Hyper-V -- but Nutanix is completely absent from its codebase, community, and roadmap.

## The Key Insight

Nutanix AHV runs on **KVM/QEMU** -- the same hypervisor as KubeVirt. This eliminates the hardest part of VM migration:

| Concern | VMware -> KubeVirt | **AHV -> KubeVirt** |
|---------|-------------------|---------------------|
| Disk format | VMDK (proprietary) -> raw | **Raw -> raw (native!)** |
| Storage drivers | PVSCSI -> VirtIO (must inject) | **VirtIO -> VirtIO (already there)** |
| Network drivers | VMXNET3 -> VirtIO (must inject) | **VirtIO -> VirtIO (already there)** |
| Guest tools | Must remove VMware Tools | Remove NGT (simpler) |
| virt-v2v needed? | Yes (critical) | **No** |
| Transfer SDK | VDDK (proprietary, licensed) | **Standard HTTP API** |

No driver injection. No guest OS conversion. No virt-v2v. The migration pipeline is dramatically simpler.

## Architecture

```
                    +-------------------------------------+
                    |       nutanix-vma-operator           |
                    |                                      |
                    |  +-------------+  +---------------+  |
                    |  |  Inventory  |  |  Migration    |  |
                    |  |  Controller |  |  Controller   |  |
                    |  +------+------+  +------+--------+  |
                    |         |               |            |
                    |  +------+------+  +-----+----------+ |
                    |  | Prism Client|  |Transfer Manager | |
                    |  | (net/http)  |  |(CDI + CBT)      | |
                    |  +------+------+  +-----+----------+ |
                    +---------+---------------+-----------+
                              |               |
                   +----------+--+   +--------+--------+
                   |Prism Central|   | KubeVirt + CDI   |
                   | v3/v4 API   |   | (DataVolumes,    |
                   |             |   |  VirtualMachine) |
                   |Prism Element|   +------------------+
                   |(CBT/Storage)|
                   +-------------+
```

## Prerequisites

- Kubernetes cluster (v1.28+)
- [KubeVirt](https://kubevirt.io/) installed
- [CDI](https://github.com/kubevirt/containerized-data-importer) (Containerized Data Importer) installed
- Network connectivity from K8s worker nodes to Nutanix Prism Central (port 9440)
- `kubectl` configured with cluster access

## Installation

### Install CRDs

```bash
make install
```

### Deploy the Operator

```bash
# Build and push the image (replace with your registry)
make docker-build IMG=ghcr.io/nctiggy/nutanix-vma:latest
make docker-push IMG=ghcr.io/nctiggy/nutanix-vma:latest

# Deploy to the cluster
make deploy IMG=ghcr.io/nctiggy/nutanix-vma:latest
```

### Uninstall

```bash
make undeploy
make uninstall
```

## Quickstart

### 1. Create Nutanix Credentials

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: nutanix-creds
  namespace: default
stringData:
  username: admin
  password: your-prism-password
```

### 2. Create a NutanixProvider

```yaml
apiVersion: vma.nutanix.io/v1alpha1
kind: NutanixProvider
metadata:
  name: my-nutanix
  namespace: default
spec:
  url: https://prism-central.example.com:9440
  secretRef:
    name: nutanix-creds
  insecureSkipVerify: true  # Set false for production
  refreshInterval: "5m"
```

Wait for the provider to connect:

```bash
kubectl get nutanixproviders
# NAME         URL                                        PHASE       VMs   AGE
# my-nutanix   https://prism-central.example.com:9440     Connected   42    1m
```

### 3. Create Network and Storage Maps

```yaml
apiVersion: vma.nutanix.io/v1alpha1
kind: NetworkMap
metadata:
  name: my-network-map
  namespace: default
spec:
  providerRef:
    name: my-nutanix
  map:
    - source:
        name: "VM Network"         # Nutanix subnet name
      destination:
        type: pod                  # pod (masquerade) or multus (bridge)
---
apiVersion: vma.nutanix.io/v1alpha1
kind: StorageMap
metadata:
  name: my-storage-map
  namespace: default
spec:
  providerRef:
    name: my-nutanix
  map:
    - source:
        name: "default-container"  # Nutanix storage container name
      destination:
        storageClass: local-path   # Target Kubernetes StorageClass
        volumeMode: Filesystem
        accessMode: ReadWriteOnce
```

### 4. Create a Migration Plan

```yaml
apiVersion: vma.nutanix.io/v1alpha1
kind: MigrationPlan
metadata:
  name: my-plan
  namespace: default
spec:
  providerRef:
    name: my-nutanix
  targetNamespace: migrated-vms
  type: cold
  networkMapRef:
    name: my-network-map
  storageMapRef:
    name: my-storage-map
  vms:
    - id: "abc12345-6789-0000-0000-abcdef012345"  # Nutanix VM UUID
      name: "my-linux-vm"
  maxInFlight: 1
  targetPowerState: Running
```

Wait for validation:

```bash
kubectl get migrationplans
# NAME      PHASE   AGE
# my-plan   Ready   30s
```

### 5. Execute the Migration

```yaml
apiVersion: vma.nutanix.io/v1alpha1
kind: Migration
metadata:
  name: migrate-batch-1
  namespace: default
spec:
  planRef:
    name: my-plan
```

Monitor progress:

```bash
# Using kubectl-vma plugin
kubectl vma status migrate-batch-1

# Or directly
kubectl get migrations migrate-batch-1 -o yaml
```

### 6. Verify

```bash
kubectl get virtualmachines -n migrated-vms
# NAME           AGE   STATUS    READY
# my-linux-vm    2m    Running   True
```

## kubectl Plugin

Build and install:

```bash
go build -o kubectl-vma ./cmd/kubectl-vma/
sudo mv kubectl-vma /usr/local/bin/
```

Commands:

```bash
kubectl vma inventory my-nutanix          # List VMs from Nutanix
kubectl vma plan my-plan                  # Show plan validation status
kubectl vma migrate my-plan               # Create a Migration from a plan
kubectl vma status migrate-batch-1        # Show per-VM migration progress
kubectl vma cancel migrate-batch-1 VM-ID  # Cancel specific VMs
```

## CRD Reference

### NutanixProvider

Connects to a Nutanix Prism Central instance and discovers inventory.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.url` | string | Yes | Prism Central URL (e.g., `https://pc:9440`) |
| `spec.secretRef.name` | string | Yes | Secret with `username` and `password` keys |
| `spec.insecureSkipVerify` | bool | No | Skip TLS verification (default: `false`) |
| `spec.refreshInterval` | string | No | Inventory refresh interval (default: `5m`) |

**Status**: Phase (`Pending`, `Connecting`, `Connected`, `Error`), VMCount, Conditions.

### NetworkMap

Maps Nutanix subnets to KubeVirt networks.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.providerRef.name` | string | Yes | NutanixProvider reference |
| `spec.map[].source.id` | string | No | Nutanix subnet UUID |
| `spec.map[].source.name` | string | No | Nutanix subnet name |
| `spec.map[].destination.type` | string | Yes | `pod` (masquerade) or `multus` (bridge) |
| `spec.map[].destination.name` | string | No | Multus NAD name (required for `multus`) |
| `spec.map[].destination.namespace` | string | No | Multus NAD namespace |

### StorageMap

Maps Nutanix storage containers to Kubernetes StorageClasses.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.providerRef.name` | string | Yes | NutanixProvider reference |
| `spec.map[].source.id` | string | No | Nutanix storage container UUID |
| `spec.map[].source.name` | string | No | Nutanix storage container name |
| `spec.map[].destination.storageClass` | string | Yes | Target StorageClass |
| `spec.map[].destination.volumeMode` | string | No | `Filesystem` or `Block` (default: `Filesystem`) |
| `spec.map[].destination.accessMode` | string | No | `ReadWriteOnce`, `ReadWriteMany`, or `ReadOnlyMany` (default: `ReadWriteOnce`) |

### MigrationPlan

Defines which VMs to migrate and validates them before execution.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.providerRef.name` | string | Yes | NutanixProvider reference |
| `spec.targetNamespace` | string | Yes | Namespace for migrated KubeVirt VMs |
| `spec.type` | string | No | `cold` or `warm` (default: `cold`) |
| `spec.networkMapRef.name` | string | Yes | NetworkMap reference |
| `spec.storageMapRef.name` | string | Yes | StorageMap reference |
| `spec.vms[].id` | string | Yes | Nutanix VM UUID |
| `spec.vms[].name` | string | No | Display name |
| `spec.vms[].hooks[].hookRef` | object | No | Hook CR reference |
| `spec.vms[].hooks[].step` | string | No | `PreHook` or `PostHook` |
| `spec.maxInFlight` | int | No | Max concurrent VM migrations (default: `1`) |
| `spec.targetPowerState` | string | No | `Running` or `Stopped` (default: `Running`) |
| `spec.warmConfig.precopyInterval` | string | No | Warm precopy interval (default: `30m`) |
| `spec.warmConfig.maxPrecopyRounds` | int | No | Max precopy rounds (default: `10`) |

**Status**: Phase (`Pending`, `Validating`, `Ready`, `Error`), per-VM validation concerns.

### Migration

Executes a MigrationPlan. Each VM progresses through a pipeline of phases.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.planRef.name` | string | Yes | MigrationPlan reference |
| `spec.cutover` | timestamp | No | Cutover time for warm migrations |
| `spec.cancel[]` | string[] | No | VM UUIDs to cancel |

**Status**: Phase (`Pending`, `Running`, `Completed`, `Failed`, `Cancelled`), per-VM status with pipeline phase, timestamps, and resource tracking.

**Cold migration pipeline**: `StorePowerState` -> `PowerOff` -> `WaitForPowerOff` -> `CreateSnapshot` -> `ExportDisks` -> `ImportDisks` -> `CreateVM` -> `StartVM` -> `Cleanup` -> `Completed`

**Warm migration pipeline**: `BulkCopy` -> `WaitBulkCopy` -> `Precopy` (loop) -> `PowerOff` -> `WaitForPowerOff` -> `FinalSync` -> `CreateVM` -> `StartVM` -> `Cleanup` -> `Completed`

### Hook

Defines a pre or post migration action executed as a Kubernetes Job.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.image` | string | Yes | Container image for the hook Job |
| `spec.playbook` | string | No | Base64-encoded playbook (mounted at `/tmp/hook/`) |
| `spec.deadline` | string | No | Job deadline duration (e.g., `5m`) |
| `spec.serviceAccount` | string | No | ServiceAccount for the hook Job |

Migration context (VM details JSON, plan details JSON) is mounted at `/tmp/hook/` via a ConfigMap.

## Configuration

### Operator Flags

The operator binary accepts standard controller-runtime flags:

| Flag | Description |
|------|-------------|
| `--metrics-bind-address` | Address for Prometheus metrics (default: `:8080`) |
| `--health-probe-bind-address` | Address for health probes (default: `:8081`) |
| `--leader-elect` | Enable leader election for HA |

### Prometheus Metrics

The operator exposes metrics at `/metrics`:

| Metric | Type | Description |
|--------|------|-------------|
| `vma_migrations_total` | Counter | Total migrations by status (completed/failed/cancelled) |
| `vma_migration_duration_seconds` | Histogram | Per-VM migration duration |
| `vma_disk_transfer_bytes` | Counter | Total bytes transferred per VM |
| `vma_active_migrations` | Gauge | Currently active migrations |

### Kubernetes Events

The operator emits events on Migration CRs:

- `MigrationStarted` (Normal) -- Migration initiated
- `PhaseTransition` (Normal) -- VM advanced to a new phase
- `MigrationCompleted` (Normal) -- All VMs migrated successfully
- `MigrationFailed` (Warning) -- One or more VMs failed
- `MigrationCancelled` (Normal) -- All VMs were cancelled

## Migration Modes

### Cold Migration (Production-Ready)

Power off source VM -> snapshot -> export disks -> import via CDI -> create KubeVirt VM -> start.

### Warm Migration (Experimental)

Uses Nutanix v4 CBT (Changed Block Tracking) APIs for incremental sync while the source VM runs. Minimal downtime cutover.

> **Warning**: Warm migration depends on unverified assumptions about the Nutanix Image Service API (HTTP Range header support for partial disk reads). This feature requires real-cluster validation.

## VM Metadata Translation

The operator automatically maps Nutanix VM configuration to KubeVirt:

| Nutanix | KubeVirt | Notes |
|---------|----------|-------|
| `num_sockets` | `cpu.sockets` | Topology preserved |
| `num_vcpus_per_socket` | `cpu.cores` | Not flattened |
| `memory_size_mib` | `resources.requests.memory` | Direct map |
| `boot_type: UEFI` | `firmware.bootloader.efi` | BIOS/UEFI/SecureBoot |
| `adapter_type: SCSI` | `disk.bus: scsi` | Matches AHV virtio-scsi |
| `subnet_reference` | pod/masquerade or multus/bridge | Via NetworkMap |
| `machine_type: Q35` | `machine.type: q35` | KubeVirt alias |

## Pre-Flight Validation

Before migration starts, the operator validates each VM:

- All disks have mapped storage containers (Error)
- All NICs have mapped subnets (Error)
- Target StorageClasses and NetworkAttachmentDefinitions exist (Error)
- CDI and KubeVirt are installed (Error)
- Target namespace exists (Error)
- GPU passthrough VMs flagged (Warning -- requires manual target config)
- Volume Group VMs blocked (Error -- not supported)
- DIRECT_NIC / passthrough NICs flagged (Warning -- requires SR-IOV)
- NETWORK_FUNCTION_NIC blocked (Error -- not migrable)
- MAC address conflicts detected (Warning)
- IDE disk bus (Info -- mapped to SATA on KubeVirt)

## Troubleshooting

### Provider stuck in Connecting

```bash
kubectl describe nutanixprovider my-nutanix
```

Check the `Connected` condition message. Common causes:
- **Wrong URL**: Ensure the URL includes the port (`:9440`)
- **Bad credentials**: Verify the Secret `username`/`password` keys
- **TLS error**: Set `insecureSkipVerify: true` or provide a CA cert
- **Network unreachable**: Verify K8s nodes can reach Prism Central

### Plan stuck in Error

```bash
kubectl get migrationplan my-plan -o jsonpath='{.status.vMs}' | jq
```

Check per-VM validation concerns. Common errors:
- **Unmapped storage container**: Add the container to your StorageMap
- **Unmapped subnet**: Add the subnet to your NetworkMap
- **StorageClass not found**: Create the target StorageClass
- **CDI not installed**: Install CDI before creating plans

### Migration stuck in ImportDisks

DataVolumes are waiting for CDI to import disk images.

```bash
kubectl get datavolumes -n <target-namespace>
kubectl describe datavolume <name> -n <target-namespace>
```

Common issues:
- **CDI pods can't reach Prism**: Check network policies and firewall rules
- **Disk image too large**: Check PVC storage capacity and StorageClass provisioner
- **Credential error**: Verify the auto-created credential Secret has correct keys

### Migration Failed -- source VM still powered off

The operator attempts to restore the original power state on failure. If this fails:

```bash
# Check the VMMigrationStatus for the original power state
kubectl get migration <name> -o jsonpath='{.status.vMs[0].originalPowerState}'

# Manually power on via Nutanix Prism console if needed
```

### Cleanup leftover resources

If a migration is deleted before cleanup completes:

```bash
# Check for orphaned DataVolumes
kubectl get datavolumes -n <target-namespace>

# Check for orphaned credential Secrets
kubectl get secrets -n <target-namespace> -l app.kubernetes.io/managed-by=nutanix-vma

# Delete manually if needed
kubectl delete datavolume <name> -n <target-namespace>
```

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, testing, and PR process.

```bash
make build              # Build manager binary
make test               # Run unit + integration tests
make lint               # Run golangci-lint
make test-integration   # Run integration tests only
make test-e2e           # Run E2E tests (requires NUTANIX_E2E=true)
```

## Known Constraints

| Constraint | Status | Impact |
|-----------|--------|--------|
| Warm migration delta data source | Experimental | CBT API returns block metadata; Range header support unverified |
| Cold export fallback | Available | Clone-from-recovery-point path implemented as fallback |
| PE authentication | Uses PC credentials | May need separate PE credentials in some configurations |
| CDI network access | Deployment prereq | CDI pods must reach Prism Central port 9440 |

## License

Apache License 2.0. See [LICENSE](LICENSE).
