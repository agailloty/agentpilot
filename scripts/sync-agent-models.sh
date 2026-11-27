#!/bin/sh
# Sync the model choices of docs/AgentManifests/*.json from the CLIs actually
# installed on this machine. For each manifest with a modelSync block, runs
# `agentpilot-runner models --write` so the checked-in manifest lists exactly
# the models the CLI exposes (e.g. `agy models`, `copilot --help` --model).
#
# Usage:
#   scripts/sync-agent-models.sh            # uses agentpilot-runner from PATH, else `go run`
#   AGENTPILOT_RUNNER=/path/to/agentpilot-runner scripts/sync-agent-models.sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
MANIFEST_DIR="$ROOT/docs/AgentManifests"

if [ -n "${AGENTPILOT_RUNNER:-}" ]; then
    run_runner() { "$AGENTPILOT_RUNNER" "$@"; }
elif command -v agentpilot-runner >/dev/null 2>&1; then
    run_runner() { agentpilot-runner "$@"; }
else
    run_runner() { (cd "$ROOT/server" && go run ./cmd/runner "$@"); }
fi

for manifest in "$MANIFEST_DIR"/*.json; do
    name=$(basename "$manifest")
    [ "$name" = "schema.json" ] && continue

    agent_id=$(sed -n 's/.*"agentId"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$manifest" | head -n 1)
    cmd=$(sed -n 's/.*"launchCommand"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$manifest" | head -n 1)
    has_sync=$(sed -n 's/.*"modelSync".*/yes/p' "$manifest" | head -n 1)

    echo "==> $agent_id (cmd: $cmd)"
    if [ -z "$has_sync" ]; then
        echo "    no modelSync block, skipped"
        continue
    fi
    if [ "$cmd" = "__shell__" ]; then
        echo "    shell agent, skipped"
        continue
    fi

    if run_runner models --agent-id "$agent_id" --cmd "$cmd" --manifest "$manifest" --write; then
        echo "    manifest updated"
    else
        echo "    WARNING: discovery failed (CLI not installed or no models parsed); manifest unchanged"
    fi
done
