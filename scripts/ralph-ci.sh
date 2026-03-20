#!/usr/bin/env bash
#
# ralph-ci.sh -- Ralph loop with GitHub Actions CI verification and telemetry
#
# Instead of marking stories complete after local tests pass, this wrapper:
# 1. Runs Ralph's AI tool (Claude Code) to implement a story
# 2. Pushes to GitHub
# 3. Watches the CI run via `gh run watch`
# 4. Only marks the story complete if CI passes
# 5. If CI fails, leaves the story incomplete so the next iteration debugs it
# 6. Records full telemetry to metrics.json for post-mortem analysis
#
# Usage:
#   ./scripts/ralph-ci.sh [max_iterations]
#   ./scripts/ralph-ci.sh 30
#
# Prerequisites:
#   - Claude Code installed and authenticated
#   - gh CLI installed and authenticated
#   - jq installed
#   - Git repo with remote configured
#
# Outputs:
#   - prd.json          -- story completion status
#   - progress.txt      -- learnings and CI failure logs
#   - metrics.json      -- full telemetry (timestamps, durations, pass/fail per iteration)
#   - metrics-report.md -- human-readable summary generated at the end

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MAX_ITERATIONS="${1:-30}"
TOOL="claude"
METRICS_FILE="$REPO_ROOT/metrics.json"
METRICS_REPORT="$REPO_ROOT/metrics-report.md"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

log() { echo -e "${BLUE}[ralph-ci]${NC} $1"; }
success() { echo -e "${GREEN}[ralph-ci]${NC} $1"; }
warn() { echo -e "${YELLOW}[ralph-ci]${NC} $1"; }
error() { echo -e "${RED}[ralph-ci]${NC} $1"; }
metric() { echo -e "${CYAN}[metrics]${NC} $1"; }

# Check prerequisites
command -v claude >/dev/null 2>&1 || { error "claude CLI not found. Install: npm install -g @anthropic-ai/claude-code"; exit 1; }
command -v gh >/dev/null 2>&1 || { error "gh CLI not found. Install: brew install gh"; exit 1; }
command -v jq >/dev/null 2>&1 || { error "jq not found. Install: brew install jq"; exit 1; }

# Verify gh is authenticated
gh auth status >/dev/null 2>&1 || { error "gh CLI not authenticated. Run: gh auth login"; exit 1; }

cd "$REPO_ROOT"

# Ensure prd.json exists
if [ ! -f "prd.json" ]; then
    error "prd.json not found in repo root. Convert your PRD first:"
    error "  Open Claude Code and run: Load the ralph skill and convert tasks/prd-nutanix-vma.md to prd.json"
    exit 1
fi

# Ensure progress.txt exists
touch progress.txt

# ============================================================
# Telemetry: Initialize metrics.json
# ============================================================
RUN_START=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
RUN_START_EPOCH=$(date +%s)

# Initialize or load existing metrics
if [ ! -f "$METRICS_FILE" ]; then
    cat > "$METRICS_FILE" <<INIT
{
  "runStart": "$RUN_START",
  "runEnd": null,
  "totalDurationSeconds": null,
  "totalDurationHuman": null,
  "maxIterations": $MAX_ITERATIONS,
  "totalIterations": 0,
  "storiesCompleted": 0,
  "storiesRemaining": 0,
  "totalCIRuns": 0,
  "ciPasses": 0,
  "ciFails": 0,
  "ciSkipped": 0,
  "noCommitIterations": 0,
  "firstPassRate": null,
  "iterations": []
}
INIT
    metric "Initialized metrics.json"
else
    metric "Resuming with existing metrics.json"
fi

# Helper: record an iteration to metrics.json
record_iteration() {
    local iter_num="$1"
    local story_id="$2"
    local story_title="$3"
    local result="$4"        # "ci_pass", "ci_fail", "no_commit", "ci_skipped", "push_fail"
    local ci_run_id="$5"
    local iter_start="$6"
    local iter_end="$7"
    local iter_start_epoch="$8"
    local iter_end_epoch="$9"
    local attempt="${10:-1}"

    local duration=$((iter_end_epoch - iter_start_epoch))
    local duration_human
    if [ $duration -ge 3600 ]; then
        duration_human="$((duration / 3600))h $((duration % 3600 / 60))m $((duration % 60))s"
    elif [ $duration -ge 60 ]; then
        duration_human="$((duration / 60))m $((duration % 60))s"
    else
        duration_human="${duration}s"
    fi

    local new_entry
    new_entry=$(jq -n \
        --argjson iteration "$iter_num" \
        --arg storyId "$story_id" \
        --arg storyTitle "$story_title" \
        --arg result "$result" \
        --arg ciRunId "$ci_run_id" \
        --arg startTime "$iter_start" \
        --arg endTime "$iter_end" \
        --argjson durationSeconds "$duration" \
        --arg durationHuman "$duration_human" \
        --argjson attempt "$attempt" \
        '{
            iteration: $iteration,
            storyId: $storyId,
            storyTitle: $storyTitle,
            result: $result,
            ciRunId: (if $ciRunId == "" then null else $ciRunId end),
            startTime: $startTime,
            endTime: $endTime,
            durationSeconds: $durationSeconds,
            durationHuman: $durationHuman,
            attemptNumber: $attempt
        }')

    # Append to iterations array and update counters
    jq --argjson entry "$new_entry" --arg result "$result" '
        .iterations += [$entry] |
        .totalIterations += 1 |
        if $result == "ci_pass" then .ciPasses += 1 | .totalCIRuns += 1 | .storiesCompleted += 1
        elif $result == "ci_fail" then .ciFails += 1 | .totalCIRuns += 1
        elif $result == "ci_skipped" then .ciSkipped += 1 | .storiesCompleted += 1
        elif $result == "no_commit" then .noCommitIterations += 1
        else .
        end
    ' "$METRICS_FILE" > "${METRICS_FILE}.tmp" && mv "${METRICS_FILE}.tmp" "$METRICS_FILE"
}

# Helper: compute attempt number for a story (how many times we've tried it)
get_attempt_number() {
    local story_id="$1"
    local prev_attempts
    prev_attempts=$(jq --arg sid "$story_id" '[.iterations[] | select(.storyId == $sid)] | length' "$METRICS_FILE" 2>/dev/null || echo "0")
    echo $((prev_attempts + 1))
}

# Helper: finalize metrics at the end
finalize_metrics() {
    local run_end
    local run_end_epoch
    run_end=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    run_end_epoch=$(date +%s)
    local total_duration=$((run_end_epoch - RUN_START_EPOCH))

    local total_human
    if [ $total_duration -ge 3600 ]; then
        total_human="$((total_duration / 3600))h $((total_duration % 3600 / 60))m $((total_duration % 60))s"
    elif [ $total_duration -ge 60 ]; then
        total_human="$((total_duration / 60))m $((total_duration % 60))s"
    else
        total_human="${total_duration}s"
    fi

    local remaining
    remaining=$(jq '[.userStories[] | select(.status != "completed")] | length' prd.json 2>/dev/null || echo "0")

    jq \
        --arg runEnd "$run_end" \
        --argjson totalDuration "$total_duration" \
        --arg totalHuman "$total_human" \
        --argjson remaining "$remaining" \
        '
        .runEnd = $runEnd |
        .totalDurationSeconds = $totalDuration |
        .totalDurationHuman = $totalHuman |
        .storiesRemaining = $remaining |
        .firstPassRate = (if .totalCIRuns > 0 then
            ((.ciPasses / .totalCIRuns * 100) | floor | tostring) + "%"
        else "N/A" end)
        ' "$METRICS_FILE" > "${METRICS_FILE}.tmp" && mv "${METRICS_FILE}.tmp" "$METRICS_FILE"

    # Generate the human-readable report
    generate_report
}

# Helper: generate metrics-report.md
generate_report() {
    local data
    data=$(cat "$METRICS_FILE")

    cat > "$METRICS_REPORT" <<REPORT
# Ralph CI Run -- Metrics Report

Generated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")

## Summary

| Metric | Value |
|--------|-------|
| Run Start | $(echo "$data" | jq -r '.runStart') |
| Run End | $(echo "$data" | jq -r '.runEnd // "in progress"') |
| Total Duration | $(echo "$data" | jq -r '.totalDurationHuman // "in progress"') |
| Total Iterations | $(echo "$data" | jq -r '.totalIterations') |
| Stories Completed | $(echo "$data" | jq -r '.storiesCompleted') |
| Stories Remaining | $(echo "$data" | jq -r '.storiesRemaining') |
| CI Passes | $(echo "$data" | jq -r '.ciPasses') |
| CI Failures | $(echo "$data" | jq -r '.ciFails') |
| CI Skipped (pre-CI) | $(echo "$data" | jq -r '.ciSkipped') |
| No-Commit Iterations | $(echo "$data" | jq -r '.noCommitIterations') |
| First-Pass CI Rate | $(echo "$data" | jq -r '.firstPassRate // "N/A"') |

## Per-Story Breakdown

REPORT

    # Group iterations by story and show attempts
    echo "$data" | jq -r '
        .iterations | group_by(.storyId) | .[] |
        "### " + .[0].storyTitle + " (" + .[0].storyId + ")\n" +
        "| Attempt | Result | Duration | CI Run | Time |\n" +
        "|---------|--------|----------|--------|------|\n" +
        (map(
            "| " + (.attemptNumber | tostring) +
            " | " + (if .result == "ci_pass" then "PASS" elif .result == "ci_fail" then "**FAIL**" elif .result == "ci_skipped" then "SKIP (pre-CI)" else .result end) +
            " | " + .durationHuman +
            " | " + (if .ciRunId then "[" + .ciRunId + "](../../actions/runs/" + .ciRunId + ")" else "--" end) +
            " | " + .startTime + " |"
        ) | join("\n")) + "\n"
    ' >> "$METRICS_REPORT"

    # Add failure timeline
    local fail_count
    fail_count=$(echo "$data" | jq '[.iterations[] | select(.result == "ci_fail")] | length')

    if [ "$fail_count" -gt 0 ]; then
        cat >> "$METRICS_REPORT" <<FAIL_HEADER

## Failure Timeline

| Iteration | Story | Attempt | Duration | CI Run |
|-----------|-------|---------|----------|--------|
FAIL_HEADER
        echo "$data" | jq -r '
            .iterations[] | select(.result == "ci_fail") |
            "| " + (.iteration | tostring) +
            " | " + .storyTitle +
            " | " + (.attemptNumber | tostring) +
            " | " + .durationHuman +
            " | [" + .ciRunId + "](../../actions/runs/" + .ciRunId + ") |"
        ' >> "$METRICS_REPORT"
    fi

    # Add efficiency analysis
    cat >> "$METRICS_REPORT" <<EFFICIENCY

## Efficiency Analysis

EFFICIENCY

    echo "$data" | jq -r '
        "- **Total iterations used**: " + (.totalIterations | tostring) + " of " + (.maxIterations | tostring) + " max",
        "- **Productive iterations** (resulted in story completion): " + (.storiesCompleted | tostring),
        "- **Debug iterations** (CI failures requiring retry): " + (.ciFails | tostring),
        "- **Wasted iterations** (no commits produced): " + (.noCommitIterations | tostring),
        "- **Iteration efficiency**: " + (if .totalIterations > 0 then ((.storiesCompleted / .totalIterations * 100) | floor | tostring) + "%" else "N/A" end) +
            " (stories completed / total iterations)",
        "- **Average iteration duration**: " + (if .totalIterations > 0 then
            ((.iterations | map(.durationSeconds) | add) / .totalIterations | floor | tostring) + "s"
        else "N/A" end),
        "- **Average story duration** (including retries): " + (if .storiesCompleted > 0 then
            ((.iterations | map(.durationSeconds) | add) / .storiesCompleted | floor | tostring) + "s"
        else "N/A" end)
    ' >> "$METRICS_REPORT"

    metric "Report written to $METRICS_REPORT"
}

# ============================================================
# Main Loop
# ============================================================

log "Starting Ralph CI loop (max $MAX_ITERATIONS iterations)"
log "Stories will only be marked complete after GitHub Actions CI passes"
log "Telemetry recording to metrics.json"
echo ""

# Trap to finalize metrics on exit (normal or error)
trap finalize_metrics EXIT

for i in $(seq 1 "$MAX_ITERATIONS"); do
    echo ""
    log "=========================================="
    log "Iteration $i / $MAX_ITERATIONS"
    log "=========================================="

    ITER_START=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    ITER_START_EPOCH=$(date +%s)

    # Check if all stories are complete
    INCOMPLETE=$(jq '[.userStories[] | select(.status != "completed")] | length' prd.json 2>/dev/null || echo "0")
    if [ "$INCOMPLETE" = "0" ]; then
        success "All stories complete! Ralph is done."
        exit 0
    fi

    STORY_ID=$(jq -r '[.userStories[] | select(.status != "completed")][0].id // "unknown"' prd.json 2>/dev/null)
    STORY_TITLE=$(jq -r '[.userStories[] | select(.status != "completed")][0].title // "unknown"' prd.json 2>/dev/null)
    ATTEMPT=$(get_attempt_number "$STORY_ID")

    log "Story: $STORY_TITLE ($STORY_ID)"
    log "Attempt: $ATTEMPT"
    log "Incomplete stories remaining: $INCOMPLETE"

    # Record commit count before Ralph runs
    COMMIT_BEFORE=$(git rev-list --count HEAD 2>/dev/null || echo "0")

    # Run Claude Code with the Ralph prompt
    log "Launching Claude Code..."
    RALPH_PROMPT=$(cat <<'PROMPT'
You are Ralph, an autonomous coding agent. Read prd.json and progress.txt to understand the project state.

Find the highest-priority incomplete story in prd.json and implement it.

Rules:
1. Read AGENTS.md for project conventions and build commands.
2. Implement the story completely -- all files, all tests.
3. Run `make build && make test` to verify locally.
4. If local checks pass: `git add` the relevant files and `git commit` with a descriptive message.
5. DO NOT mark the story as "completed" in prd.json yet -- the CI pipeline will verify first.
6. DO NOT run `git push` -- the wrapper script handles that.
7. Update progress.txt with what you did, any issues encountered, and patterns discovered.
8. If you are debugging a story that failed CI in a previous iteration, read progress.txt for the failure context and CI logs.

If there is nothing to implement (all stories complete), output: <promise>COMPLETE</promise>
PROMPT
)

    # Run Claude -- write to file first to avoid pipefail issues
    echo "$RALPH_PROMPT" | claude --dangerously-skip-permissions --print > "/tmp/ralph-iteration-$i.log" 2>&1 || true
    cat "/tmp/ralph-iteration-$i.log"

    ITER_END=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    ITER_END_EPOCH=$(date +%s)

    # Check if Ralph signaled completion
    if grep -q "<promise>COMPLETE</promise>" "/tmp/ralph-iteration-$i.log" 2>/dev/null; then
        success "Ralph signaled all stories complete."
        exit 0
    fi

    # Check if Ralph made any commits
    COMMIT_AFTER=$(git rev-list --count HEAD 2>/dev/null || echo "0")
    if [ "$COMMIT_AFTER" = "$COMMIT_BEFORE" ]; then
        warn "No new commits from this iteration. Ralph may have encountered an issue."
        warn "Check /tmp/ralph-iteration-$i.log for details."
        echo "---" >> progress.txt
        echo "Iteration $i [$(date -u +"%Y-%m-%dT%H:%M:%SZ")]: No commits produced for $STORY_TITLE (attempt $ATTEMPT). Check logs." >> progress.txt
        record_iteration "$i" "$STORY_ID" "$STORY_TITLE" "no_commit" "" "$ITER_START" "$ITER_END" "$ITER_START_EPOCH" "$ITER_END_EPOCH" "$ATTEMPT"
        continue
    fi

    NEW_COMMITS=$((COMMIT_AFTER - COMMIT_BEFORE))
    log "Ralph produced $NEW_COMMITS new commit(s)"

    # Push to GitHub
    log "Pushing to GitHub..."
    if ! git push 2>&1; then
        error "git push failed. Skipping CI verification."
        echo "---" >> progress.txt
        echo "Iteration $i [$(date -u +"%Y-%m-%dT%H:%M:%SZ")]: git push failed for $STORY_TITLE." >> progress.txt
        ITER_END=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
        ITER_END_EPOCH=$(date +%s)
        record_iteration "$i" "$STORY_ID" "$STORY_TITLE" "push_fail" "" "$ITER_START" "$ITER_END" "$ITER_START_EPOCH" "$ITER_END_EPOCH" "$ATTEMPT"
        continue
    fi

    # Wait a moment for GitHub to register the push
    sleep 3

    # Find the CI workflow run triggered by this push
    log "Waiting for GitHub Actions CI to start..."
    LATEST_SHA=$(git rev-parse HEAD)
    RUN_ID=""

    # Poll for the workflow run (GitHub may take a few seconds to create it)
    for attempt in $(seq 1 12); do
        RUN_ID=$(gh run list --commit "$LATEST_SHA" --workflow ci.yaml --json databaseId --jq '.[0].databaseId' 2>/dev/null || true)
        if [ -n "$RUN_ID" ] && [ "$RUN_ID" != "null" ]; then
            break
        fi
        sleep 5
    done

    if [ -z "$RUN_ID" ] || [ "$RUN_ID" = "null" ]; then
        warn "Could not find CI workflow run for commit $LATEST_SHA"
        warn "CI may not be configured yet (expected for Story 1)."
        warn "Marking story complete without CI verification."

        # For early stories (before CI exists), mark complete directly
        jq "(.userStories[] | select(.id == \"$STORY_ID\")).status = \"completed\"" prd.json > prd.json.tmp && mv prd.json.tmp prd.json 2>/dev/null || true
        echo "---" >> progress.txt
        echo "Iteration $i [$(date -u +"%Y-%m-%dT%H:%M:%SZ")]: $STORY_TITLE completed (no CI workflow found -- pre-CI story). Attempt $ATTEMPT." >> progress.txt
        git add prd.json progress.txt && git commit -m "Mark $STORY_ID complete (pre-CI)" && git push 2>/dev/null || true

        ITER_END=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
        ITER_END_EPOCH=$(date +%s)
        record_iteration "$i" "$STORY_ID" "$STORY_TITLE" "ci_skipped" "" "$ITER_START" "$ITER_END" "$ITER_START_EPOCH" "$ITER_END_EPOCH" "$ATTEMPT"
        continue
    fi

    log "CI run found: $RUN_ID"
    log "Watching CI run... (this may take a few minutes)"

    # Watch the CI run until it completes
    # NOTE: --exit-status returns non-zero on CI failure. With set -eo pipefail,
    # we must avoid the pipe failing the script. Write output to a temp file
    # instead of piping, then check conclusion via API.
    gh run watch "$RUN_ID" 2>&1 > "/tmp/ralph-ci-watch-$i.log" || true
    tail -20 "/tmp/ralph-ci-watch-$i.log"

    # Check the actual CI conclusion via the API (reliable regardless of exit code)
    CI_CONCLUSION=$(gh run view "$RUN_ID" --json conclusion --jq '.conclusion' 2>/dev/null || echo "unknown")

    if [ "$CI_CONCLUSION" = "success" ]; then
        CI_STATUS=0
    else
        CI_STATUS=1
    fi

    ITER_END=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    ITER_END_EPOCH=$(date +%s)

    if [ $CI_STATUS -eq 0 ]; then
        success "CI PASSED for iteration $i (attempt $ATTEMPT of $STORY_TITLE)"

        # Mark the story as completed in prd.json
        jq "(.userStories[] | select(.id == \"$STORY_ID\")).status = \"completed\"" prd.json > prd.json.tmp && mv prd.json.tmp prd.json

        success "Marked story '$STORY_ID' as completed"

        echo "---" >> progress.txt
        echo "Iteration $i [$(date -u +"%Y-%m-%dT%H:%M:%SZ")]: $STORY_TITLE COMPLETED. CI passed (run $RUN_ID). Attempt $ATTEMPT." >> progress.txt
        git add prd.json progress.txt metrics.json && git commit -m "Mark $STORY_ID complete (CI verified: run $RUN_ID, attempt $ATTEMPT)" && git push 2>/dev/null || true

        record_iteration "$i" "$STORY_ID" "$STORY_TITLE" "ci_pass" "$RUN_ID" "$ITER_START" "$ITER_END" "$ITER_START_EPOCH" "$ITER_END_EPOCH" "$ATTEMPT"

    else
        error "CI FAILED for iteration $i (attempt $ATTEMPT of $STORY_TITLE, run $RUN_ID)"

        # Fetch CI failure logs and save to progress.txt for the next iteration
        log "Fetching failure logs..."
        CI_LOGS=$(gh run view "$RUN_ID" --log-failed 2>&1 | tail -100)

        {
            echo "---"
            echo "Iteration $i [$(date -u +"%Y-%m-%dT%H:%M:%SZ")]: $STORY_TITLE CI FAILED (run $RUN_ID, attempt $ATTEMPT)."
            echo "Story NOT marked complete. The next iteration should debug this failure."
            echo ""
            echo "CI failure logs (last 100 lines):"
            echo '```'
            echo "$CI_LOGS"
            echo '```'
        } >> progress.txt

        warn "Failure context written to progress.txt"
        warn "Next iteration will get a fresh context window to debug"

        git add progress.txt metrics.json && git commit -m "CI failed: $STORY_ID (run $RUN_ID, attempt $ATTEMPT)" && git push 2>/dev/null || true

        record_iteration "$i" "$STORY_ID" "$STORY_TITLE" "ci_fail" "$RUN_ID" "$ITER_START" "$ITER_END" "$ITER_START_EPOCH" "$ITER_END_EPOCH" "$ATTEMPT"
    fi

    # Brief pause between iterations
    sleep 2
done

warn "Reached max iterations ($MAX_ITERATIONS). Some stories may be incomplete."
REMAINING=$(jq '[.userStories[] | select(.status != "completed")] | length' prd.json 2>/dev/null || echo "?")
warn "Incomplete stories: $REMAINING"
exit 1
