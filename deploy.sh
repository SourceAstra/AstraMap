#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
BIN="$ROOT_DIR/amap"
INSTALL_DIR="${AMAP_INSTALL_DIR:-${HOME}/bin}"
INSTALL_DIR_EXPLICIT=0
ACTION="deploy"

usage() {
    cat <<'EOF'
Usage: ./deploy.sh [--install-dir DIR] [--uninstall|--rollback] [-h|--help]
EOF
}

while (($# > 0)); do
    case "$1" in
        --install-dir)
            [[ $# -ge 2 ]] || { echo "--install-dir requires a directory" >&2; exit 2; }
            INSTALL_DIR="$2"; INSTALL_DIR_EXPLICIT=1; shift 2
            ;;
        --uninstall)
            ACTION="uninstall"; shift
            ;;
        --rollback)
            ACTION="rollback"; shift
            ;;
        -h|--help)
            usage; exit 0
            ;;
        *)
            echo "Unknown argument: $1" >&2; usage >&2; exit 2
            ;;
    esac
done

# Detect active amap on PATH to avoid deploying to ~/.local/bin while PATH
# resolves to a different location (e.g. ~/bin).
ACTIVE_AMAP=$(type -P amap 2>/dev/null || true)
INSTALL_DIRS=("$INSTALL_DIR")
if ((INSTALL_DIR_EXPLICIT == 0)) && [[ -n "$ACTIVE_AMAP" && -x "$ACTIVE_AMAP" ]]; then
    ACTIVE_DIR=$(dirname -- "$ACTIVE_AMAP")
    if [[ "$ACTIVE_DIR" != "$INSTALL_DIR" ]]; then
        INSTALL_DIRS+=("$ACTIVE_DIR")
        echo "Detected active amap on PATH: $ACTIVE_AMAP; will also deploy to $ACTIVE_DIR"
    fi
fi

is_alive() {
    [[ "$1" =~ ^[0-9]+$ ]] && kill -0 "$1" 2>/dev/null
}

is_amap_process() {
    local pid="$1" args
    is_alive "$pid" || return 1
    args=$(ps -p "$pid" -o args= 2>/dev/null || true)
    [[ "$args" == *"amap serve"* || "$args" == *"amap dashboard"* || "$args" == *"amap mcp"* ]]
}

stop_pid() {
    local pid="$1"
    is_alive "$pid" || return 0
    echo "Stopping amap process: $pid"
    kill -TERM "$pid" 2>/dev/null || true
    for _ in {1..20}; do
        is_alive "$pid" || return 0
        sleep 0.25
    done
    echo "Process did not exit gracefully, sending KILL: $pid" >&2
    kill -KILL "$pid" 2>/dev/null || true
}

is_amap_service_command() {
    local args="$1" path
    for path in "${INSTALL_DIRS[@]}" "$ROOT_DIR"; do
        [[ "$args" == "$path/amap serve"* || "$args" == "$path/amap dashboard"* || "$args" == "$path/amap mcp"* ]] && return 0
    done
    [[ "$args" == */amap\ serve* || "$args" == */amap\ dashboard* || "$args" == */amap\ mcp* ]]
}

stop_running_processes() {
    while read -r pid args; do
        [[ -n "${pid:-}" && "$pid" != "$$" ]] || continue
        if is_amap_service_command "$args"; then
            stop_pid "$pid"
        fi
    done < <(ps -eo pid=,args=)
}

echo "[1/3] Stopping running amap processes"
stop_running_processes

INSTALL_BIN="${INSTALL_DIR}/amap"
PREVIOUS_BIN="${INSTALL_DIR}/amap.previous"

if [[ "$ACTION" == "uninstall" ]]; then
    for directory in "${INSTALL_DIRS[@]}"; do
        rm -f -- "$directory/amap" "$directory/amap.previous"
    done
    echo "Uninstalled amap binary"
    exit 0
fi

if [[ "$ACTION" == "rollback" ]]; then
    [[ -f "$PREVIOUS_BIN" ]] || { echo "No previous version to rollback: $PREVIOUS_BIN" >&2; exit 1; }
    if [[ -f "$INSTALL_BIN" ]]; then
        mv -f -- "$INSTALL_BIN" "${INSTALL_BIN}.failed"
    fi
    mv -- "$PREVIOUS_BIN" "$INSTALL_BIN"
    rm -f -- "${INSTALL_BIN}.failed"
    echo "Rolled back to: $INSTALL_BIN"
    exit 0
fi

echo "[2/3] Building amap"
make -C "$ROOT_DIR" build

echo "[3/3] Installing amap"
for directory in "${INSTALL_DIRS[@]}"; do
    mkdir -p "$directory"
    target="$directory/amap"
    previous="$directory/amap.previous"
    staging="${target}.new"
    cp "$ROOT_DIR/amap" "$staging"
    chmod 0755 "$staging"
    if [[ -f "$target" ]]; then
        mv -f -- "$target" "$previous"
    fi
    mv -- "$staging" "$target"
    echo "Installed: $target"
done

echo "Deploy complete"
echo "Verify: $INSTALL_BIN --version"
