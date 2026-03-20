# Gotchas, Risks, and Open Questions

## Known Gotchas

### 1. No Direct "Stream Disk Blocks" API

Unlike VMware VDDK (a purpose-built SDK for block-level disk streaming), Nutanix does not have an equivalent. Options for disk data access:

- **Image Service download**: `GET /api/nutanix/v3/images/{uuid}/file` -- downloads entire disk as one blob. No resume, no block-level access.
- **CVM NFS access**: Direct NFS mount of `/.acropolis/vmdisk/<uuid>` -- high performance but requires CVM network access from K8s pods.
- **CBT API**: Returns changed block metadata but not the actual block data. You still need a separate mechanism to read those blocks.

**Impact**: For large disks (500GB+), the Image Service approach downloads the entire image in one HTTP request. Need robust retry/resume handling.

**Mitigation**: Build a transfer pod that runs on a node with CVM network access, or implement HTTP range requests if the Nutanix API supports them (needs testing).

### 2. CBT API Two-Step with Short-Lived Tokens

The v4 CBT/CRT API requires:
1. POST to Prism Central -> returns PE cluster IP + JWT token (15 minutes!)
2. GET to Prism Element using JWT token

**Impact**: For large disks with many changed regions (paginated at 10,000 per call), the 15-minute token may expire mid-pagination.

**Mitigation**: Token refresh logic between pagination pages. Pre-calculate expected pages and refresh proactively.

### 3. Prism Central vs. Prism Element Split

| API | Available On | Notes |
|-----|-------------|-------|
| VM management (v4) | Prism Central only | Main API for inventory |
| Image download (v3) | Prism Central | Binary download |
| Storage containers (v2) | Prism Element only | No PC equivalent |
| CBT data (v4) | Prism Element only | Redirected from PC |

**Impact**: The operator needs credentials and network access to BOTH Prism Central and potentially multiple Prism Element instances (one per cluster).

**Mitigation**: Provider CRD should support discovery of PE instances from PC. May need per-cluster PE credentials.

### 4. Image Service Download Limitations

- No streaming/chunked download (entire file as one blob)
- Unknown if HTTP range requests are supported
- PC image uploads go to SelfServiceContainer (no container selection)
- Creating an image from a vDisk requires the vDisk to exist (snapshot or live disk)

**Impact**: Memory pressure on transfer pods for large disks.

**Mitigation**: Stream to disk, not memory. Use CDI's built-in HTTP importer which handles this.

### 5. Storage Container Access Requires CVM

Direct NFS access to `/.acropolis/vmdisk/` needs:
- Network route from K8s pod to CVM (port 2049 NFS)
- This may not exist in all network topologies
- CVMs are on a private management network in many deployments

**Impact**: The "direct NFS" transfer path may not be universally available.

**Mitigation**: Default to Image Service API path (works over HTTPS to PC). Offer NFS as an optional high-performance path.

### 6. Nutanix Go SDK Instability

The Go SDK (`github.com/nutanix/ntnx-api-golang-clients`) is:
- Auto-generated from OpenAPI specs
- Known to have breaking changes between releases
- Separate Go modules per namespace (vmm, dataprotection, networking, etc.)
- May lag behind API changes

**Impact**: SDK version pinning issues, potential breaking changes on upgrade.

**Mitigation**: Build a thin abstraction layer. Consider using raw `net/http` with manual JSON marshaling for stability. Pin specific SDK versions.

### 7. Volume Groups with External iSCSI

VMs using Nutanix Volume Groups (shared storage via iSCSI) can't be migrated by copying vDisks alone. Volume Groups are external to the VM's disk_list.

**Impact**: VMs with Volume Groups need special handling or should be excluded.

**Mitigation**: Detect Volume Group references in validation. Flag as "not supported" or provide Volume Group migration as a separate feature.

### 8. GPU Passthrough VMs

AHV supports three GPU modes:
- `PASSTHROUGH_GRAPHICS` -- full GPU passthrough
- `PASSTHROUGH_COMPUTE` -- compute-only passthrough
- `VIRTUAL` -- vGPU (shared GPU)

KubeVirt has its own GPU/vGPU device plugin model that doesn't map 1:1.

**Impact**: GPU VMs may need manual intervention or should be excluded from automated migration.

**Mitigation**: Validate and warn. Require manual GPU configuration on the KubeVirt side.

### 9. NGT (Nutanix Guest Tools) Removal

NGT includes:
- VSS provider (Windows)
- File-level restore agent
- Self-service restore portal
- Guest agent for IP reporting

Left installed, NGT won't cause boot failures but will log errors trying to reach a Prism endpoint that doesn't exist.

**Impact**: Not a blocker but creates noise in guest logs.

**Mitigation**: Pre-migration hook to uninstall NGT. Or post-migration cleanup script. Or document as a manual step.

### 10. Disk Device Naming

AHV uses `/dev/sd*` (SCSI via virtio-scsi). KubeVirt defaults to `/dev/vd*` (VirtIO block).

However, KubeVirt also supports `disk.bus: scsi` which would present as `/dev/sd*` -- matching AHV.

**Impact**: If guest uses device names in fstab (not UUIDs), boot could fail.

**Mitigation**:
- Use `disk.bus: scsi` in KubeVirt to match AHV's virtio-scsi presentation
- OR use `disk.bus: virtio` (default) -- most modern Linux uses UUIDs in fstab
- Validate fstab in pre-flight if possible (requires guest inspection)

## Risks

### High Severity

| Risk | Description | Mitigation |
|------|-------------|------------|
| Large disk transfer times | TB-scale VMs can take hours to transfer | CBT warm migration, compression, parallel disks |
| Data corruption during transfer | Network interruption mid-transfer | Checksums per block, retry with resume, snapshot consistency |
| Source VM left powered off | Operator failure between power-off and VM creation on target | Store power state, implement rollback logic, timeout + auto-restore |

### Medium Severity

| Risk | Description | Mitigation |
|------|-------------|------------|
| Nutanix API changes | v3 deprecation, v4 breaking changes | Abstract client, version negotiation, integration tests |
| Multi-cluster complexity | VMs across multiple PE clusters behind one PC | Per-cluster credential management, cluster-aware routing |
| Network topology barriers | K8s pods can't reach Prism/CVM | Transfer proxy, configurable endpoints, VPN docs |
| Concurrent migration load | Many VMs migrating simultaneously overwhelm Nutanix or K8s | maxInFlight controls, bandwidth throttling, resource requests |

### Low Severity

| Risk | Description | Mitigation |
|------|-------------|------------|
| Encrypted disks (LUKS) | Need LUKS key to access disk contents | Support key injection (Forklift pattern) |
| Nutanix licensing | API access may vary by license tier | Document license requirements |
| Snapshot accumulation | Failed migrations leave orphan snapshots | Cleanup controller, finalizers |
| Static IP conflicts | Migrated VM may conflict with existing IPs in K8s network | Pre-flight IP validation, NetworkPolicy |

## Open Questions (Need Nutanix Cluster Access)

### API Behavior

1. **Can we create an image from a specific vDisk in a recovery point via API?**
   The docs suggest: create recovery point -> clone VM from recovery point -> create image from clone's disk. Is there a shorter path?

2. **Does the Image Service download support HTTP Range requests?**
   If yes, we can implement resume-on-failure and parallel chunk downloads. Critical for large disks.

3. **What's the actual API response format for CBT changed regions?**
   Need to test with real data to understand block granularity, zero-region behavior, and edge cases.

4. **Can Prism Central export OVA directly?**
   Docs mention OVA export since pc.2020.8. If the OVA includes disk data + metadata, this could be an alternative transfer path.

5. **Is there a streaming/chunked upload API for creating images?**
   For large disk exports to the Image Service.

### Performance

6. **What's the throughput of Image Service download for large disks?**
   Is it bounded by Prism Central's web server? Does it scale with cluster size?

7. **What's the latency/throughput of CBT API calls?**
   How fast can we paginate through changed regions for a 1TB disk?

8. **Can multiple disks be exported simultaneously from the same VM?**
   Or do we need to serialize?

### SDK/Tooling

9. **Does the Go SDK cover all needed endpoints?**
   Specifically: dataprotection CBT, vmm power actions, image file download. Some endpoints may be REST-only.

10. **Is there a mock/simulator for the Nutanix API?**
    For integration testing without a real cluster. Forklift has `vcsim` for VMware.

### Multi-Cluster

11. **How does PC report VMs from multiple PE clusters?**
    Does the VM list response include cluster reference? Can we filter by cluster?

12. **Are PE credentials separate from PC credentials?**
    Or does PC auth propagate to PE?

### Edge Cases

13. **What happens to VM categories/tags during migration?**
    Should they map to K8s labels? Are they preserved anywhere?

14. **How are VM serial numbers/UUIDs handled?**
    Some guest software is tied to hardware IDs. Does KubeVirt allow setting a custom machine UUID?

15. **What about VMs with multiple NICs on the same subnet?**
    Does the NetworkMap need to handle per-NIC mapping or just per-subnet?

16. **What's the behavior for VMs with CD-ROM drives attached?**
    Are ISO images migrated too? Or just data disks?

## Comparison with Existing Tools

### vs. Forklift (for VMware)

| Aspect | Forklift (VMware) | Nutanix VMA (Proposed) |
|--------|-------------------|------------------------|
| Guest conversion | Required (VMware Tools -> VirtIO) | Not required (already VirtIO) |
| Disk transfer SDK | VDDK (proprietary, licensed) | Nutanix REST API (standard HTTP) |
| Warm migration | Yes (CBT via VDDK) | Yes (CBT via v4 API) |
| Inventory caching | SQLite in-process | TBD (CRD status or in-memory) |
| Validation | OPA/Rego policies | Go-based (simpler, or OPA later) |
| UI | OpenShift Console plugin | TBD (kubectl plugin first) |
| Maturity | Production (Red Hat MTV) | Greenfield |

### vs. Nutanix Move

| Aspect | Nutanix Move | Nutanix VMA (Proposed) |
|--------|-------------|------------------------|
| Direction | Inbound to AHV only | Outbound from AHV to KubeVirt |
| Target | Nutanix AHV | KubeVirt on any K8s |
| Architecture | VM appliance on AHV | K8s operator |
| Sources | VMware, Hyper-V, AWS, Azure, AHV | Nutanix AHV only |
| Warm migration | Yes | Yes (planned) |
| Open source | No | Yes (planned) |

## Testing Strategy

### Unit Tests
- Nutanix API client mocks
- VM metadata -> KubeVirt CR translation
- Validation logic
- State machine transitions

### Integration Tests
- Mock Prism API server (need to build)
- CDI DataVolume creation/completion
- KubeVirt VirtualMachine creation

### E2E Tests (Require Real Infrastructure)
- Full cold migration of a Linux VM
- Full cold migration of a Windows VM
- Warm migration with CBT
- Failure and recovery scenarios
- Multi-disk, multi-NIC VM migration

### Test Infrastructure Options
- Real Nutanix cluster (when available)
- Nutanix Community Edition (free, limited)
- Mock API server (for CI/CD)
