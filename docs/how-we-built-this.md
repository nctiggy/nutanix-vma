# How We Built This PRD

This document captures the full prompt-driven workflow used to design the Nutanix VMA project -- from initial idea to production-ready PRD. The entire process was a conversation between a human (Craig) and Claude Code (Opus 4.6), with an external review pass from OpenAI Codex (GPT-5.4).

**Total time**: ~1 session
**Tools used**: Claude Code (CLI), OpenAI Codex (CLI), GitHub CLI
**Approach**: Research-first, multi-critic iterative refinement, Ralph-compatible story decomposition

---

## Phase 1: Research

### Prompt 1 -- Initial Research Request

> Ok, I want to build a tool similar to kubevirt forklift that would enable migrating nutanix vm's (maybe nutanix vms are already in the correct format, I am not sure). Can you research this. what could this look like what would it take, what are the details? I really would want to mimic how forklift manages this. Take a lot of time researching how to do this. I should gain access to a nutanix cluster at some point but for now we need to work off of theory. ultimately I will want to build this as a project with a prd and use ralph to build the first draft but do not worry about this right now. First I want you to look into this and research this. thinking through theory, process, order of operations, gotchas, risks, etc

**What Claude did**: Launched 4 parallel research agents:
1. KubeVirt Forklift architecture deep-dive (CRDs, controllers, migration flow, provider model)
2. Nutanix AHV internals (disk format, storage architecture, APIs, SDKs)
3. VM migration techniques (virt-v2v, qemu-img, CDI, guest OS considerations)
4. Forklift provider implementation analysis (Go interfaces, code structure, extensibility)

**Key finding**: Nutanix AHV is KVM-based, so guest OS conversion (the hardest part of VMware->KubeVirt) is unnecessary. Disks are raw files, guests already use VirtIO drivers.

### Prompt 2 -- Document the Research

> note down your findings in this dir

**What Claude did**: Wrote 7 research documents totaling ~3,000 lines:
- `research/00-executive-summary.md`
- `research/01-forklift-architecture.md`
- `research/02-nutanix-ahv-internals.md`
- `research/03-migration-pipeline.md`
- `research/04-proposed-architecture.md`
- `research/05-gotchas-risks-questions.md`
- `research/06-vm-metadata-mapping.md`

---

## Phase 2: PRD Creation

### Prompt 3 -- PRD Request with References

> Consider https://github.com/snarktank/ai-dev-tasks/blob/main/create-prd.md
> https://github.com/snarktank/ai-dev-tasks/blob/main/generate-tasks.md
> https://github.com/snarktank/ralph
>
> Lets build a PRD that works with ralph how these repos define. As we build out a PRD for this project, ask questions, run the PRD through critic loops. Lastly run it through codex via cli (I am authed already) to get critic feedback from codex as a third party set of eyes.

**What Claude did**: Fetched all three reference repos to understand:
- The PRD template format (snarktank/ai-dev-tasks `create-prd.md`)
- The task generation format (snarktank/ai-dev-tasks `generate-tasks.md`)
- How Ralph works (snarktank/ralph -- autonomous AI agent loop with fresh context per iteration)

Then asked 5 clarifying questions per the template:
1. Scope for first PRD? (all phases vs. incremental)
2. Target K8s distribution?
3. API client strategy? (Go SDK vs. custom HTTP)
4. Open source or private?
5. How to handle no Nutanix cluster?

### Prompt 4 -- Answers to Clarifying Questions

> 1. Do everything. Keep it all in one PRD for the initial try.
> 2. It should just work with whatever upstream vanilla Kubernetes we deploy, so it shouldn't matter.
> 3. Use your best guess, whatever you think the right way to do this is, so that it's the most compatible.
> 4. Let's do that. We're gonna make this open source. I'd also like you to integrate GitHub, so I'd like you to push this to GitHub. I would like you to include:
>    - Testing, Unit test, Integration test, At some point deployment test
>    - I need you to do these tests via pushes to the GitHub repo, so GitHub actions would be ideal.
>    - At some point, we can add GitHub runners to do actual tests with an actual Nutanix cluster and VMO cluster present.
>    - We need to really make sure that when we're done with this, this works, so we need every step of the way. We need a working product. It has to be able to execute, so you need to be able to execute it.
>    - You tell me what you need access to in order to make sure things work, like the web UI, all of that kind of stuff, all the CRDs, Logging API, whatever it is that we're gonna add into this. It needs to work at the end of every Ralph loop.
>    - You're gonna make sure that this is gonna be functional from a Ralph perspective, and then I guess the testing just needs to know if there's a Nutanix cluster or not, so maybe have a flag or something like that.
>    - Let's build it all in theory, and when we get that cluster, we will do some of the testing and debugging, maybe with a different Ralph loop.
>    - Maybe even you could help me build a PRD that is for once we have the Nutanix cluster, what we're gonna run through from a test perspective.
>    - I want you to build this as if you have access to one and just make assumptions along the way.
>    - Maybe you could even build some kind of mock-up of the API using the open API spec that they adhere to theoretically, so let's start there.
> 5. (included in 4 above)

**What Claude did**: Wrote PRD v1 (`tasks/prd-nutanix-vma.md`) with:
- 8 goals, 8 epics, 19 user stories
- 72 functional requirements across 10 sections
- 19 Ralph-compatible implementation stories
- Full directory structure, CRD specs, API response types
- Mock Nutanix API server design
- GitHub Actions CI/CD plan

---

## Phase 3: Critic Loops

### Critic Loop 1 -- Internal (Ralph Compatibility)

Claude launched an agent to review the PRD specifically for Ralph execution viability:
- Are stories sized for one AI context window?
- Are dependencies clear?
- Would `make build && make test` pass after each story?
- Is the execution order correct?

**Found 18 issues**, including:
- Stories 2, 8, 13 too large (6+ CRDs, 10 files, 12-phase pipeline)
- Priority ordering bug (Story 7 P1 needed before Story 8 P0)
- Interface growth undefined between Stories 3-7
- Missing dependencies between stories
- AGENTS.md content not specified

### Critic Loop 2 -- Internal (Technical Accuracy)

Claude launched an agent to review against the research documents:
- Nutanix API endpoints/versions correct?
- KubeVirt VM CR fields accurate?
- CDI DataVolume patterns right?
- Migration design technically sound?

**Found 36 issues**, including:
- CPU topology should preserve sockets/cores/threads, not flatten
- CDI HTTP Basic Auth uses `accessKeyId`/`secretKey`, not `username`/`password`
- Machine type should use KubeVirt aliases (`q35`, `pc`), not versioned strings
- Missing async task polling API endpoint
- Finalizers and owner references needed
- Warm migration delta data source underspecified

### PRD v2 Applied

All issues from critics 1 and 2 were applied:
- 5 stories split (2->2a/2b, 8->8a/8b, 13->13a/13b/13c)
- CPU topology preserved
- Story dependencies made explicit
- Priority ordering fixed
- Async task model added
- Nutanix API structs embedded
- AGENTS.md content drafted

### Critic Loop 3 -- External (OpenAI Codex / GPT-5.4)

The PRD was sent to Codex CLI for an independent third-party review:

```
codex exec "You are a senior technical reviewer and PRD critic. Read the PRD
file at tasks/prd-nutanix-vma.md and the research files in the research/
directory. Provide a detailed critique covering:
1) Are the Ralph stories correctly sized for one AI context window (max ~5-8
   files each)?
2) Are there any technical inaccuracies in the Nutanix API, KubeVirt, or CDI
   integration?
3) Are there missing requirements that would block a working implementation?
4) Are story dependencies correct and complete?
5) Is the warm migration design technically sound given the CBT API constraints?
6) Would this PRD produce a working product if implemented story-by-story?
Return specific actionable feedback. Be harsh and thorough. Do NOT write any
code or modify any files. Only read and analyze."
```

**Codex verdict**: "Would this PRD produce a working product story-by-story? **No.** It would likely produce a convincing mock-driven cold-migration prototype and a speculative warm-migration prototype, not a reliable product."

**Found 12 issues**, including:
1. Warm migration built on unverified assumption (CBT gives metadata, not block data; Range header support unknown)
2. Cold export path assumption unverified (image creation from recovery point vDisk may need clone step)
3. Story dependencies wrong (11 missing deps on 4/6, 13b missing dep on 5, 18 missing dep on 14)
4. Success metrics overclaim envtest capabilities
5. PE authentication underspecified
6. Target-side validation missing (StorageClass, NAD, CDI/KubeVirt installation)
7. CDI TLS/CA propagation missing
8. DIRECT_NIC not handled
9. API endpoint inconsistencies between PRD and research
10. Missing idempotency/restart requirements
11. Several stories still too large
12. "Reads like a mock-first prototype plan, not a product plan"

### PRD v3 Applied

All Codex findings addressed:
- Warm migration downgraded to EXPERIMENTAL/GATED
- Cold export fallback path added (clone -> image -> delete clone)
- Idempotency/resume requirements added
- CDI CA certificate propagation added
- Target-side validation added
- Story dependencies fixed
- Success metrics reworded
- PE auth flagged as unverified constraint

### Critic Loop 4 -- Internal (Ralph Execution Simulation)

Final pass: simulated executing each of the 22 stories and counted files, checked deps, verified build/test would pass.

**Found 7 issues** (3 critical, 4 moderate):
1. Story 1: kubebuilder generates ~20 files; `cmd/main.go` vs `cmd/operator/` path mismatch
2. Story 2b: Missing resource-tracking fields that Story 13a needs for idempotency
3. Story 3: Interface methods reference types from Story 4's `types.go`
4. Story 11: No instructions for obtaining KubeVirt/CDI CRD fixtures for envtest
5. Story 12: Ambiguous inventory access pattern
6. Story 13b: Too much scope with fallback export path
7. Story 10: Validator needs K8s client injection pattern specified

### PRD v4 Applied (Final)

All execution blockers fixed:
- kubebuilder install instructions + `cmd/main.go` path clarified
- Resource tracking fields added to Story 2b's CRD types
- Story 3 creates `types.go` with forward-declared types
- Story 11 specifies CRD fixture strategy for envtest
- Story 12 clarifies Plan controller creates its own Nutanix client
- Story 13b defers fallback export path to reduce scope
- Story 10 specifies `ValidationOptions` with optional K8s client

---

## Phase 4: GitHub + Final Artifacts

### Prompt 5 -- Push to GitHub

> Yeah, let's push to GitHub

**What Claude did**: Created public repo at https://github.com/nctiggy/nutanix-vma and pushed all commits.

### Prompt 6 -- Final Verification

> before we do anything else, let's run this through one more critical loop just to verify that we don't have any problems, any obvious issues, anything that would stop this Ralph loop from continuing to finish. Also make sure that there's no stories that are too big after the Codex review.

**What Claude did**: Ran the execution simulation (critic loop 4), applied remaining fixes, pushed v4.

### Prompt 7 -- Apply Codex Fixes

> Yeah, apply the fixes, please.

**What Claude did**: Applied all 12 Codex findings to the PRD (v3):
- Downgraded warm migration to EXPERIMENTAL/GATED
- Added cold export fallback path (clone -> image -> delete clone)
- Added idempotency/resume-after-restart requirements
- Added CDI CA certificate propagation
- Added target-side validation (StorageClass, NAD, CDI/KubeVirt existence)
- Added DIRECT_NIC SR-IOV warning
- Fixed 3 story dependency errors
- Reworded success metrics to not overclaim envtest capabilities
- Added PE auth and cold export path to Known Constraints

Committed as PRD v3, pushed to GitHub.

### Prompt 8 -- Push to GitHub + README

> Okay, let's make sure we push this, but also let's build up a pretty good README for this repo. Before we get going, I want you to take note of all of the prompts I've given you to get to this point and push that as part of the repo as well, so people can see how we built out this project. Maybe even include the prompt you gave Codex as part of it, just to give a perspective of what that looked like.

**What Claude did**:
- Created `docs/how-we-built-this.md` with the full prompt log and process documentation
- Wrote comprehensive `README.md` with architecture, migration modes, VM metadata mapping, project status, and repo structure
- Pushed both to GitHub

### Prompt 9 -- Add Ralph Execution Instructions

> In the readme, does it tell me how to run this from the command line with Ralph, based on the Ralph repo I gave you at the beginning?

**What Claude did**: Fetched Ralph's `ralph.sh` to understand exact CLI usage, then added a full "Running Ralph" section to the README:
- Prerequisites
- Step 1: Install Ralph
- Step 2: Convert PRD to prd.json
- Step 3: Run Ralph (`./scripts/ralph/ralph.sh --tool claude 30`)
- Monitoring progress commands

### Prompt 10 -- CI-Verified Ralph Loop

> And just to be clear, you're marking up documentation for how to use the snark tank git repo, Ralph, correct? And lastly on this Ralph PRD, are we having a push to GitHub after every loop?

**What Claude did**: Clarified that Ralph commits locally but does NOT push. Craig's response led to:

> It might be cool to have it be that when a Ralph step finishes and it thinks it's finished successfully, that it doesn't mark complete until it pushes to the repo in that test run and we watch using the GH tool to make sure that those GitHub actions are successful before we mark it complete. Then, if it doesn't mark it complete, we spin up a new cloud instance window to troubleshoot and try to get this to a passing state vs doing it all in one giant context window. My biggest fear would be running out of context and then getting into hallucination problems.

**What Claude did**: Built `scripts/ralph-ci.sh` -- a custom wrapper that uses GitHub Actions as an external verification oracle:

```
For each iteration:
  1. Claude Code implements the next incomplete story
  2. Runs make build && make test locally
  3. Commits and pushes to GitHub
  4. Watches the CI run via `gh run watch`
  5. IF CI passes  -> marks story complete
  6. IF CI fails   -> does NOT mark complete, writes CI failure
                      logs to progress.txt
  7. Next iteration: fresh context window reads failure logs and debugs
```

Key insight from Craig: "a fresh context window for debugging is a feature, not a bug" -- prevents hallucination cascades where a stuck context keeps trying the same broken approach.

Updated README with full documentation including ASCII flow diagram of the Ralph <-> GitHub <-> CI interaction.

### Prompt 11 -- Telemetry and Metrics

> The last thing that I'd love to see is a built-in way to keep track of:
> - time stamps
> - how many times we failed and had to redo an iteration loop
> - the overall, how long did this loop take from start to finish
> That would be really cool to look back at as a post-mortem and as people look back to this project to see how that all went down.

**What Claude did**: Added full telemetry to `ralph-ci.sh`:

- **`metrics.json`**: Machine-readable telemetry for every iteration -- timestamps (UTC), story ID, result (ci_pass/ci_fail/no_commit/ci_skipped), duration, CI run ID, attempt number, aggregates (first-pass rate, efficiency)
- **`metrics-report.md`**: Auto-generated human-readable report with summary table, per-story breakdown showing all attempts, failure timeline with GitHub Actions links, and efficiency analysis
- Report generates automatically on exit (normal, Ctrl+C, or error) via `trap EXIT`
- Both files committed to repo for post-mortem analysis

### Prompt 12 -- Update Prompt Log

> Did you update the prompt list so that we can see how we got to here?

**What Claude did**: Updated this document (`docs/how-we-built-this.md`) with Prompts 7-12 capturing the full conversation from PRD v3 fixes through CI-verified loop, telemetry, and this meta-update.

### Prompt 13 -- Convert PRD to prd.json

> Load the ralph skill and convert tasks/prd-nutanix-vma.md to prd.json

**What Claude did**: Fetched Ralph's `CLAUDE.md`, `prompt.md`, and `prd.json.example` to understand the exact schema. Converted all 23 stories from the PRD into `prd.json` with: id, title, full description (implementation details embedded), acceptanceCriteria, priority (1-23 execution order), passes: false, and notes (dependencies + warnings). Created `progress.txt` with empty Codebase Patterns section. Committed and pushed.

### Prompt 14 -- Ralph Loop Ended (Bug #1)

> The ralph loop just ended. why did that happen?

**What Claude did**: Investigated via `gh run list` and `gh run view --log-failed`. Found US-003 CI failed (6 lint errors: 3 errcheck + 3 unused). Discovered `ralph-ci.sh` bug #1: `set -euo pipefail` killed the script when `gh run watch --exit-status` returned non-zero. Added `|| true` and API-based conclusion check. Wrote failure context to `progress.txt`. Pushed fix.

### Prompt 15 -- Story Mismatch Question

> Its running currently but it looks like it ran a github action already for the prd (us-004) story that we are currently on. Is this correct?

**What Claude did**: Checked `gh run list` and explained the tracking mismatch -- Ralph fixed US-003 AND implemented US-004 in one context window, but `ralph-ci.sh` only tracked one story per iteration. The display lagged reality but work was flowing correctly.

### Prompt 16 -- Ralph Stopped Again (Bug #2)

> Ok, ralph stopped but it looks like tests in github actions failed.. is this right?

**What Claude did**: Investigated US-017 CI failure (2 staticcheck lint errors: deprecated `GetEventRecorderFor`). Discovered `ralph-ci.sh` bug #2: `pipefail` makes a pipe's exit code come from the first failing command (`gh run watch`), not the last (`tail`). `|| true` only caught `tail`'s exit code. Fixed by writing to temp file instead of piping. Wrote failure context to `progress.txt`. Pushed fix.

### Prompt 17 -- Are We Done?

> Are we done? What can I do to see the results?

**What Claude did**: Checked `gh run list`, `prd.json` status, `git log`, and verified all 23 stories had implementation commits with CI passing. Found 20/23 marked complete in `prd.json` (US-017/018/019 code was committed but not marked due to the crash). Provided full status table with CI attempts per story and instructions for building, deploying, and using the project.

### Prompt 18 -- Web UI Question

> As part of this, did we create a web frontend UI similar to forklift?

**What Claude did**: Confirmed no -- Web UI was explicitly listed as a non-goal in the PRD. Explained what UX was built (kubectl plugin, CRD status, Events, Prometheus metrics). Offered to add a web UI as a follow-up PRD/Ralph loop.

### Prompt 19 -- Post-Mortem Documentation

> Can you update the how we built this doc with the length of run time, how long each story took, where there were issues, etc? Anything else you think would be relevant

**What Claude did**: Reconstructed full timeline from `git log` timestamps. Added Phase 5 (Ralph Execution) and Phase 6 (Results) to this document with: minute-level timeline, overall stats (23 stories, 64 commits, 22,810 lines Go, 9.5h wall clock), per-story timing table, detailed failure analysis for all 3 failure scenarios, `ralph-ci.sh` bug documentation, code output breakdown, and 12 takeaways.

### Prompt 20 -- Metrics Data Question

> Did you use the metrics.json and metrics-report.md to help guide how long this ran for?

**What Claude did**: Admitted no -- initially reconstructed from `git log`. Found the metrics files existed with good data (24 iterations recorded). Updated the per-story timing table with actual `metrics.json` values (more accurate). Added a Telemetry System Performance section documenting what the auto-recording captured vs missed across the 3 runs.

### Prompt 21 -- Fix Metrics Overwrite + Update Prompts

> Can we fix the mechanism that overwrote the metrics and metrics-report? I want to use this mechanism again for feature requests. Also, have you updated all the prompts from where we last left off in the doc?

**What Claude did**: Fixed 3 issues in `ralph-ci.sh` metrics handling:
1. On resume, read `runStart` from existing `metrics.json` (not reset to "now") so total duration is calculated from the original start
2. Continue iteration numbering from where we left off (`ITERATION_OFFSET`)
3. Added `--reset` flag to archive old metrics and start fresh for a new feature run (`metrics-archive/` directory)

Updated this document with Prompts 13-21.

---

## Phase 5: Ralph Execution

### Timeline

Ralph started at **9:33 PM PDT** (2026-03-19) and the last story code was committed at **4:40 AM PDT** (2026-03-20). The CI verification for the final stories completed at **7:04 AM PDT** after two `ralph-ci.sh` restarts due to script bugs.

| Milestone | Time (PDT) | Elapsed |
|-----------|-----------|---------|
| Research + PRD design starts | ~5:30 PM Mar 19 | 0h |
| PRD v4 complete, pushed to GitHub | 9:33 PM Mar 19 | ~4h |
| `prd.json` created, Ralph loop starts | 9:41 PM Mar 19 | ~4h 10m |
| US-001 (Scaffolding) complete | 9:44 PM | +3m |
| US-002a (CRDs pt 1) complete | 9:50 PM | +9m |
| US-002b (CRDs pt 2) complete | 9:56 PM | +15m |
| US-003 CI fails (lint errors) -- loop crashes (bug #1) | 10:01 PM | +20m |
| Manual fix: `ralph-ci.sh` set -e bug patched | 10:15 PM | +34m |
| US-003 lint fixed + US-004 implemented in one iteration | 10:22 PM | +41m |
| US-005 through US-010 (6 stories, no failures) | 10:33 PM - 11:55 PM | +1h 32m - 2h 14m |
| US-011 (Provider Controller + envtest harness) | 12:11 AM Mar 20 | +2h 30m |
| US-012 (Plan Controller) | 12:35 AM | +2h 54m |
| US-013a/b/c (Migration Controller, 3 parts) | 12:51 AM - 1:29 AM | +3h 10m - 3h 48m |
| US-014 (Warm Migration) | 1:54 AM | +4h 13m |
| US-015 (Hook System) | 2:27 AM | +4h 46m |
| US-016 (kubectl plugin) | 2:47 AM | +5h 06m |
| US-017 CI fails (deprecated API) -- loop crashes (bug #2) | 4:06 AM | +6h 25m |
| US-017/018/019 code committed before crash | 4:06 AM - 4:40 AM | +6h 25m - 6h 59m |
| Manual fix: `ralph-ci.sh` pipefail bug patched | 6:22 AM | +8h 41m |
| US-016 integration test flakiness fixed, marked complete | 7:04 AM | +9h 23m |
| **All 23 stories implemented** | **4:40 AM** | **~7h coding** |
| **All CI verified** | **7:04 AM** | **~9.5h total** |

### Overall Stats

| Metric | Value |
|--------|-------|
| Total stories | 23 |
| Stories passing CI on first attempt | 19 of 23 (83%) |
| Stories requiring retry | 4 (US-003, US-016, US-017, US-016 again) |
| Total Ralph iterations | ~28 |
| Total commits | 64 |
| Implementation commits | 23 |
| CI verification commits | 21 |
| Bug fix / debug commits | 8 |
| Progress.txt update commits | 12 |
| Wall clock: PRD design | ~4 hours |
| Wall clock: Ralph coding | ~7 hours |
| Wall clock: including CI waits + script fixes | ~9.5 hours |
| `ralph-ci.sh` crashes | 2 (both `set -eo pipefail` issues) |

### Code Output

| Metric | Lines |
|--------|-------|
| Go source files | 72 |
| Production Go code (non-test, non-generated) | 9,019 |
| Test code | 13,791 |
| Generated code (deepcopy) | 863 |
| Config/YAML (CRDs, RBAC, deployment) | 1,975 |
| **Total Go code** | **22,810** |
| Test-to-production ratio | **1.53:1** |

### Per-Story Timing

Derived from two sources: `metrics.json` (auto-recorded telemetry from `ralph-ci.sh` -- available for the second run onward) and `git log` timestamps (for the first run before the script crash). Duration includes Claude thinking time + CI wait time. Where `metrics.json` data is available, those numbers are used as they're more accurate than git-log estimates.

| Story | Title | Duration | CI Attempts | Notes |
|-------|-------|----------|-------------|-------|
| US-001 | Project Scaffolding | 11m 11s | 1 | kubebuilder init + deps |
| US-002a | CRD Types (Provider/NetworkMap/StorageMap) | 5m 53s | 1 | |
| US-002b | CRD Types (Plan/Migration/Hook) | 5m 53s | 1 | |
| US-003 | API Client -- Core + Auth | 9m 19s | 2 | **FAIL** (run 1): 3 errcheck + 3 unused lint errors. Loop crashed (bug #1). Fixed in run 2. |
| US-004 | API Client -- VM Operations | 10m 52s | 1 | |
| US-005 | API Client -- Snapshots & Images | 9m 3s | 1 | |
| US-006 | API Client -- Networking/Storage/Cluster | 11m 35s | 1 | |
| US-007 | API Client -- CBT/CRT | 10m 58s | 1 | Complex two-step flow + JWT + pagination |
| US-008a | Mock Server -- Core | 14m 10s | 1 | 8 files (at budget limit) |
| US-008b | Mock Server -- Snapshots/Images/CBT | 16m 2s | 1 | + standalone binary |
| US-009 | VM Builder | 17m 42s | 1 | Comprehensive test fixtures |
| US-010 | Validation Engine | 18m 14s | 1 | 13 validation rules |
| US-011 | Provider Controller + envtest | 23m 45s | 1 | Most complex setup (envtest + mock + CRD fixtures) |
| US-012 | Plan Controller | 16m 0s | 1 | |
| US-013a | Migration Controller -- Framework | 22m 5s | 1 | State machine + idempotency |
| US-013b | Migration Controller -- Disk Transfer | 15m 39s | 1 | CDI DataVolumes + credential secrets + CA propagation |
| US-013c | Migration Controller -- VM Creation | 26m 18s | 1 | + full integration test |
| US-014 | Warm Migration (Experimental) | 32m 19s | 1 | Longest story -- precopy loop + delta transfer |
| US-015 | Hook System | 20m 14s | 1 | |
| US-016 | kubectl Plugin | 39m 42s* | 5 | **FAIL x4**: 3 no-commit iterations + 1 CI fail (flaky integration test, finalizer race). Fixed on attempt 5. *Duration is for the passing attempt; total wall time across all 5 attempts was ~1h 59m. |
| US-017 | Observability | ~19m | 2 | **FAIL**: Deprecated `GetEventRecorderFor` (staticcheck SA1019). Loop crashed (bug #2). Fixed in subsequent run. |
| US-018 | E2E Test Framework | ~6m | 1 | Fast -- mostly test scaffolding |
| US-019 | Documentation & Release | ~5m | 1 | README rewrite + CONTRIBUTING.md + release workflow |

*Timing for US-001 through US-016 from `metrics.json` (auto-recorded telemetry). US-017/018/019 estimated from git log timestamps (script crashed before recording).*

### Telemetry System Performance

The `ralph-ci.sh` telemetry system (`metrics.json` + `metrics-report.md`) was designed to auto-record all this data. In practice:

- **Run 1** (US-001 through US-003 failure): Telemetry initialized but the script crashed on the first CI failure (bug #1: `set -e` + `gh run watch`). The `trap EXIT` finalizer ran but only captured 3 iterations before the crash.
- **Run 2** (US-003 retry through US-016): Telemetry worked correctly for 21 iterations. Captured accurate per-iteration timing, CI run IDs, attempt numbers, and pass/fail results. Crashed again on US-017 CI failure (bug #2: `pipefail` in pipe).
- **Run 3** (US-017 through US-019): Code was committed before the crash but telemetry wasn't captured for these stories.

**Net result**: `metrics.json` has accurate data for 24 of ~28 total iterations. The per-story timing table above uses `metrics.json` data where available (US-001 through US-016) and git-log estimates for the rest.

**Key `metrics.json` stats** (from the auto-generated `metrics-report.md`):
- Total iterations recorded: 24
- Stories completed (in this run): 20
- CI passes: 20, CI failures: 1, No-commit iterations: 3
- First-pass CI rate: 95% (within this run -- doesn't count the US-003 failure from run 1)
- Average iteration duration: 17m 19s
- Average story duration (including retries): 20m 47s
- Iteration efficiency: 83% (stories completed / total iterations)

The 3 "no-commit" iterations were all on US-016 -- Ralph spawned fresh contexts that read the flaky test failure but couldn't produce a working fix, resulting in no git commits. On the 4th attempt it committed a fix that still failed CI, and the 5th attempt finally resolved the finalizer race condition.

### Failures and What Caused Them

#### Failure 1: US-003 -- Lint Errors (Iteration 4)

**What failed**: `errcheck` (3 unchecked `resp.Body.Close()` return values) and `unused` (3 symbols: `pollTask`, `defaultPollInterval`, `defaultPollTimeout` -- declared but not called by any exported code yet).

**Why Ralph missed it locally**: Ralph ran `make build && make test` which passed, but didn't run `make lint`. The lint errors were only caught by the CI pipeline.

**How it was fixed**: Next iteration (fresh context) read the failure logs from `progress.txt`, changed `resp.Body.Close()` to `_ = resp.Body.Close()`, and had `pollTaskWithOptions` call `pollTask` internally to eliminate the unused warning.

**Infrastructure issue**: This failure also exposed **bug #1 in ralph-ci.sh** -- `set -euo pipefail` caused the script to exit when `gh run watch --exit-status` returned non-zero. The failure handler never ran. Had to manually patch the script and write failure context to `progress.txt`.

#### Failure 2: US-016 -- Flaky Integration Test (Iterations ~18-22)

**What failed**: Integration test for the kubectl plugin was timing-dependent. A race condition between the finalizer update and the status update in the migration controller caused intermittent test failures. The test would sometimes see the migration in a partially-updated state.

**Why it was flaky**: The migration controller was doing two separate updates (finalizer + status) in the same reconcile. When envtest processed them, the test's `Eventually` block sometimes caught the intermediate state.

**How it was fixed**: After 4 failed attempts, the 5th iteration split the finalizer update and status update into separate reconcile passes. This is a known controller-runtime pattern -- you can't update both `metadata` (finalizer) and `status` in the same API call.

**Impact**: This was the most expensive failure -- 5 attempts. Each attempt spawned a fresh Claude context that tried different approaches before landing on the correct fix.

#### Failure 3: US-017 -- Deprecated API (Iteration ~25)

**What failed**: `staticcheck SA1019` flagged `mgr.GetEventRecorderFor()` as deprecated. The replacement is `mgr.GetEventRecorder()` (no "For" suffix, no name argument).

**Why Ralph used the deprecated API**: The controller-runtime version in use had both methods available, but the newer one was preferred. Ralph's training data likely included examples using the older API.

**How it was fixed**: Next iteration (fresh context) read the failure context, did a simple find-and-replace in both `migration_controller.go` and `test/integration/suite_test.go`.

**Infrastructure issue**: This also exposed **bug #2 in ralph-ci.sh** -- the same `pipefail` issue as bug #1, but in a different code path. The pipe `gh run watch | tail -20 || true` still failed because `pipefail` propagates the first command's exit code through the pipe. Fixed by writing to a temp file instead of piping.

### ralph-ci.sh Bugs Discovered During Execution

| Bug | Root Cause | Impact | Fix |
|-----|-----------|--------|-----|
| **Bug #1**: Script exits on CI failure instead of continuing | `set -euo pipefail` + `gh run watch --exit-status` returns non-zero | Loop dies silently; failure handler never runs; no logs written to progress.txt | Added `\|\| true` to `gh run watch` call |
| **Bug #2**: Same crash despite Bug #1 fix | `pipefail` makes pipe exit code come from `gh run watch` (first cmd), not `tail` (last cmd). `\|\| true` only catches `tail`'s exit. | Same as Bug #1 -- loop dies on CI failure | Replaced pipe with temp file + separate `tail` |

Both bugs had the same symptom (silent exit on CI failure) but different root causes. The lesson: `set -o pipefail` and `|| true` don't compose the way you'd expect when the failing command is not the last in a pipe.

---

## Phase 6: Results

### What Was Built

A fully functional Kubernetes operator for migrating Nutanix AHV VMs to KubeVirt:

- **6 CRDs**: NutanixProvider, NetworkMap, StorageMap, MigrationPlan, Migration, Hook
- **Custom Nutanix API client**: 20 methods covering VMs, snapshots, images, CBT, networking, storage, cluster discovery. Built on `net/http`, not the auto-generated SDK.
- **Mock Nutanix API server**: In-process test helper + standalone binary. Simulates all endpoints including async tasks, CBT, image downloads with Range headers.
- **VM metadata builder**: Translates Nutanix VM config to KubeVirt VirtualMachine spec (CPU topology, firmware, disks, NICs, machine type, name sanitization).
- **Validation engine**: 13 pre-flight rules with target-side validation (StorageClass, NAD, CDI/KubeVirt existence).
- **3 controllers**: Provider (inventory), Plan (validation), Migration (cold + warm pipeline with idempotent phases).
- **Transfer manager**: CDI DataVolume creation with credential secrets, CA propagation, progress tracking.
- **Warm migration**: CBT-based precopy loop with delta transfer (experimental).
- **Hook system**: Pre/post migration K8s Jobs with mounted context.
- **kubectl plugin**: `kubectl vma inventory/plan/migrate/status/cancel`.
- **Observability**: K8s Events, structured logging, Prometheus metrics.
- **E2E test framework**: Ginkgo suite gated behind `NUTANIX_E2E=true`.
- **CI/CD**: GitHub Actions (lint + build + test + integration), release workflow.

### What Remains

1. **Real Nutanix cluster validation** -- all API behavior is based on documentation, not testing
2. **Warm migration data source** -- need to confirm HTTP Range header support on Image Service
3. **Cold export fallback path** -- clone-from-recovery-point fallback not yet implemented (deferred from US-013b)
4. **PE authentication** -- need to verify if PC credentials propagate to PE API calls

---

## Takeaways

### Design Phase

1. **Research first, PRD second**: The 7 research documents (~3,000 lines) took significant time but saved far more by catching issues early. The KVM-to-KVM insight shaped the entire architecture.

2. **Multi-model critic loops work**: Claude found different issues than Codex. Internal critics caught Ralph-specific execution problems. Codex caught architectural assumptions and missing requirements. Using both gave better coverage than either alone.

3. **The Codex review was the most impactful**: Its "not a product plan" verdict and the warm-migration gating recommendation were the most significant changes. Having a different AI model review your work catches blind spots.

4. **Story sizing is the hardest part of Ralph PRDs**: 4 of the 7 final-round issues were about stories being too large or having incorrect dependencies. Every story must produce code that compiles and passes tests -- this is a much harder constraint than traditional sprint planning.

5. **Assumptions must be flagged, not hidden**: The biggest technical risk (warm migration delta data source) was in the research from the start but only properly flagged after Codex called it out. Unverified assumptions in PRDs become bugs in code.

### Execution Phase

6. **83% first-pass CI rate is good but not great**: 19 of 23 stories passed CI on the first attempt. The 4 failures were: lint errors (Ralph didn't run `make lint` locally), a flaky integration test (race condition in envtest), a deprecated API (training data lag), and the same flaky test again. Adding `make lint` to the Ralph prompt would have prevented 2 of the 4 failures.

7. **Fresh context windows for debugging really work**: When US-003 failed lint, the next iteration (fresh Claude context) read the failure logs and fixed it cleanly. This validates the core thesis: a new context without the prior attempt's assumptions debugs more efficiently than continuing in a stuck context.

8. **Flaky tests are the worst failure mode for Ralph**: US-016's integration test failed 4 times before the 5th iteration found the root cause (a controller-runtime pattern issue with finalizer + status in the same reconcile). Each failed attempt was a full fresh-context iteration. Flaky tests waste iterations because each fresh context tries a different hypothesis.

9. **The CI verification gate caught real problems**: Without it, Ralph would have marked US-003 and US-017 as complete despite lint failures. The "push -> verify CI -> then mark complete" pattern ensures the codebase is actually green, not just locally green.

10. **Infrastructure code (the wrapper script) needs testing too**: `ralph-ci.sh` had 2 bugs in a 300-line bash script, both caused by `set -o pipefail` interacting with `|| true` in non-obvious ways. The lesson: test your test infrastructure. Both bugs silently killed the loop when CI failed -- exactly the scenario the script was designed to handle.

11. **Ralph sometimes does extra work**: In one iteration, Claude fixed US-003's lint errors AND implemented US-004 in the same context window. The script only tracked one story per iteration, causing a tracking mismatch. Not a big problem (Claude is smart enough to skip already-implemented work), but the script's display lagged reality.

12. **~23,000 lines of Go in ~7 hours is remarkable**: Even accounting for the ~60% test code ratio, Ralph produced ~9,000 lines of production code (operator, API client, mock server, builder, validation, CLI) in a single overnight session. The code compiles, passes lint, and passes integration tests. The question is whether it works against real infrastructure -- that's the next phase.
