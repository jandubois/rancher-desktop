#!/usr/bin/env bash

# Collect the evidence that BATS's own capture misses when a job fails.
# capture_logs() runs inside BATS, so it records nothing for a job that fails
# before Run BATS starts, or for the test that hangs until that step times
# out.  The workflow runs this after Run BATS, even when that step failed or
# never ran, unless the job was cancelled.
#
# Ported from rancher-desktop-2's rdd/scripts/collect-bats-logs.sh and the
# non-destructive half of its bats-with-timeout.sh support bundle, adapted to
# Rancher Desktop's fixed paths (it has no per-instance directories), to rdctl
# in place of rdd, and to an Alpine guest running OpenRC rather than systemd.
#
# Usage: collect-logs.sh <output-dir>

# Deliberately no errexit: a diagnostic must never abort part-way and take the
# rest of the evidence with it.
set -o nounset
shopt -s nullglob

output_dir=${1:?usage: collect-logs.sh <output-dir>}
mkdir -p "$output_dir"
bundle="$output_dir/support-bundle.log"

# Under Git Bash and WSL alike the Windows environment variables hold Windows
# paths, which neither shell can use directly.
win32_path() {
    cygpath --unix "$1" 2>/dev/null || wslpath -u "$1" 2>/dev/null
}

exe=
case "$(uname -s)" in
Darwin)
    platform=darwin
    app_home="$HOME/Library/Application Support/rancher-desktop"
    log_dir="$HOME/Library/Logs/rancher-desktop"
    resources="/Applications/Rancher Desktop.app/Contents/Resources/resources"
    ;;
Linux)
    platform=linux
    app_home="$HOME/.local/share/rancher-desktop"
    log_dir="$app_home/logs"
    resources="/opt/rancher-desktop/resources/resources"
    ;;
*)
    platform=win32
    exe=.exe
    # Stop Git Bash rewriting rdctl's /v1/... API paths into Windows paths
    # when it starts a native program.
    export MSYS2_ARG_CONV_EXCL='*'
    app_home="$(win32_path "${LOCALAPPDATA:-}")/rancher-desktop"
    log_dir="$app_home/logs"
    resources="$(win32_path "${PROGRAMFILES:-}")/Rancher Desktop/resources/resources"
    ;;
esac

# The socket dump and the probes that talk to Rancher Desktop or the guest run
# behind a timeout, because the failures this bundle exists to explain are the
# ones where something is wedged.  With no timeout command, skip them rather
# than risk hanging the job that is collecting the evidence.
# Homebrew's coreutils installs the GNU tools with a g prefix.
timeout_cmd=
for candidate in timeout gtimeout; do
    if command -v "$candidate" >/dev/null; then
        timeout_cmd=$candidate
        break
    fi
done

with_timeout() {
    local seconds=$1
    shift
    "$timeout_cmd" --kill-after=1 "$seconds" "$@" 2>&1
}

# Copy the application logs.  On Unix the lima logs are symlinks into the VM
# directory; cp copies what they point to, and a dangling one fails quietly.
if [[ -d $log_dir ]]; then
    mkdir -p "$output_dir/logs"
    cp -L "$log_dir"/*.log "$output_dir/logs/" 2>/dev/null
fi

# Copy the logs of every lima instance still on disk.  BATS copies them too,
# except for a test that hangs until the Run BATS step times out.
for vm_dir in "$app_home"/lima/*/; do
    vm_name=$(basename "$vm_dir")
    [[ $vm_name == _config ]] && continue
    mkdir -p "$output_dir/lima-$vm_name"
    cp "$vm_dir"/*.log "$vm_dir"/ha.std* "$output_dir/lima-$vm_name/" 2>/dev/null
done

# Evidence of memory pressure or an external kill.  macOS jetsam and the Linux
# OOM killer both use SIGKILL, which leaves no crash log, so without this a
# process that vanished for want of memory looks identical to one that hung.
dump_memory_pressure() {
    echo "=== Memory stats ==="
    case "$platform" in
    darwin)
        vm_stat 2>&1
        # no-spell-check-next-line
        sysctl vm.swapusage 2>&1
        ;;
    linux)
        free -h 2>&1
        ;;
    esac

    echo
    echo "=== Top processes by memory ==="
    case "$platform" in
    darwin)
        top -l 1 -n 20 -o mem -stats pid,command,mem,state 2>&1 | tail -25
        ;;
    linux)
        ps -eo pid,pgid,pmem,rss,comm --sort=-rss 2>&1 | head -21
        ;;
    esac

    echo
    echo "=== Memory pressure / OOM events ==="
    case "$platform" in
    darwin)
        # Jetsam kills land in the unified log with sender=kernel.
        # no-spell-check-next-line
        log show --style compact --last 1h --predicate \
            '(sender == "kernel") AND ((eventMessage CONTAINS[c] "jetsam") OR (eventMessage CONTAINS[c] "memorystatus") OR (eventMessage CONTAINS[c] "low swap"))' \
            2>&1 | tail -100
        ;;
    linux)
        dmesg 2>/dev/null | grep -iE 'OOM|killed process|out of memory' | tail -50
        ;;
    esac
}

dump_sockets() {
    if command -v ss >/dev/null; then
        echo "=== Open sockets (ss) ==="
        with_timeout 30 ss --tcp --udp --processes --numeric
    elif command -v lsof >/dev/null; then
        echo "=== Open sockets (lsof) ==="
        with_timeout 30 lsof -i -P
    fi
}

# Snapshot what Rancher Desktop thinks its own state is.  Probe unconditionally
# rather than gating on a status check: the hangs worth diagnosing have the
# server alive with one request path stuck, so a gate would skip exactly when
# the capture is most useful.
dump_api_state() {
    local rdctl="$resources/$platform/bin/rdctl$exe"
    if [[ ! -x $rdctl ]]; then
        echo "rdctl not found at $rdctl"
        return
    fi

    echo "=== rdctl api /v1/backend_state ==="
    with_timeout 10 "$rdctl" api /v1/backend_state

    echo
    echo "=== rdctl list-settings ==="
    with_timeout 10 "$rdctl" list-settings

    echo
    echo "=== rdctl api /v1/diagnostic_checks ==="
    with_timeout 15 "$rdctl" api /v1/diagnostic_checks
}

# The guest holds the answer whenever the host side only shows a stuck ssh.
dump_lima_guest() {
    local limactl="$resources/$platform/lima/bin/limactl"
    [[ -x $limactl ]] || return
    # limactl talks to the VM directly, so it still answers after the Rancher
    # Desktop process itself is gone.
    local -x LIMA_HOME="$app_home/lima"

    echo "=== Guest: rc-status ==="
    with_timeout 20 "$limactl" shell 0 sudo rc-status --all

    echo
    echo "=== Guest: ps ==="
    with_timeout 20 "$limactl" shell 0 ps -ef

    echo
    echo "=== Guest: dmesg ==="
    with_timeout 20 "$limactl" shell 0 sudo dmesg | tail -100
}

dump_wsl_guest() {
    command -v wsl.exe >/dev/null || return
    # Without WSL_UTF8 wsl.exe emits UTF-16LE with a BOM.
    local -x WSL_UTF8=1

    echo "=== wsl --list --verbose ==="
    with_timeout 20 wsl.exe --list --verbose

    echo
    echo "=== Guest: rc-status ==="
    with_timeout 20 wsl.exe --distribution rancher-desktop -- rc-status --all

    echo
    echo "=== Guest: ps ==="
    with_timeout 20 wsl.exe --distribution rancher-desktop -- ps -ef
}

{
    echo "=== Support bundle at $(date -u +%FT%TZ) ==="
    echo "uname: $(uname -a)"
    echo

    echo "=== ps ==="
    ps aux 2>&1
    echo

    dump_memory_pressure
    echo

    if [[ -z $timeout_cmd ]]; then
        echo "=== No timeout command found; skipping the probes that can hang ==="
    else
        dump_sockets
        echo

        dump_api_state
        echo

        if [[ $platform == win32 ]]; then
            dump_wsl_guest
        else
            dump_lima_guest
        fi
    fi
    echo

    echo "=== End of support bundle ==="
} >>"$bundle" 2>&1

echo "Collected logs and a support bundle in $output_dir"
