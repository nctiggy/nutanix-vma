#!/usr/bin/env bash
#
# ralph-ci.sh -- Ralph loop with GitHub Actions CI verification
#
# Instead of marking stories complete after local tests pass, this wrapper:
# 1. Runs Ralph's AI tool (Claude Code) to implement a story
# 2. Pushes to GitHub
# 3. Watches the CI run via `gh run watch`
# 4. Only marks the story complete if CI passes
# 5. If CI fails, leaves the story incomplete so the next iteration debugs it
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

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MAX_ITERATIONS="${1:-30}"
TOOL="claude"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log() { echo -e "${BLUE}[ralph-ci]${NC} $1"; }
success() { echo -e "${GREEN}[ralph-ci]${NC} $1"; }
warn() { echo -e "${YELLOW}[ralph-ci]${NC} $1"; }
error() { echo -e "${RED}[ralph-ci]${NC} $1"; }

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

log "Starting Ralph CI loop (max $MAX_ITERATIONS iterations)"
log "Stories will only be marked complete after GitHub Actions CI passes"
echo ""

for i in $(seq 1 "$MAX_ITERATIONS"); do
    echo ""
    log "=========================================="
    log "Iteration $i / $MAX_ITERATIONS"
    log "=========================================="

    # Check if all stories are complete
    INCOMPLETE=$(jq '[.userStories[] | select(.status != "completed")] | length' prd.json 2>/dev/null || echo "0")
    if [ "$INCOMPLETE" = "0" ]; then
        success "All stories complete! Ralph is done."
        exit 0
    fi

    NEXT_STORY=$(jq -r '[.userStories[] | select(.status != "completed")][0].title // "unknown"' prd.json 2>/dev/null)
    log "Next story: $NEXT_STORY"
    log "Incomplete stories remaining: $INCOMPLETE"

    # Record commit count before Ralph runs
    COMMIT_BEFORE=$(git rev-list --count HEAD 2>/dev/null || echo "0")

    # Run Claude Code with the Ralph prompt
    # The prompt tells Claude to implement the next story but NOT mark it complete
    # We handle completion status after CI verification
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

    echo "$RALPH_PROMPT" | claude --dangerously-skip-permissions --print 2>&1 | tee "/tmp/ralph-iteration-$i.log"

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
        echo "Iteration $i: No commits produced. Check logs." >> progress.txt
        continue
    fi

    NEW_COMMITS=$((COMMIT_AFTER - COMMIT_BEFORE))
    log "Ralph produced $NEW_COMMITS new commit(s)"

    # Push to GitHub
    log "Pushing to GitHub..."
    if ! git push 2>&1; then
        error "git push failed. Skipping CI verification."
        echo "---" >> progress.txt
        echo "Iteration $i: git push failed." >> progress.txt
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
        jq '(.userStories[] | select(.status != "completed") | select(. == (.userStories[] | select(.status != "completed"))[0])) .status = "completed"' prd.json > prd.json.tmp && mv prd.json.tmp prd.json 2>/dev/null || true
        echo "---" >> progress.txt
        echo "Iteration $i: Story completed (no CI workflow found -- likely pre-CI story)." >> progress.txt
        git add prd.json progress.txt && git commit -m "Mark story complete (pre-CI)" && git push 2>/dev/null || true
        continue
    fi

    log "CI run found: $RUN_ID"
    log "Watching CI run... (this may take a few minutes)"

    # Watch the CI run until it completes
    gh run watch "$RUN_ID" --exit-status 2>&1 | tail -20

    CI_STATUS=$?

    if [ $CI_STATUS -eq 0 ]; then
        success "CI PASSED for iteration $i"

        # Mark the story as completed in prd.json
        # Find the first incomplete story and mark it
        STORY_ID=$(jq -r '[.userStories[] | select(.status != "completed")][0].id // empty' prd.json 2>/dev/null)
        if [ -n "$STORY_ID" ]; then
            jq "(.userStories[] | select(.id == \"$STORY_ID\")).status = \"completed\"" prd.json > prd.json.tmp && mv prd.json.tmp prd.json
            success "Marked story '$STORY_ID' as completed"
        fi

        echo "---" >> progress.txt
        echo "Iteration $i: Story completed. CI passed (run $RUN_ID)." >> progress.txt
        git add prd.json progress.txt && git commit -m "Mark story complete (CI verified: run $RUN_ID)" && git push 2>/dev/null || true

    else
        error "CI FAILED for iteration $i (run $RUN_ID)"

        # Fetch CI failure logs and save to progress.txt for the next iteration
        log "Fetching failure logs..."
        CI_LOGS=$(gh run view "$RUN_ID" --log-failed 2>&1 | tail -100)

        {
            echo "---"
            echo "Iteration $i: CI FAILED (run $RUN_ID). Story NOT marked complete."
            echo "The next iteration should debug this failure."
            echo ""
            echo "CI failure logs (last 100 lines):"
            echo '```'
            echo "$CI_LOGS"
            echo '```'
        } >> progress.txt

        warn "Failure context written to progress.txt"
        warn "Next iteration will get a fresh context window to debug"

        git add progress.txt && git commit -m "CI failed (run $RUN_ID) -- failure logs for next iteration" && git push 2>/dev/null || true
    fi

    # Brief pause between iterations
    sleep 2
done

warn "Reached max iterations ($MAX_ITERATIONS). Some stories may be incomplete."
REMAINING=$(jq '[.userStories[] | select(.status != "completed")] | length' prd.json 2>/dev/null || echo "?")
warn "Incomplete stories: $REMAINING"
exit 1
