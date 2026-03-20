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

The operator follows the [Forklift](https://github.com/kubev2v/forklift) model with Kubernetes-native CRDs:

| CRD | Purpose |
|-----|---------|
| `NutanixProvider` | Connection to Prism Central (credentials, TLS, refresh interval) |
| `NetworkMap` | Nutanix subnets -> KubeVirt pod/Multus networks |
| `StorageMap` | Nutanix storage containers -> Kubernetes StorageClasses |
| `MigrationPlan` | Which VMs to migrate, validation results |
| `Migration` | Execution of a plan, per-VM pipeline progress |
| `Hook` | Pre/post migration actions (containers or Ansible) |

## Migration Modes

### Cold Migration (Production-Ready)

Power off source VM -> snapshot -> export disks -> import via CDI -> create KubeVirt VM -> start.

```
PowerOff -> Snapshot -> ExportDisks -> ImportDisks -> CreateVM -> StartVM -> Cleanup
```

### Warm Migration (Experimental -- Requires Real-Cluster Validation)

Uses Nutanix v4 CBT (Changed Block Tracking) APIs for incremental sync while the source VM runs. Minimal downtime cutover.

> **Warning**: Warm migration depends on unverified assumptions about the Nutanix Image Service API (HTTP Range header support for partial disk reads). This feature is gated behind real-cluster validation.

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

- All disks have mapped storage containers
- All NICs have mapped subnets
- Target StorageClasses and NetworkAttachmentDefinitions exist
- CDI and KubeVirt are installed
- GPU passthrough VMs flagged (Warning)
- Volume Group VMs blocked (Error)
- MAC address conflicts detected

## Project Status

**Phase: PRD Complete, Implementation Pending**

This project is designed to be built using [Ralph](https://github.com/snarktank/ralph) -- an autonomous AI agent loop that implements stories iteratively. The PRD has been through 4 rounds of critic review (3 internal + 1 external via OpenAI Codex/GPT-5.4).

### What's Done

- Comprehensive research (7 documents covering Forklift internals, Nutanix AHV architecture, migration pipeline design, API analysis, and metadata mapping)
- Production-ready PRD with 22 Ralph-compatible stories
- Full CRD specifications and API response type definitions
- Mock Nutanix API server design
- GitHub Actions CI/CD plan
- E2E test framework design (gated behind real infrastructure)

### Running Ralph

There are two ways to run Ralph: the standard loop (local verification only) or the CI-verified loop (recommended).

#### Prerequisites

- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated (`npm install -g @anthropic-ai/claude-code`)
- [GitHub CLI](https://cli.github.com/) installed and authenticated (`gh auth login`)
- `jq` installed (`brew install jq` or `apt install jq`)

#### Step 1 -- Install Ralph

```bash
mkdir -p scripts/ralph
curl -sL https://raw.githubusercontent.com/snarktank/ralph/main/ralph.sh -o scripts/ralph/ralph.sh
curl -sL https://raw.githubusercontent.com/snarktank/ralph/main/CLAUDE.md -o scripts/ralph/CLAUDE.md
chmod +x scripts/ralph/ralph.sh
```

#### Step 2 -- Convert the PRD to prd.json

Open Claude Code in this repo and run:

```
Load the ralph skill and convert tasks/prd-nutanix-vma.md to prd.json
```

This creates `prd.json` with user stories structured for autonomous execution.

#### Step 3 -- Run Ralph

**Option A: CI-Verified Loop (Recommended)**

```bash
./scripts/ralph-ci.sh 30
```

This is a custom wrapper that adds GitHub Actions as an external verification gate:

```
For each iteration:
  1. Claude Code implements the next incomplete story
  2. Runs make build && make test locally (quick sanity check)
  3. Commits and pushes to GitHub
  4. Watches the CI run via `gh run watch`
  5. IF CI passes  -> marks story complete, moves to next story
  6. IF CI fails   -> does NOT mark complete, writes CI failure
                      logs to progress.txt, moves to next iteration
  7. Next iteration: fresh context window reads the failure logs
                     and debugs the issue
```

The key insight: **a fresh context window for debugging is a feature, not a bug.** A new Claude instance reads the CI failure logs without the baggage of the previous context window's assumptions. This prevents hallucination cascades where a stuck context keeps trying the same broken approach.

**Option B: Standard Ralph Loop (Local Only)**

```bash
./scripts/ralph/ralph.sh --tool claude 30
```

This is the standard Ralph loop -- it only verifies locally with `make build && make test`. No push, no CI verification. Faster but less reliable.

#### How the CI-Verified Loop Works

```
   Ralph (Claude Code)          GitHub              CI Runner
   ==================          ======              =========
   Read prd.json + progress.txt
   Pick next incomplete story
   Implement story
   make build && make test
   git commit
   git push ─────────────────► receives push
                                triggers CI ──────► lint, build,
                                                    unit test,
                                                    integration test
   gh run watch ◄──────────────────────────────────►
   (blocks until CI completes)
                                                    CI result
   IF pass: mark complete ◄─────────────────────── ✅ green
   IF fail: save logs     ◄─────────────────────── ❌ red
            to progress.txt
            DO NOT mark complete

   Next iteration (FRESH context window):
   Read progress.txt
   See: "Story X failed CI. Logs: ..."
   Debug with clean context
```

#### Monitoring Progress

```bash
# Check which stories are complete
jq '.userStories[] | {id, title, status}' prd.json

# Check learnings and CI failure logs from past iterations
cat progress.txt

# Check git log for Ralph's commits
git log --oneline

# Check GitHub Actions runs
gh run list --limit 10
```

#### Built-In Telemetry

The CI-verified loop automatically records full telemetry to `metrics.json` and generates a human-readable `metrics-report.md` at the end of the run (or on Ctrl+C). Tracked metrics include:

| Metric | Description |
|--------|-------------|
| Timestamps | UTC start/end time for the overall run and each iteration |
| Per-iteration timing | How long each story attempt took (Claude thinking + CI wait) |
| Pass/fail per story | How many CI attempts each story needed to pass |
| First-pass CI rate | Percentage of stories that passed CI on the first try |
| Failure timeline | Chronological list of every CI failure with links to the GH Actions run |
| Efficiency analysis | Productive vs debug vs wasted iterations, average durations |
| Attempt tracking | Which stories needed multiple attempts and how many |

Example output from `metrics-report.md`:

```markdown
## Summary

| Metric | Value |
|--------|-------|
| Total Duration | 2h 45m 12s |
| Total Iterations | 28 |
| Stories Completed | 22 |
| CI Passes | 22 |
| CI Failures | 4 |
| First-Pass CI Rate | 84% |

## Per-Story Breakdown

### Story 1: Project Scaffolding (story-1)
| Attempt | Result | Duration | CI Run | Time |
|---------|--------|----------|--------|------|
| 1 | SKIP (pre-CI) | 3m 22s | -- | 2026-03-19T22:00:00Z |

### Story 13b: Migration Controller -- Disk Transfer (story-13b)
| Attempt | Result | Duration | CI Run | Time |
|---------|--------|----------|--------|------|
| 1 | **FAIL** | 8m 15s | 12345 | 2026-03-20T01:30:00Z |
| 2 | **FAIL** | 6m 44s | 12348 | 2026-03-20T01:39:00Z |
| 3 | PASS | 7m 02s | 12351 | 2026-03-20T01:46:00Z |
```

The metrics files are committed to the repo so the full build history is preserved for post-mortem analysis.

### What's Next After Ralph

1. Validate against a real Nutanix cluster when access is available
2. Run a second Ralph loop with a cluster-validation PRD
3. Promote warm migration from experimental to production once CBT data source is confirmed

## Repository Structure

```
nutanix-vma/
├── research/                      # Deep research documents
│   ├── 00-executive-summary.md
│   ├── 01-forklift-architecture.md
│   ├── 02-nutanix-ahv-internals.md
│   ├── 03-migration-pipeline.md
│   ├── 04-proposed-architecture.md
│   ├── 05-gotchas-risks-questions.md
│   └── 06-vm-metadata-mapping.md
├── tasks/
│   └── prd-nutanix-vma.md         # Full PRD (22 stories, 72 requirements)
├── docs/
│   └── how-we-built-this.md       # Full prompt log and process documentation
└── README.md
```

After Ralph runs, the repo will also contain:
```
├── cmd/main.go                    # Operator entry point
├── api/v1alpha1/                  # CRD type definitions
├── internal/
│   ├── controller/                # K8s controllers
│   ├── nutanix/                   # Prism API client
│   ├── builder/                   # VM metadata translation
│   ├── transfer/                  # Disk transfer orchestration
│   └── validation/                # Pre-flight checks
├── pkg/mock/                      # Mock Nutanix API server
├── test/                          # Integration + E2E tests
├── config/                        # CRD manifests, RBAC, deployment
└── .github/workflows/             # CI/CD pipelines
```

## Prerequisites (For Running the Operator)

- Kubernetes cluster (any vanilla K8s)
- [KubeVirt](https://kubevirt.io/) installed
- [CDI](https://github.com/kubevirt/containerized-data-importer) (Containerized Data Importer) installed
- Network connectivity from K8s worker nodes to Nutanix Prism Central (port 9440)

## Technology Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.22+ |
| Operator Framework | controller-runtime / kubebuilder v4 |
| Nutanix API Client | Custom `net/http` (not the auto-generated Go SDK) |
| Disk Transfer | CDI DataVolumes (HTTP source) |
| Warm Transfer | Custom transfer pods + Nutanix CBT API |
| Validation | Go-based with optional K8s client for target-side checks |
| CLI | kubectl plugin (`kubectl vma`) |
| CI/CD | GitHub Actions |
| Container Registry | ghcr.io |

## How This Project Was Built

This project was designed entirely through AI-assisted prompt engineering:

1. **Research** -- Claude Code (Opus 4.6) ran 4 parallel research agents to analyze Forklift source code, Nutanix APIs, migration techniques, and provider implementation patterns
2. **PRD v1** -- Written following the [snarktank PRD template](https://github.com/snarktank/ai-dev-tasks/blob/main/create-prd.md) with [Ralph](https://github.com/snarktank/ralph)-compatible story decomposition
3. **Critic Loop 1** (Claude) -- Ralph compatibility review: story sizing, dependencies, build viability
4. **Critic Loop 2** (Claude) -- Technical accuracy review: API correctness, KubeVirt/CDI patterns, security
5. **Critic Loop 3** (OpenAI Codex/GPT-5.4) -- Independent external review. Verdict: "not a product plan" -- led to warm migration being gated as experimental and multiple architectural fixes
6. **Critic Loop 4** (Claude) -- Execution simulation: story-by-story file count and dependency verification

Full prompt log and process details: [`docs/how-we-built-this.md`](docs/how-we-built-this.md)

## Known Constraints & Risks

| Constraint | Status | Impact |
|-----------|--------|--------|
| No Nutanix cluster access | Unverified | All Nutanix API behavior is based on documentation, not testing |
| Warm migration delta data source | CRITICAL RISK | CBT API returns block metadata, not data. Range header support unconfirmed |
| Cold export path | Unverified | Image creation from recovery point vDisk may need clone step |
| PE authentication | Unverified | PC credentials may not propagate to PE API calls |
| CDI network access | Deployment prereq | CDI pods must reach Prism Central port 9440 |

## Contributing

This project is not yet at the implementation stage. Once Ralph completes the initial build:

1. Fork the repo
2. Create a feature branch
3. Run `make build && make test && make lint`
4. Submit a PR

## License

Apache License 2.0. See [LICENSE](LICENSE).
