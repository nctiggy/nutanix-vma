# VM Metadata Mapping: Nutanix AHV -> KubeVirt

## Compute

| Nutanix Field | KubeVirt Field | Notes |
|--------------|----------------|-------|
| `num_sockets` | -- | Combined into cores |
| `num_vcpus_per_socket` | -- | Combined into cores |
| `num_sockets * num_vcpus_per_socket` | `spec.domain.cpu.cores` | Total vCPUs |
| `num_threads_per_core` | `spec.domain.cpu.threads` | If >1 |
| `memory_size_mib` | `spec.domain.resources.requests.memory` | Direct map (MiB) |
| `enable_cpu_passthrough` | `spec.domain.cpu.model: host-passthrough` | Expose host CPU features |
| `is_vcpu_hard_pinned` | `spec.domain.cpu.dedicatedCpuPlacement: true` | Requires specific node config |
| `machine_type: Q35` | `spec.domain.machine.type: q35` | Modern chipset |
| `machine_type: PC` | `spec.domain.machine.type: pc-i440fx` | Legacy chipset |

### CPU Topology Considerations

Nutanix expresses CPU as `sockets * vcpus_per_socket * threads_per_core`.
KubeVirt supports `spec.domain.cpu.sockets`, `.cores`, `.threads`.

Recommended mapping:
```yaml
# Nutanix: 2 sockets, 4 vcpus/socket, 1 thread/core = 8 vCPUs
# KubeVirt:
spec:
  domain:
    cpu:
      sockets: 2
      cores: 4
      threads: 1
```

## Boot/Firmware

| Nutanix `boot_type` | KubeVirt `firmware.bootloader` |
|---------------------|-------------------------------|
| `LEGACY` | `bios: {}` |
| `UEFI` | `efi: {}` |
| `SECURE_BOOT` | `efi: { secureBoot: true }` |

```yaml
# LEGACY (BIOS)
spec:
  domain:
    firmware:
      bootloader:
        bios: {}

# UEFI
spec:
  domain:
    firmware:
      bootloader:
        efi: {}

# UEFI + Secure Boot
spec:
  domain:
    firmware:
      bootloader:
        efi:
          secureBoot: true
```

## Storage

### Disk Mapping

| Nutanix Disk Field | KubeVirt | Notes |
|-------------------|----------|-------|
| `device_type: DISK` | `spec.domain.devices.disks[].disk` | Standard disk |
| `device_type: CDROM` | `spec.domain.devices.disks[].cdrom` | CD-ROM device |
| `adapter_type: SCSI` | `disk.bus: scsi` or `disk.bus: virtio` | See bus discussion below |
| `adapter_type: IDE` | `disk.bus: sata` | IDE -> SATA in KubeVirt |
| `adapter_type: PCI` | `disk.bus: virtio` | NVMe-like |
| `adapter_type: SATA` | `disk.bus: sata` | Direct map |
| `disk_size_bytes` | PVC `.spec.resources.requests.storage` | Round up to GiB |

### Disk Bus Decision

AHV uses `virtio-scsi` by default. KubeVirt supports both:
- `disk.bus: scsi` -- presents as `/dev/sd*` (matches AHV)
- `disk.bus: virtio` -- presents as `/dev/vd*` (KubeVirt default, slightly better performance)

**Recommendation**: Use `scsi` by default to maximize compatibility. Device names in fstab, bootloader, and application configs will match. Offer `virtio` as an opt-in for performance-sensitive workloads.

### Disk Ordering

Nutanix disks are ordered by their index in `disk_list`. Preserve this ordering in KubeVirt to ensure consistent device naming.

### Multi-Disk Example

```yaml
# Nutanix VM with 2 disks + 1 CDROM
# disk_list:
#   [0] DISK, SCSI, 100GB (boot)
#   [1] DISK, SCSI, 500GB (data)
#   [2] CDROM, IDE

spec:
  domain:
    devices:
      disks:
      - name: disk-0
        bootOrder: 1
        disk:
          bus: scsi
      - name: disk-1
        disk:
          bus: scsi
      - name: disk-2
        cdrom:
          bus: sata
  volumes:
  - name: disk-0
    persistentVolumeClaim:
      claimName: vm-name-disk-0  # 100Gi PVC
  - name: disk-1
    persistentVolumeClaim:
      claimName: vm-name-disk-1  # 500Gi PVC
  - name: disk-2
    cloudInitNoCloud:  # or empty CDROM
      userData: ""
```

## Networking

### NIC Mapping

| Nutanix NIC Field | KubeVirt | Notes |
|-------------------|----------|-------|
| `subnet_reference` | Via NetworkMap -> pod or multus | User-configured mapping |
| `model: virtio` | `spec.domain.devices.interfaces[].model: virtio` | Default on both |
| `mac_address` | `interfaces[].macAddress` | Optional preserve |
| `ip_endpoint_list` (static) | cloud-init or static IP in guest | Complex, see below |
| `nic_type: NORMAL_NIC` | Standard interface | Default |
| `nic_type: DIRECT_NIC` | SR-IOV interface | Requires SR-IOV setup |
| `nic_type: NETWORK_FUNCTION_NIC` | Not supported | Flag in validation |

### Network Type Mapping

```yaml
# Pod network (default, NAT/masquerade)
spec:
  domain:
    devices:
      interfaces:
      - name: net-0
        masquerade: {}
  networks:
  - name: net-0
    pod: {}

# Multus secondary network (L2 bridge)
spec:
  domain:
    devices:
      interfaces:
      - name: net-0
        bridge: {}
  networks:
  - name: net-0
    multus:
      networkName: default/my-bridge-nad
```

### Static IP Preservation

If the Nutanix VM has static IPs configured:
1. The IP won't automatically transfer to the KubeVirt VM
2. Options:
   - **cloud-init**: Inject network config via cloud-init (if guest supports it)
   - **Guest config preservation**: If guest has static IPs configured in `/etc/netplan/` or Windows network settings, they persist on the disk -- but may conflict with the new network
   - **DHCP**: Switch to DHCP on the KubeVirt side (simplest)
   - **Manual**: Document as a post-migration step

### Multi-NIC Example

```yaml
# Nutanix VM with 2 NICs:
#   NIC 0: subnet "Production" (VLAN 100) -> pod network
#   NIC 1: subnet "Management" (VLAN 200) -> multus bridge

spec:
  domain:
    devices:
      interfaces:
      - name: production
        masquerade: {}
      - name: management
        bridge: {}
  networks:
  - name: production
    pod: {}
  - name: management
    multus:
      networkName: default/mgmt-bridge
```

## GPU

| Nutanix GPU Mode | KubeVirt Equivalent | Supported? |
|-----------------|---------------------|------------|
| `PASSTHROUGH_GRAPHICS` | `spec.domain.devices.gpus[].deviceName` | Yes, with device plugin |
| `PASSTHROUGH_COMPUTE` | `spec.domain.devices.gpus[].deviceName` | Yes, with device plugin |
| `VIRTUAL` (vGPU) | `spec.domain.devices.gpus[].virtualGPUOptions` | Yes, with NVIDIA vGPU |

GPU migration requires:
1. Same GPU hardware available on KubeVirt nodes
2. GPU device plugin installed (NVIDIA, AMD)
3. Manual configuration (no automated mapping)

**Recommendation**: Flag GPU VMs in validation. Don't auto-migrate GPU config.

## Other Metadata

| Nutanix Field | KubeVirt Mapping | Notes |
|--------------|------------------|-------|
| `name` | `metadata.name` | Sanitize for DNS1123 |
| `description` | `metadata.annotations["description"]` | Preserve as annotation |
| `categories` | `metadata.labels` | Map category:value to labels |
| `power_state` | `spec.running` | Based on `targetPowerState` |
| `guest_os_id` | annotation | Informational |
| `serial_port_list` | `spec.domain.devices.serials` | Direct map |
| `vga_console_enabled` | `spec.domain.devices.autoattachGraphicsDevice` | Default true |
| `nutanix_guest_tools` | -- | Not migrated (irrelevant on KubeVirt) |

## Name Sanitization

Nutanix VM names can contain spaces, underscores, and other characters not valid in K8s names. Sanitization rules:

```
Input:  "My Web Server (prod)_v2"
Output: "my-web-server-prod-v2"

Rules:
1. Lowercase
2. Replace spaces, underscores, parentheses with hyphens
3. Remove invalid characters
4. Collapse multiple hyphens
5. Trim leading/trailing hyphens
6. Truncate to 63 characters (DNS1123 max)
7. If collision, append -N suffix
```

## Complete Example

### Nutanix VM (from v3 API)

```json
{
  "metadata": {
    "uuid": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
  },
  "spec": {
    "name": "web-server-01",
    "description": "Production web server",
    "resources": {
      "num_sockets": 2,
      "num_vcpus_per_socket": 4,
      "memory_size_mib": 8192,
      "machine_type": "Q35",
      "boot_config": {
        "boot_type": "UEFI"
      },
      "disk_list": [
        {
          "device_properties": {
            "device_type": "DISK",
            "disk_address": { "adapter_type": "SCSI", "device_index": 0 }
          },
          "disk_size_bytes": 107374182400,
          "storage_config": {
            "storage_container_reference": { "uuid": "sc-uuid-1" }
          }
        },
        {
          "device_properties": {
            "device_type": "DISK",
            "disk_address": { "adapter_type": "SCSI", "device_index": 1 }
          },
          "disk_size_bytes": 536870912000,
          "storage_config": {
            "storage_container_reference": { "uuid": "sc-uuid-1" }
          }
        }
      ],
      "nic_list": [
        {
          "subnet_reference": { "uuid": "subnet-uuid-1" },
          "ip_endpoint_list": [
            { "ip": "10.0.1.50", "type": "ASSIGNED" }
          ],
          "mac_address": "50:6b:8d:12:34:56"
        }
      ]
    }
  }
}
```

### Generated KubeVirt VirtualMachine

```yaml
apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: web-server-01
  namespace: migrated-vms
  labels:
    migration.nutanix.io/source-uuid: a1b2c3d4-e5f6-7890-abcd-ef1234567890
    migration.nutanix.io/plan: migrate-web-tier
  annotations:
    migration.nutanix.io/source-description: "Production web server"
    migration.nutanix.io/source-cluster: "cluster-uuid"
    migration.nutanix.io/migrated-at: "2026-03-19T22:00:00Z"
spec:
  running: true
  template:
    spec:
      domain:
        machine:
          type: q35
        cpu:
          sockets: 2
          cores: 4
          threads: 1
        resources:
          requests:
            memory: 8192Mi
        firmware:
          bootloader:
            efi: {}
        devices:
          disks:
          - name: disk-0
            bootOrder: 1
            disk:
              bus: scsi
          - name: disk-1
            disk:
              bus: scsi
          interfaces:
          - name: default
            masquerade: {}
      networks:
      - name: default
        pod: {}
      volumes:
      - name: disk-0
        persistentVolumeClaim:
          claimName: web-server-01-disk-0
      - name: disk-1
        persistentVolumeClaim:
          claimName: web-server-01-disk-1
```
