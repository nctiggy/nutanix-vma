# Nutanix AHV Internals

## VM Disk Format

### Internal Storage

All VM disks are **raw flat files** within the Nutanix Distributed Storage Fabric (DSF):

```
/<container-name>/.acropolis/vmdisk/<vDisk-UUID>
```

Example:
```
/ctr01/.acropolis/vmdisk/958a3c70-7d99-4706-bee5-35cb70339ce0
```

Snapshots stored at:
```
/<container-name>/.acropolis/snapshot/<snapshot-UUID>/vmdisk/
```

### Disk Presentation to VMs

Disks are presented as **raw SCSI block devices** via **virtio-scsi** (preferred, default). IDE supported but not recommended. Windows requires VirtIO drivers (or NGT) for virtio-scsi.

### Image Service Import Formats

**raw, QCOW2, VHD, VHDX, VMDK, VDI, OVA, ISO** -- all converted internally to raw on import.

### Export

From a CVM using `qemu-img`:

```bash
# Export as QCOW2 (compressed, thin)
qemu-img convert -f raw -O qcow2 -c \
  nfs://<cvm_ip>/<container>/.acropolis/vmdisk/<vmdisk_uuid> \
  nfs://<cvm_ip>/<container>/<output_name>.qcow2

# Export as VMDK
qemu-img convert -f raw -O vmdk \
  nfs://<cvm_ip>/<container>/.acropolis/vmdisk/<vmdisk_uuid> \
  nfs://<cvm_ip>/<container>/<output_name>.vmdk
```

Exports are thin-provisioned -- a 100GB disk with 10GB data exports as ~10GB.

## Storage Architecture (DSF)

### Overview

Distributed Storage Fabric aggregates local storage from every node into a single software-defined storage pool. Each node runs a **Controller VM (CVM)** with the **Stargate** process handling all I/O.

### Storage Hierarchy

| Component | Description | Size |
|-----------|-------------|------|
| **Storage Pool** | Physical devices (PCIe SSD, SSD, HDD) across all nodes | Cluster-wide |
| **Container** | Logical segmentation of storage pool | Logical |
| **vDisk** | Any file >512KB on AOS (includes VM disks) | Variable |
| **vBlock** | 1MB chunk of virtual address space | 1MB |
| **Extent** | 1MB piece of logically contiguous data | 1MB |
| **Extent Group** | 1-4MB piece of physically contiguous data on disk | 1-4MB |

### I/O Path

**Writes:**
1. Write arrives at local Stargate (CVM)
2. Bursty random (<1.5MB): written to **OpLog** (SSD-backed WAL), sync-replicated to another CVM, acknowledged
3. Sequential/sustained (>1.5MB): bypass OpLog, direct to **Extent Store**
4. OpLog drained to Extent Store asynchronously

**Reads:**
1. Request goes to local Stargate
2. Served from **Unified Cache** (in-memory, 4KB granularity) if cached
3. Otherwise from Extent Store (local SSD/HDD)

### AHV I/O Path

- **Standard**: VM OS -> virtio-scsi -> QEMU -> libiscsi -> local CVM Stargate
- **Frodo / AHV Turbo Mode** (default since AOS 5.5.X): Replaces single-threaded QEMU main loop with vhost-user-scsi. One virtual queue per vCPU. 25-75% CPU reduction, up to 3x throughput. Requires >=2 vCPUs.
- **iSCSI multi-pathing**: Primary path to local CVM; auto-failover to remote Stargate if local CVM fails

### Data Locality

- Cache locality: remote data pulled to local Unified Cache at 4KB granularity
- Extent locality: after 3 random / 10 sequential touches in 10min, extent groups migrate to local storage
- VM live-migration: new local CVM serves I/O immediately, data gradually follows

### Replication Factor

- **RF2**: 2 copies, tolerates 1 node failure
- **RF3**: 3 copies, tolerates 2 node failures
- Paxos algorithm: majority must agree before commit

## Nutanix APIs

### API Versions

| Version | Scope | Base URL | Status |
|---------|-------|----------|--------|
| **v4** (recommended) | Prism Central only | `https://<PC>:9440/api/<namespace>/v4.x/<path>` | GA (pc.2024.3+) |
| **v3** (legacy) | Prism Central only | `https://<PC>:9440/api/nutanix/v3/<path>` | Will deprecate Q4 FY26 |
| **v2.0** (legacy) | Prism Element only | `https://<PE>:9440/api/nutanix/v2.0/<path>` | Will deprecate |

Auth: HTTP Basic Auth or IAM API Key (v4).

### Key v4 Endpoints

```
# VMs (vmm namespace)
GET  /api/vmm/v4.0/ahv/config/vms              # List VMs
GET  /api/vmm/v4.0/ahv/config/vms/{uuid}        # Get VM
POST /api/vmm/v4.0/ahv/config/vms/{uuid}/$actions/power-off

# Images (vmm namespace)
GET  /api/vmm/v4.0/content/images               # List images
POST /api/vmm/v4.0/content/images               # Create image

# Snapshots (dataprotection namespace)
POST /api/dataprotection/v4.0/config/recovery-points           # Create snapshot
GET  /api/dataprotection/v4.0/config/recovery-points/{uuid}    # Get snapshot
POST /api/dataprotection/v4.0/config/recovery-points/{uuid}/$actions/replicate
POST /api/dataprotection/v4.0/config/recovery-points/{uuid}/$actions/restore
```

### CBT/CRT APIs (v4 dataprotection)

Two-step process for changed block tracking:

**Step 1 -- Cluster Discovery (POST to Prism Central)**:
Returns PE cluster IP + JWT token (15-min validity).
Request body specifies `COMPUTE_CHANGED_REGIONS` operation with base and reference recovery point UUIDs.

**Step 2 -- Compute Changed Regions (GET to Prism Element)**:
Returns list of changed regions between two recovery points.
- Uses JWT token: `NTNX_IGW_SESSION=<token>`
- Paginated: up to 10,000 regions per call via `nextOffset`
- Returns zero-region indicators for optimization

### Key v3 Endpoints

```
# VMs
POST /api/nutanix/v3/vms/list     # List VMs (POST with filter body)
GET  /api/nutanix/v3/vms/{uuid}   # Get VM

# Images
POST /api/nutanix/v3/images/list          # List images
GET  /api/nutanix/v3/images/{uuid}/file   # Download image (binary)
PUT  /api/nutanix/v3/images/{uuid}/file   # Upload image

# Recovery Points
POST /api/nutanix/v3/vm_recovery_points/{uuid}/restore
```

v3 uses intent-based model (spec vs. status pattern).

### Key v2 Endpoints (Prism Element only)

```
GET  /api/nutanix/v2.0/vms                              # List VMs
GET  /api/nutanix/v2.0/snapshots                         # List snapshots
POST /api/nutanix/v2.0/snapshots                         # Create snapshot
GET  /api/nutanix/v2.0/storage-containers                # List containers
GET  /api/nutanix/v2.0/protection_domains/{pd}/dr_snapshots
```

### REST API Explorer

Built-in at: `https://<prism_ip>/api/nutanix/v3/api_explorer/`

## VM Metadata

### Compute
- `num_sockets`, `num_vcpus_per_socket`, `num_threads_per_core`
- `memory_size_mib`
- `enable_cpu_passthrough`, `is_vcpu_hard_pinned`
- `machine_type` -- PC (default) or Q35

### Boot
- `boot_type` -- `LEGACY` (BIOS), `UEFI`, `SECURE_BOOT`
- `boot_device_order_list` -- CDROM, DISK, NETWORK
- `boot_device_mac_address` -- for PXE

### Storage (disk_list)
- `disk_size_bytes` / `disk_size_mib`
- `device_type` -- DISK or CDROM
- `adapter_type` -- SCSI (virtio-scsi), IDE, PCI, SATA
- `storage_container_reference`
- `data_source_reference` -- clone from image/snapshot
- `volume_group_reference`

### Networking (nic_list)
- `subnet_reference`
- `ip_endpoint_list` -- static IPs
- `mac_address`
- `nic_type` -- NORMAL_NIC, DIRECT_NIC, NETWORK_FUNCTION_NIC
- `model` -- virtio (default)

### GPU (gpu_list)
- `mode` -- PASSTHROUGH_GRAPHICS, PASSTHROUGH_COMPUTE, VIRTUAL (vGPU)
- `vendor` -- NVIDIA, AMD, Intel
- `device_id`

### Other
- `name`, `description`, `categories`
- `cluster_reference`, `availability_zone_reference`
- `nutanix_guest_tools` -- NGT status
- `power_state` -- ON, OFF
- `guest_os_id`
- `serial_port_list`, `vga_console_enabled`

## Image Service

- Import: raw, QCOW2, VHD, VHDX, VMDK, VDI, OVA, ISO
- Import sources: HTTP/HTTPS URL, NFS URL, direct upload
- PC uploads go to SelfServiceContainer (no container selection)
- For specific container placement, upload via PE

### Create image from vDisk (aCLI on CVM):
```bash
acli image.create <name> \
  source_url=nfs://127.0.0.1/<container>/.acropolis/vmdisk/<uuid> \
  container=<target_container> image_type=kDiskImage
```

### Download image via API:
```bash
curl -k -u admin:password -X GET \
  "https://<cluster>:9440/api/nutanix/v3/images/<uuid>/file" \
  -o /path/to/output.raw
```

## Snapshot/Clone Capabilities

### Snapshot Technology: Redirect-on-Write (RoW)

- When snapshot taken, original vDisk metadata cloned as read-only (zero space)
- New writes redirected to new locations
- Single write operation per update (vs. CoW's 1 read + 2 writes)
- Direct data lookup during restore (no chain traversal)

### Types
- **Crash-consistent**: Instantaneous, no guest impact
- **Application-consistent**: Requires NGT + VSS (Windows) or pre/post-freeze scripts (Linux)

### Can snapshot running VMs? Yes.

### Exporting Snapshot Data

No single API call for direct export. Workflow:
1. Create recovery point (snapshot)
2. Clone/restore snapshot to new VM
3. Export clone's vDisks via `qemu-img` or image service
4. OR download via: `GET /api/nutanix/v3/images/{uuid}/file`

### Cloning
- Redirect-on-write: clone and original share unchanged blocks
- Instantaneous (metadata-only)
- Via aCLI: `acli vm.clone testClone clone_from_vm=MYBASEVM`

## Nutanix Move (Official Migration Tool)

Nutanix's V2V tool for migrating VMs **to** AHV (not from):

- Deployed as VM appliance on target cluster
- Supports: VMware ESXi, Hyper-V, AWS, Azure, AHV-to-AHV
- Phases: Seeding (full copy) -> Incremental Sync (CBT) -> Cutover
- Reads source format (VMDK, VHD), writes as raw to AHV

Move is **inbound-only** -- it doesn't help with migrating **out** of Nutanix.

## AHV vs ESXi on Nutanix

| Aspect | AHV | ESXi on Nutanix |
|--------|-----|-----------------|
| Disk format | Raw in `/.acropolis/vmdisk/` | Standard VMDK |
| Storage protocol | iSCSI (local CVM) | NFSv3 (CVM export) |
| Provisioning | Always thin | Thin or Thick |
| I/O path | virtio-scsi -> iSCSI -> Stargate | VMDK -> NFS -> Stargate |
| VM management | Prism, aCLI | vCenter + Prism |
| Snapshots | Nutanix RoW | VMware CoW or Nutanix PD |

## SDKs and CLI Tools

### Go SDK (v4)
```bash
go get github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/...
go get github.com/nutanix/ntnx-api-golang-clients/dataprotection-go-client/v4/...
go get github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/...
go get github.com/nutanix/ntnx-api-golang-clients/storage-go-client/v4/...
go get github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/...
```

Source: [github.com/nutanix/ntnx-api-golang-clients](https://github.com/nutanix/ntnx-api-golang-clients)

### Python SDK (v4)
```bash
pip install ntnx-vmm-py-client ntnx-dataprotection-py-client ntnx-storage-py-client
```

### CLI Tools
- **aCLI** -- CVM-only, VM/disk/image/network operations
- **nCLI** -- Installable locally, cluster/VM/storage/protection domain operations
- **NuCLEI** -- Intent-based CLI for Prism Central
- **qemu-img** -- Available on CVMs for disk conversion

## Sources

- [Nutanix Bible -- AOS Storage](https://www.nutanixbible.com/4c-book-of-aos-storage.html)
- [Nutanix Bible -- AHV Architecture](https://www.nutanixbible.com/5a-book-of-ahv-architecture.html)
- [Nutanix Bible -- How AHV Works](https://www.nutanixbible.com/5b-book-of-ahv-how-it-works.html)
- [Nutanix Bible -- REST APIs](https://www.nutanixbible.com/19a-rest-apis.html)
- [Nutanix Developer Portal](https://www.nutanix.dev/api-reference/)
- [Nutanix v4 CBT/CRT APIs](https://www.nutanix.dev/2025/01/15/nutanix-v4-disaster-recovery-api-series-part-2-changed-blocks-tracking-cbt-and-changed-regions-tracking-crt/)
- [vDisk Series -- Where Are They Stored](https://next.nutanix.com/how-it-works-22/ahv-vdisk-part-1-where-are-they-stored-33618)
- [vDisk Series -- Accessing and Downloading](https://next.nutanix.com/ahv-virtualization-27/ahv-vdisk-part-2-accessing-and-downloading-vdisks-33672)
- [vDisk Series -- Creating Images from vDisks](https://next.nutanix.com/intelligent-operations-26/ahv-vdisk-part-3-creating-an-image-of-or-from-an-existing-vdisk-33686)
- [Export VM from AHV](https://next.nutanix.com/move-application-migration-19/want-to-export-a-vm-from-ahv-here-s-how-37275)
