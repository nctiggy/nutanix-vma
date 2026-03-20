# Migration Pipeline Design

## Why AHV->KubeVirt is Simpler Than VMware->KubeVirt

| Concern | VMware -> KubeVirt | AHV -> KubeVirt |
|---------|-------------------|-----------------|
| Disk format | VMDK (proprietary) -> raw | Raw -> raw (already native!) |
| Storage drivers | PVSCSI -> VirtIO (must inject) | VirtIO -> VirtIO (already there) |
| Network drivers | VMXNET3 -> VirtIO (must inject) | VirtIO -> VirtIO (already there) |
| Guest tools | Remove VMware Tools | Remove NGT (simpler) |
| virt-v2v needed? | Yes (critical for driver swap) | No (maybe light cleanup only) |
| Transfer SDK | VDDK (proprietary, licensed) | Standard HTTP API / NFS |

## Cold Migration Pipeline

### Overview
```
1. Connect to Prism Central
2. User selects VMs, maps networks/storage
3. Validate compatibility
4. For each VM:
   a. Power off source VM
   b. Create recovery point (snapshot)
   c. Export each disk image
   d. Import into CDI DataVolume
   e. Create KubeVirt VirtualMachine CR
   f. Start VM
   g. Cleanup
```

### Detailed Steps

#### Step 1: Inventory
```
Prism Central v4 API:
  GET /api/vmm/v4.0/ahv/config/vms           -> VM list
  GET /api/networking/v4.0/config/subnets     -> Network list
  GET /api/nutanix/v2.0/storage-containers    -> Storage container list (PE only)
```

Cache results locally (in-memory or CRD status).

#### Step 2: Plan & Validate

User creates:
- **NetworkMap**: Nutanix subnet UUID -> pod network or Multus NAD
- **StorageMap**: Nutanix storage container UUID -> K8s StorageClass
- **MigrationPlan**: List of VM UUIDs + maps + options

Validation checks:
- All VM disks have mapped storage containers
- All VM NICs have mapped subnets
- Boot type is supported (LEGACY/UEFI/SECURE_BOOT)
- No unsupported features (GPU passthrough, Volume Groups with external iSCSI)
- Disk sizes fit within StorageClass limits
- Target namespace exists
- No MAC conflicts on target

#### Step 3: Power Off Source VM
```
POST /api/vmm/v4.0/ahv/config/vms/{uuid}/$actions/power-off
```
Poll until confirmed off. Record original power state for rollback.

#### Step 4: Create Recovery Point (Snapshot)
```
POST /api/dataprotection/v4.0/config/recovery-points
Body: {
  "name": "migration-<vm-uuid>-<timestamp>",
  "vm_recovery_point_list": [
    { "vm_reference": { "uuid": "<vm-uuid>" } }
  ]
}
```

#### Step 5: Export Disk Images

**Option A -- Image Service (Recommended for MVP)**:
1. Create an image from the snapshot's vDisk:
   ```
   POST /api/vmm/v4.0/content/images
   Body: {
     "name": "migration-disk-<uuid>",
     "type": "DISK_IMAGE",
     "source": { "data_source_reference": { "uuid": "<vdisk-uuid>" } }
   }
   ```
2. Download the image:
   ```
   GET /api/nutanix/v3/images/{image-uuid}/file
   ```

**Option B -- Direct NFS (Requires CVM Network Access)**:
```bash
# From a transfer pod with NFS access:
qemu-img convert -f raw -O qcow2 -c \
  nfs://<cvm_ip>/<container>/.acropolis/vmdisk/<vdisk_uuid> \
  /staging/disk.qcow2
```

**Option C -- Volume Populator (Best Long-Term)**:
Custom CRD + populator pod that connects to Prism API and streams disk data directly into PVC.

#### Step 6: Import into KubeVirt via CDI

```yaml
apiVersion: cdi.kubevirt.io/v1beta1
kind: DataVolume
metadata:
  name: <vm-name>-disk-0
  namespace: <target-namespace>
spec:
  source:
    http:
      url: "https://<prism-central>:9440/api/nutanix/v3/images/<uuid>/file"
      secretRef: nutanix-creds  # basic auth secret
      certConfigMap: nutanix-ca  # if custom CA
  storage:
    resources:
      requests:
        storage: <disk-size>
    storageClassName: <mapped-storage-class>
```

CDI auto-converts QCOW2/raw to raw, resizes to fill PVC.

#### Step 7: Create KubeVirt VirtualMachine CR

```yaml
apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: <vm-name>
  namespace: <target-namespace>
  labels:
    migration.nutanix.io/source-uuid: <nutanix-vm-uuid>
spec:
  running: true
  template:
    spec:
      domain:
        cpu:
          cores: <num_sockets * num_vcpus_per_socket>
        resources:
          requests:
            memory: <memory_size_mib>Mi
        firmware:
          bootloader:
            # LEGACY -> bios: {}
            # UEFI -> efi: {}
            # SECURE_BOOT -> efi: { secureBoot: true }
        devices:
          disks:
          - name: disk-0
            disk:
              bus: virtio  # AHV already uses virtio!
          interfaces:
          - name: net-0
            masquerade: {}  # or bridge for Multus
      networks:
      - name: net-0
        pod: {}  # or multus with NAD reference
      volumes:
      - name: disk-0
        persistentVolumeClaim:
          claimName: <vm-name>-disk-0
```

#### Step 8: Cleanup
- Delete temporary images from Nutanix Image Service
- Delete recovery points (snapshots)
- Optionally delete source VM (user confirmation required)

## Warm Migration Pipeline (Minimal Downtime)

### Overview
```
1. Take initial snapshot S1
2. Bulk-copy all allocated blocks
3. VM continues running on Nutanix
4. Take snapshot S2
5. Use CBT API to get changed blocks between S1 and S2
6. Transfer only delta
7. Repeat 4-6 until delta is small
8. CUTOVER: Power off, final snapshot, final delta
9. Create KubeVirt VM, start
```

### CBT API Flow

**Step 1 -- Create Base Recovery Point**:
```
POST /api/dataprotection/v4.0/config/recovery-points
-> recovery_point_uuid_1
```

**Step 2 -- Full Copy**: Download all disk data from S1.

**Step 3 -- Create Reference Recovery Point**:
```
POST /api/dataprotection/v4.0/config/recovery-points
-> recovery_point_uuid_2
```

**Step 4 -- Cluster Discovery (POST to PC)**:
```
POST /api/dataprotection/v4.0/config/recovery-points/{rp2}/$actions/compute-changed-regions
Body: {
  "operation": "COMPUTE_CHANGED_REGIONS",
  "base_recovery_point": { "uuid": "<rp1>" },
  "reference_recovery_point": { "uuid": "<rp2>" }
}
-> Response: { "cluster_ip": "10.x.x.x", "jwt_token": "<15min-token>", "redirect_uri": "..." }
```

**Step 5 -- Compute Changed Regions (GET to PE)**:
```
GET https://<PE_IP>:9440/api/dataprotection/v4.0/config/recovery-points/{rp2}/changed-regions
  ?offset=0&length=<disk-size>&block_size_byte=1048576&base_recovery_point_id=<rp1>
Headers: Cookie: NTNX_IGW_SESSION=<jwt_token>
-> Response: {
  "changed_regions": [
    { "offset": 0, "length": 1048576, "is_zero": false },
    { "offset": 5242880, "length": 2097152, "is_zero": true },
    ...
  ],
  "next_offset": 10485760,
  "file_size": 53687091200
}
```

**Step 6 -- Transfer Delta**: Read only the changed byte ranges from the reference recovery point's vDisk. Write to the corresponding offsets in the target PVC.

**Step 7 -- Cutover**: Power off VM, take final snapshot, compute final delta (should be small), transfer, create VM, start.

### Warm Migration Considerations

- JWT token expires in 15 minutes -- must refresh between pages if disk is large
- Pagination: max 10,000 regions per call
- Zero-region optimization: skip zero regions (don't transfer, just write zeros or rely on thin provisioning)
- Transfer pod needs write access to the target PVC (use a pod with the PVC mounted)
- Delta writes need random access to PVC -- use block mode PVC or mount as filesystem and write to the raw file

## Data Transfer Strategies Comparison

| Strategy | Complexity | Performance | Incremental? | Network Req |
|----------|-----------|-------------|--------------|-------------|
| Image Service Download | Low | Medium | No | PC access only |
| Direct NFS from CVM | Medium | High | No | CVM NFS access |
| Volume Populator | High | High | Yes (with CBT) | PC + PE access |
| CDI HTTP Import | Low | Medium | No | PC access only |
| `virtctl image-upload` | Low | Low | No | Local file + K8s access |

### Recommended Evolution

1. **MVP**: Image Service Download -> CDI HTTP Import
2. **v1**: Custom Volume Populator (direct Prism API -> PVC)
3. **v2**: CBT-based warm migration via Volume Populator

## Guest OS Considerations

### Linux VMs (Minimal Work)

Since AHV already uses KVM/VirtIO:
- VirtIO kernel modules already loaded (virtio_blk, virtio_net, virtio_scsi)
- Initramfs already includes VirtIO drivers
- **No virt-v2v needed**

Checklist:
- [ ] Verify fstab uses UUIDs not device names (`/dev/sd*` should work since both use SCSI, but UUIDs are safer)
- [ ] Install `qemu-guest-agent` for KubeVirt management
- [ ] Remove NGT if installed (optional, not harmful if left)
- [ ] Update network config if using static IPs (MAC may change)
- [ ] Verify cloud-init compatibility if used

### Windows VMs (Some Work)

AHV Windows VMs already have VirtIO drivers (Nutanix VirtIO package):
- Storage: vioscsi (virtio-scsi)
- Network: netkvm (virtio-net)
- Balloon: balloon
- Serial: vioserial

Checklist:
- [ ] Verify VirtIO drivers are present and up-to-date
- [ ] Consider updating to [Fedora VirtIO-Win drivers](https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/) for best KubeVirt compat
- [ ] Uninstall NGT
- [ ] Verify boot configuration (BIOS/UEFI match)
- [ ] Update network settings if static IPs

### virt-v2v: When You Actually Need It

For AHV->KubeVirt, virt-v2v is **generally not needed**. Consider it only for:
- Very old Linux VMs without VirtIO in initramfs (unlikely on AHV)
- Windows VMs with outdated/missing VirtIO drivers (rare on AHV)
- If you want automated NGT removal and QEMU guest agent injection

If needed, use `-i disk` mode (virt-v2v has no native AHV driver):
```bash
virt-v2v -i disk /path/to/exported-disk.raw -o local -of qcow2 -os /output/
```

## Sources

- [CDI User Guide](https://kubevirt.io/user-guide/storage/containerized_data_importer/)
- [CDI Supported Operations](https://github.com/kubevirt/containerized-data-importer/blob/main/doc/supported_operations.md)
- [KubeVirt Disks and Volumes](https://kubevirt.io/user-guide/storage/disks_and_volumes/)
- [KubeVirt Interfaces and Networks](https://kubevirt.io/user-guide/network/interfaces_and_networks/)
- [How to Import VM into KubeVirt](https://kubevirt.io/2019/How-To-Import-VM-into-Kubevirt.html)
- [virt-v2v man page](https://libguestfs.org/virt-v2v.1.html)
- [Nutanix v4 CBT/CRT APIs](https://www.nutanix.dev/2025/01/15/nutanix-v4-disaster-recovery-api-series-part-2-changed-blocks-tracking-cbt-and-changed-regions-tracking-crt/)
