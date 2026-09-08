#!/usr/bin/env bash

# Boot one Rancher Desktop VM and run three loads in the guest, each for a
# fixed time:
#
#   tar     busybox tar unpacking /media/sda/alpine.apkovl.tar.gz in parallel,
#           which is what segfaulted at boot on GitHub's macos-15-intel runners
#   stress  stress-ng memory and CPU stressors with --verify, which report
#           corruption themselves
#   go      go build -a std in a container, which is what died in the
#           extensions suite on the same runners
#
# Each load counts its own failures.  The kernel logs a line for any process
# that segfaults, so the guest's dmesg covers every load, and collect-logs.sh
# copies the serial console out afterwards.  A summary goes to
# $LOGS_DIR/summary.md and to the step summary.
#
# Environment:
#   LOGS_DIR             directory for the logs (required)
#   LOADS                loads to run, in order (default: tar stress go)
#   DURATION             seconds per load (default: 600)
#   RD_CONTAINER_ENGINE  moby or containerd (default: moby)
#   RD_VM_CPUS           vCPUs for the VM (default: 4)
#   RD_VM_MEMORY         memory for the VM in GB (default: 6)
#   RD_USE_VZ            true for the Virtualization framework, false for QEMU

set -o errexit -o nounset -o pipefail

: "${LOGS_DIR:?}"
: "${LOADS:=tar stress go}"
: "${DURATION:=600}"
: "${RD_CONTAINER_ENGINE:=moby}"
: "${RD_VM_CPUS:=4}"
: "${RD_VM_MEMORY:=6}"
: "${RD_USE_VZ:=true}"

here=$(cd "$(dirname "$0")" && pwd)
bin="/Applications/Rancher Desktop.app/Contents/Resources/resources/darwin/bin"
lima_home="$HOME/Library/Application Support/rancher-desktop/lima"
summary="$LOGS_DIR/summary.md"
signatures='segfault|general protection|traps:|Oops|BUG:|Call Trace'
go_signatures='internal compiler error|split stack overflow|signal: segmentation fault|fatal error|unexpected signal|bad g in signal handler'
mkdir -p "$LOGS_DIR"

log() {
    printf '%s %s\n' "$(date -u +%FT%TZ)" "$*"
}

# Homebrew's coreutils installs the GNU tools with a g prefix.
timeout_cmd=timeout
if command -v gtimeout >/dev/null; then
    timeout_cmd=gtimeout
fi

# Every rdctl call gets a timeout, because a wedged guest is one of the
# failures this script exists to measure.  RD_TIMEOUT is in seconds.
rdctl() {
    "$timeout_cmd" --kill-after=5 "${RD_TIMEOUT:-60}" "$bin/rdctl" "$@"
}

# Run a command in the guest as root.
guest() {
    rdctl shell sudo "$@"
}

# The credential helpers named in the docker config Rancher Desktop writes
# must be on PATH, or the CLI refuses to pull.
PATH="$bin:$PATH"
if [[ $RD_CONTAINER_ENGINE == moby ]]; then
    engine=("$bin/docker" --context rancher-desktop)
else
    engine=("$bin/nerdctl" --namespace default)
fi

ctrctl() {
    "${engine[@]}" "$@"
}

# Retry a command every $2 seconds for up to $1 seconds.
retry() {
    local seconds=$1 delay=$2 deadline
    shift 2
    deadline=$(( $(date +%s) + seconds ))
    until "$@"; do
        if (( $(date +%s) >= deadline )); then
            echo "Gave up after ${seconds}s: $*" >&2
            return 1
        fi
        sleep "$delay"
    done
}

# With Kubernetes off the backend reports DISABLED, not STARTED, once the
# container engine is up.
backend_started() {
    local state
    state=$(RD_TIMEOUT=10 rdctl api /v1/backend_state 2>/dev/null | jq -r .vmState) || state=unknown
    log "backend state: $state"
    if [[ $state == ERROR ]]; then
        echo "The backend reported an error" >&2
        exit 1
    fi
    [[ $state == STARTED || $state == DISABLED ]]
}

start_rancher_desktop() {
    local args=(
        --application.debug
        --application.updater.enabled=false
        --application.admin-access=false
        --application.path-management-strategy rcfiles
        --kubernetes.enabled=false
        --container-engine.name="$RD_CONTAINER_ENGINE"
        --virtual-machine.memory-in-gb "$RD_VM_MEMORY"
        --virtual-machine.number-cpus="$RD_VM_CPUS"
        --no-modal-dialogs
    )
    if [[ $RD_USE_VZ == true ]]; then
        args+=(--virtual-machine.type vz)
    else
        args+=(--virtual-machine.type qemu)
    fi
    log "rdctl start ${args[*]}"
    "$bin/rdctl" start "${args[@]}" &

    log "waiting for the backend"
    retry 1200 10 backend_started
    log "waiting for the guest and the home directory mount"
    retry 600 5 rdctl shell test -f /var/run/lima-boot-done
    retry 300 5 rdctl shell test -d "$HOME/.rd"
    log "waiting for the container engine"
    retry 600 10 ctrctl info >/dev/null
    log "Rancher Desktop is up"
}

record_info() {
    {
        echo "runner: ${ImageOS:-?} ${ImageVersion:-?}, $(uname -m), $(sysctl -n machdep.cpu.brand_string)"
        echo "host: $(sysctl -n hw.ncpu) CPUs, $(( $(sysctl -n hw.memsize) / 1024 / 1024 )) MB"
        echo "vm: engine=$RD_CONTAINER_ENGINE vz=$RD_USE_VZ cpus=$RD_VM_CPUS memory=${RD_VM_MEMORY}GB"
        echo "guest: $(rdctl shell uname -a)"
        rdctl shell nproc
        rdctl shell free -m
        rdctl shell grep -m1 'model name' /proc/cpuinfo
        rdctl shell grep -m1 '^flags' /proc/cpuinfo
    } > "$LOGS_DIR/info.txt" 2>&1
    cat "$LOGS_DIR/info.txt"
}

guest_uptime() {
    rdctl shell cut -d ' ' -f 1 /proc/uptime 2>/dev/null || echo '?'
}

load_tar() {
    # The workspace is inside the home directory, which the guest mounts.
    rdctl shell cp "$here/tar-loop.sh" /tmp/tar-loop.sh
    RD_TIMEOUT=$(( DURATION + 300 )) guest sh /tmp/tar-loop.sh "$DURATION" "$RD_VM_CPUS"
}

load_stress() {
    local vm=$(( RD_VM_CPUS / 2 ))
    local cpu=$(( RD_VM_CPUS - vm ))
    guest apk add --no-progress stress-ng
    RD_TIMEOUT=$(( DURATION + 300 )) guest stress-ng \
        --vm "$vm" --vm-bytes 20% --vm-method all \
        --cpu "$cpu" --cpu-method all \
        --verify --timeout "${DURATION}s" --metrics-brief
}

load_go() {
    local image=registry.suse.com/bci/golang:1.27
    local end n=0 failed=0 rc
    ctrctl pull --quiet "$image"
    end=$(( $(date +%s) + DURATION ))
    while (( $(date +%s) < end )); do
        n=$(( n + 1 ))
        log "go build -a std, build $n"
        rc=0
        "$timeout_cmd" --kill-after=5 $(( DURATION + 600 )) \
            "${engine[@]}" run --rm --env CGO_ENABLED=0 "$image" go build -a std || rc=$?
        log "build $n exited $rc"
        (( rc == 0 )) || failed=$(( failed + 1 ))
    done
    echo "go: $n builds, $failed failed"
}

run_load() {
    local name=$1 rc=0
    if ! declare -F "load_$name" >/dev/null; then
        echo "Unknown load: $name" >&2
        exit 1
    fi
    local start end
    start=$(date -u +%FT%TZ)
    log "=== $name: start (guest uptime $(guest_uptime)s)"
    "load_$name" 2>&1 | tee "$LOGS_DIR/$name.log" || rc=$?
    end=$(date -u +%FT%TZ)
    log "=== $name: exit $rc (guest uptime $(guest_uptime)s)"
    printf '%s %s %s %s\n' "$name" "$rc" "$start" "$end" >> "$LOGS_DIR/loads.txt"
}

# The host sampler writes to $LOGS_DIR/_diag when the workflow runs it.
host_idle() {
    local probe="$LOGS_DIR/_diag/host-probe.log"
    if [[ -f $probe ]]; then
        "$here/host-idle.sh" "$probe" "$1" "$2"
    else
        echo "no host sampler log"
    fi
}

# The lines from a load's log that say how it went.
result_lines() {
    case $1 in
    tar)
        grep -E '^worker ' "$LOGS_DIR/tar.log" || true
        ;;
    stress)
        grep -E 'stress-ng: (fail|error):|(passed|failed|skipped): ' "$LOGS_DIR/stress.log" || true
        ;;
    go)
        grep -E '^go: ' "$LOGS_DIR/go.log" || true
        echo "crash markers: $(grep -cE "$go_signatures" "$LOGS_DIR/go.log" || true)"
        grep -E "$go_signatures" "$LOGS_DIR/go.log" || true
        ;;
    esac
}

write_summary() {
    local hypervisor=qemu serial="$lima_home/0/serial.log"
    if [[ $RD_USE_VZ == true ]]; then
        hypervisor=vz
        serial="$lima_home/0/serialv.log"
    fi
    guest dmesg > "$LOGS_DIR/dmesg.log" 2>&1 || true
    {
        echo "## Guest stress: ${ImageOS:-?} $(uname -m), $RD_CONTAINER_ENGINE, $hypervisor, $RD_VM_CPUS vCPUs, ${RD_VM_MEMORY} GB, ${DURATION}s per load"
        echo
        if [[ -f $LOGS_DIR/loads.txt ]]; then
            while read -r name rc start end; do
                echo "### $name (exit $rc, $start to $end)"
                echo
                echo '```'
                result_lines "$name"
                echo "host: $(host_idle "$start" "$end")"
                echo '```'
                echo
            done < "$LOGS_DIR/loads.txt"
        else
            echo "No load ran."
            echo
        fi
        echo "### Kernel messages matching \`$signatures\`"
        echo
        echo '```'
        grep -E "$signatures" "$LOGS_DIR/dmesg.log" || echo "none in dmesg"
        grep -E "$signatures" "$serial" 2>/dev/null || echo "none in ${serial##*/}"
        echo '```'
    } > "$summary"
    cat "$summary"
    if [[ -n ${GITHUB_STEP_SUMMARY:-} ]]; then
        cat "$summary" >> "$GITHUB_STEP_SUMMARY"
    fi
}

trap write_summary EXIT
start_rancher_desktop
record_info
log "segfaults in dmesg after boot: $(guest dmesg | grep -c segfault || true)"
for load in $LOADS; do
    run_load "$load"
done
