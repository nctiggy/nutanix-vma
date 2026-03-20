# Nutanix VMA -- Executive Summary

## What

Build a Kubernetes-native operator to migrate Nutanix AHV virtual machines to KubeVirt, inspired by [kubev2v/forklift](https://github.com/kubev2v/forklift) (Red Hat MTV).

## Why

No tool exists for Nutanix AHV -> KubeVirt migration. Forklift supports VMware vSphere, oVirt/RHV, OpenStack, OVA, EC2, and Hyper-V -- **Nutanix is completely absent** from the codebase, community, GitHub issues, and roadmap.

## Key Insight: AHV is KVM-Based

Nutanix AHV runs on **KVM/QEMU** under the hood. KubeVirt also runs on **KVM/QEMU**. This eliminates the hardest part of VM migration:

- VM disks are **raw flat files** -- no proprietary format to decode
- Guests already use **VirtIO drivers** (virtio-scsi, virtio-net) -- same as KubeVirt
- **No guest OS conversion needed for Linux** -- correct kernel modules already loaded
- **Windows VMs already have VirtIO drivers** -- may just need updating
- **virt-v2v is mostly unnecessary** -- its primary job (driver injection) is already done

This is a **massive simplification** over VMware->KubeVirt where virt-v2v must replace VMXNET3/PVSCSI with VirtIO and strip VMware Tools.

## Architecture Decision: Standalone Operator (Not a Forklift Plugin)

Forklift is **not plugin-based** -- adding a provider requires modifying ~12 hardcoded switch/factory files across the codebase. Given the simpler AHV->KubeVirt path, a standalone operator is recommended:

- Simpler pipeline (no virt-v2v step)
- Freedom to target non-OpenShift clusters
- Can integrate with SpectroCloud/Palette ecosystem
- Independent release cadence

## Implementation Phases

1. **Phase 0**: Prism API Client -- validate API access and SDK usability
2. **Phase 1**: Cold Migration MVP -- single VM, CLI tool
3. **Phase 2**: Operator + CRDs -- Provider, Plan, Migration, NetworkMap, StorageMap
4. **Phase 3**: Warm Migration -- CBT-based incremental sync
5. **Phase 4**: Polish -- validation, hooks, progress tracking, error recovery
6. **Phase 5**: Scale -- performance, parallelism, large-scale testing

## Key Resources

- Nutanix Go SDK: `github.com/nutanix/ntnx-api-golang-clients`
- Nutanix v4 API: `https://<PC>:9440/api/{namespace}/v4.x/`
- Nutanix v3 Image Download: `GET /api/nutanix/v3/images/{uuid}/file`
- Nutanix CBT/CRT: v4 dataprotection API (two-step: PC discovery -> PE changed regions)
- KubeVirt CDI: `github.com/kubevirt/containerized-data-importer`
- Forklift reference: `github.com/kubev2v/forklift`
