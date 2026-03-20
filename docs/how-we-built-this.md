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

---

## Phase 5: Ready to Run

At this point the project has:
- 7 research documents (~3,000 lines)
- 1 PRD (v4, 22 stories, 72+ requirements, 4 critic rounds)
- `scripts/ralph-ci.sh` with CI verification gate and full telemetry
- Comprehensive README with execution instructions
- Full prompt log in `docs/how-we-built-this.md`
- Public repo at https://github.com/nctiggy/nutanix-vma

Next step: convert the PRD to `prd.json` and run `./scripts/ralph-ci.sh 30`.

---

## Takeaways

1. **Research first, PRD second**: The 7 research documents (~3,000 lines) took significant time but saved far more by catching issues early. The KVM-to-KVM insight shaped the entire architecture.

2. **Multi-model critic loops work**: Claude found different issues than Codex. Internal critics caught Ralph-specific execution problems. Codex caught architectural assumptions and missing requirements. Using both gave better coverage than either alone.

3. **The Codex review was the most impactful**: Its "not a product plan" verdict and the warm-migration gating recommendation were the most significant changes. Having a different AI model review your work catches blind spots.

4. **Story sizing is the hardest part of Ralph PRDs**: 4 of the 7 final-round issues were about stories being too large or having incorrect dependencies. Every story must produce code that compiles and passes tests -- this is a much harder constraint than traditional sprint planning.

5. **Assumptions must be flagged, not hidden**: The biggest technical risk (warm migration delta data source) was in the research from the start but only properly flagged after Codex called it out. Unverified assumptions in PRDs become bugs in code.
