#!/usr/bin/env bash
#
# install-runner.sh — install the AgentPilot runner binary from a GitHub release.
#
# The binary is downloaded from the GitHub Release matching the version tag
# (e.g. v0.1.0), its SHA-256 checksum is verified against the release
# checksums.txt, then the binary is installed and (optionally) a runner
# environment file is written for systemd / shell startup.
#
# Usage:
#   ./install-runner.sh <version> [options]
#
#   <version>            Release tag: "v0.1.0", "0.1.0" or "latest".
#
# Options:
#   --install-dir DIR    Where to put the agentpilot-runner binary.
#                        Default: /usr/local/bin when running as root,
#                        otherwise ~/.local/bin
#   --server URL         Backend URL, e.g. http://100.64.0.10:8080
#                        (written to the env file as AGENTPILOT_SERVER_URL)
#   --token SECRET       Runner Bearer token secret (scope "runner")
#   --name NAME          Machine display name (default: hostname)
#   --roots PATHS        Comma-separated project root directories
#   --env-file PATH      Runner env file to write.
#                        Default: /etc/agentpilot/runner.env as root,
#                        otherwise ~/.config/agentpilot/runner.env
#   --repo OWNER/REPO    GitHub repository (default: agailloty/agentpilot)
#   --force              Overwrite an existing binary
#   --dry-run            Print what would be downloaded/installed and exit
#   -h, --help           Show this help
#
# Examples:
#   ./install-runner.sh v0.1.0
#   ./install-runner.sh 0.1.0 --install-dir ~/.local/bin \
#       --server http://100.64.0.10:8080 --token "$RUNNER_TOKEN" \
#       --roots /home/alice/projects,/srv/repos
#   curl -fsSL https://raw.githubusercontent.com/agailloty/agentpilot/main/scripts/install-runner.sh \
#       | bash -s -- latest --server http://100.64.0.10:8080 --token "$RUNNER_TOKEN"
#
# See docs/INSTALL.md for the full installation guide.

set -euo pipefail

REPO="agailloty/agentpilot"
VERSION=""
INSTALL_DIR=""
ENV_FILE=""
SERVER_URL=""
RUNNER_TOKEN=""
MACHINE_NAME=""
ROOTS=""
FORCE=0
DRY_RUN=0

log()  { printf '[install-runner] %s\n' "$*"; }
err()  { printf '[install-runner] ERROR: %s\n' "$*" >&2; }
die()  { err "$*"; exit 1; }

usage() {
    sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'
    exit "${1:-0}"
}

while [ $# -gt 0 ]; do
    case "$1" in
        --version)     VERSION="${2:?--version requires a value}"; shift 2 ;;
        --install-dir) INSTALL_DIR="${2:?--install-dir requires a value}"; shift 2 ;;
        --env-file)    ENV_FILE="${2:?--env-file requires a value}"; shift 2 ;;
        --server)      SERVER_URL="${2:?--server requires a value}"; shift 2 ;;
        --token)       RUNNER_TOKEN="${2:?--token requires a value}"; shift 2 ;;
        --name)        MACHINE_NAME="${2:?--name requires a value}"; shift 2 ;;
        --roots)       ROOTS="${2:?--roots requires a value}"; shift 2 ;;
        --repo)        REPO="${2:?--repo requires a value}"; shift 2 ;;
        --force)       FORCE=1; shift ;;
        --dry-run)     DRY_RUN=1; shift ;;
        -h|--help)     usage 0 ;;
        -*)            die "unknown option: $1 (see --help)" ;;
        *)             if [ -z "$VERSION" ]; then VERSION="$1"; shift; else die "unexpected argument: $1"; fi ;;
    esac
done

[ -n "$VERSION" ] || { err "missing <version> argument"; usage 1; }

# Normalize the version to a release tag: "0.1.0" -> "v0.1.0"; "latest" stays.
case "$VERSION" in
    latest) TAG="latest" ;;
    v*)     TAG="$VERSION" ;;
    *)      TAG="v$VERSION" ;;
esac

if [ "$TAG" = "latest" ]; then
    RELEASE_URL="https://github.com/${REPO}/releases/latest/download"
else
    RELEASE_URL="https://github.com/${REPO}/releases/download/${TAG}"
fi

# --- detect platform ---------------------------------------------------------
OS="$(uname -s)"
ARCH="$(uname -m)"
case "$OS" in
    Linux)  GOOS="linux" ;;
    Darwin) die "no macOS runner build is published for ${REPO} (linux/windows only); build from source: cd server && go build ./cmd/runner" ;;
    *)      die "unsupported OS: $OS (linux only for this script; use install-runner.ps1 on Windows)" ;;
esac
case "$ARCH" in
    x86_64|amd64)   GOARCH="amd64" ;;
    aarch64|arm64)  GOARCH="arm64" ;;
    *)              die "unsupported architecture: $ARCH (amd64/arm64 only)" ;;
esac

ASSET="agentpilot-runner_${GOOS}_${GOARCH}.tar.gz"
BINARY_NAME="agentpilot-runner"

# --- default paths -----------------------------------------------------------
if [ -z "$INSTALL_DIR" ]; then
    if [ "$(id -u)" = "0" ]; then INSTALL_DIR="/usr/local/bin"; else INSTALL_DIR="$HOME/.local/bin"; fi
fi
if [ -z "$ENV_FILE" ]; then
    if [ "$(id -u)" = "0" ]; then ENV_FILE="/etc/agentpilot/runner.env"; else ENV_FILE="$HOME/.config/agentpilot/runner.env"; fi
fi

log "release tag : $TAG"
log "asset       : $ASSET"
log "download    : $RELEASE_URL/$ASSET"
log "install dir : $INSTALL_DIR"
log "env file    : $ENV_FILE"

if [ "$DRY_RUN" = "1" ]; then
    log "dry-run: nothing will be downloaded or installed"
    exit 0
fi

# --- dependencies ------------------------------------------------------------
command -v curl >/dev/null 2>&1 || die "curl is required (https://curl.se)"
command -v tar  >/dev/null 2>&1 || die "tar is required"

sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    elif command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 -r "$1" | awk '{print $1}'
    else
        die "need sha256sum, shasum or openssl to verify the download"
    fi
}

# --- download + verify -------------------------------------------------------
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

log "downloading checksums.txt"
curl -fSL --retry 3 --connect-timeout 15 -o "$TMP/checksums.txt" "$RELEASE_URL/checksums.txt" \
    || die "failed to download checksums.txt for tag '$TAG' — check that the release exists at https://github.com/${REPO}/releases"

log "downloading $ASSET"
curl -fSL --retry 3 --connect-timeout 15 -o "$TMP/$ASSET" "$RELEASE_URL/$ASSET" \
    || die "failed to download $ASSET for tag '$TAG' — check that the release exists at https://github.com/${REPO}/releases"

EXPECTED="$(awk -v f="$ASSET" '$2 == f {print $1}' "$TMP/checksums.txt")"
[ -n "$EXPECTED" ] || die "$ASSET not listed in checksums.txt for tag '$TAG'"
ACTUAL="$(sha256_of "$TMP/$ASSET")"
if [ "$EXPECTED" != "$ACTUAL" ]; then
    die "SHA-256 mismatch for $ASSET (expected $EXPECTED, got $ACTUAL) — aborting, file not installed"
fi
log "SHA-256 verified: $ACTUAL"

# --- extract + install -------------------------------------------------------
mkdir -p "$TMP/extract"
tar -xzf "$TMP/$ASSET" -C "$TMP/extract"
[ -f "$TMP/extract/$BINARY_NAME" ] || die "archive does not contain $BINARY_NAME"

mkdir -p "$INSTALL_DIR"
DEST="$INSTALL_DIR/$BINARY_NAME"
if [ -e "$DEST" ] && [ "$FORCE" = "0" ]; then
    die "$DEST already exists (use --force to overwrite)"
fi
if command -v install >/dev/null 2>&1; then
    install -m 0755 "$TMP/extract/$BINARY_NAME" "$DEST.tmp.$$"
else
    cp "$TMP/extract/$BINARY_NAME" "$DEST.tmp.$$" && chmod 0755 "$DEST.tmp.$$"
fi
mv -f "$DEST.tmp.$$" "$DEST"
chmod 0755 "$DEST" 2>/dev/null || true
log "installed $DEST"

INSTALLED_VERSION="$("$DEST" --version 2>/dev/null || echo unknown)"
log "check: $INSTALLED_VERSION"

# --- optional runner env file -------------------------------------------------
if [ -n "$SERVER_URL" ] || [ -n "$RUNNER_TOKEN" ] || [ -n "$MACHINE_NAME" ] || [ -n "$ROOTS" ]; then
    mkdir -p "$(dirname "$ENV_FILE")"
    ENV_TMP="$ENV_FILE.tmp.$$"
    # keep any existing unmanaged lines, drop the keys we manage
    if [ -f "$ENV_FILE" ]; then
        grep -v -E '^(AGENTPILOT_SERVER_URL|AGENTPILOT_RUNNER_TOKEN|AGENTPILOT_MACHINE_NAME|AGENTPILOT_PROJECT_ROOTS)=' "$ENV_FILE" > "$ENV_TMP" || true
    else
        : > "$ENV_TMP"
    fi
    [ -n "$SERVER_URL" ]   && printf 'AGENTPILOT_SERVER_URL=%s\n' "$SERVER_URL"   >> "$ENV_TMP"
    [ -n "$RUNNER_TOKEN" ] && printf 'AGENTPILOT_RUNNER_TOKEN=%s\n' "$RUNNER_TOKEN" >> "$ENV_TMP"
    [ -n "$MACHINE_NAME" ] && printf 'AGENTPILOT_MACHINE_NAME=%s\n' "$MACHINE_NAME" >> "$ENV_TMP"
    [ -n "$ROOTS" ]        && printf 'AGENTPILOT_PROJECT_ROOTS=%s\n' "$ROOTS"        >> "$ENV_TMP"
    chmod 600 "$ENV_TMP"
    mv -f "$ENV_TMP" "$ENV_FILE"
    log "wrote runner configuration to $ENV_FILE (mode 0600)"
fi

# --- next steps ---------------------------------------------------------------
PATH_NOTE=""
case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *) PATH_NOTE="NOTE: $INSTALL_DIR is not in your PATH — add it:  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.bashrc" ;;
esac

cat <<EOF

[install-runner] Done. Installed: $DEST ($INSTALLED_VERSION)
$PATH_NOTE

Next steps:
  1. Make sure the server is running and reachable, e.g.:
       curl -fsS -H "Authorization: Bearer <mobile-token>" http://<server-ip>:8080/healthz
  2. Start the runner (config was written to $ENV_FILE):
       set -a; . "$ENV_FILE"; set +a
       agentpilot-runner
     or explicitly:
       agentpilot-runner --server http://<server-ip>:8080 --token <runner-token> --roots /path/to/projects
  3. On Linux, keep it running with the systemd unit example in docs/INSTALL.md.

Full guide: https://github.com/${REPO}/blob/main/docs/INSTALL.md
EOF
