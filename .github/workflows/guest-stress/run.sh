#!/usr/bin/env bash

# Boot one Rancher Desktop VM and run three loads in the guest, each for a
# fixed time:
#
#   tar     busybox tar unpacking /media/sda/alpine.apkovl.tar.gz in parallel,
#           which is what segfaulted at boot on GitHub's macos-15-intel runners
#   stress  stress-ng memory and CPU stressors with --verify, which report
#           corruption themselves
#   go      go build -a of the packages the extension images compile, in a
#           container, which is what died in the extensions suite on the same
#           runners
#
# Each load counts its own failures.  The kernel logs a line for any process
# that segfaults, plus a register dump for every fatal signal, so the guest's
# dmesg covers every load; core dumps land in /tmp/cores and are bundled
# with the binaries; collect-logs.sh copies the serial console out
# afterwards.  A summary goes to $LOGS_DIR/summary.md and to the step
# summary.
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
case "$(uname -s)" in
Darwin)
    platform=darwin
    bin="/Applications/Rancher Desktop.app/Contents/Resources/resources/darwin/bin"
    lima_home="$HOME/Library/Application Support/rancher-desktop/lima"
    ;;
Linux)
    platform=linux
    bin="/opt/rancher-desktop/resources/resources/linux/bin"
    lima_home="$HOME/.local/share/rancher-desktop/lima"
    ;;
*)
    echo "Unsupported platform: $(uname -s)" >&2
    exit 1
    ;;
esac
summary="$LOGS_DIR/summary.md"
signatures='segfault|general protection|traps:|Oops|BUG:|Call Trace|fatal signal'
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
    if [[ $platform == darwin ]]; then
        if [[ $RD_USE_VZ == true ]]; then
            args+=(--virtual-machine.type vz)
        else
            args+=(--virtual-machine.type qemu)
        fi
    fi
    write_cpu_override
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

# Pin the guest's emulated CPU model through lima's override config, to test
# the Lima FAQ workaround for HVF-on-Intel guest corruption.  QEMU only: VZ
# has no CPU-model control.  RD launches its app with `open -a`, which drops
# the caller's environment, so QEMU_SYSTEM_X86_64 set here would never reach
# qemu; a config file does not depend on env inheritance, and lima applies
# _config/override.yaml on top of RD's generated instance config.
write_cpu_override() {
    [[ -n ${RD_CPU_TYPE:-} ]] || return 0
    if [[ $RD_USE_VZ == true ]]; then
        echo "RD_CPU_TYPE=$RD_CPU_TYPE ignored under VZ; dispatch with vz=false" >&2
        return 0
    fi
    mkdir -p "$lima_home/_config"
    cat > "$lima_home/_config/override.yaml" <<EOF
vmOpts:
  qemu:
    cpuType:
      x86_64: $RD_CPU_TYPE
EOF
    log "cpuType override written:"
    cat "$lima_home/_config/override.yaml"
}

# A crash should leave registers and a stack behind, not one line.  /tmp is
# on the data disk; the root filesystem is tmpfs.
prepare_guest() {
    guest sysctl -w kernel.print-fatal-signals=1
    guest mkdir -p /tmp/cores
    guest sysctl -w kernel.core_pattern=/tmp/cores/core.%e.%p
}

host_info() {
    if [[ $platform == darwin ]]; then
        echo "host: $(sysctl -n machdep.cpu.brand_string), $(sysctl -n hw.ncpu) CPUs, $(( $(sysctl -n hw.memsize) / 1024 / 1024 )) MB"
        # Is macOS itself a guest, and are its "CPUs" cores or threads?  A VM
        # may present vCPUs as plain cores, so hv_vmm_present is the reliable
        # half; the topology numbers are a bonus if Apple passes them through.
        echo "host virtualized (kern.hv_vmm_present): $(sysctl -n kern.hv_vmm_present 2>/dev/null || echo '?')"
        echo "host cpu topology: physicalcpu=$(sysctl -n hw.physicalcpu) logicalcpu=$(sysctl -n hw.logicalcpu) packages=$(sysctl -n hw.packages) physicalcpu_max=$(sysctl -n hw.physicalcpu_max) logicalcpu_max=$(sysctl -n hw.logicalcpu_max)"
    else
        echo "host:$(grep -m1 'model name' /proc/cpuinfo | cut -d : -f 2-), $(nproc) CPUs, $(free -m | awk '/^Mem:/ { print $2 }') MB"
    fi
}

record_info() {
    {
        echo "runner: ${ImageOS:-?} ${ImageVersion:-?}, $(uname -m)"
        host_info
        echo "vm: engine=$RD_CONTAINER_ENGINE vz=$RD_USE_VZ cpus=$RD_VM_CPUS memory=${RD_VM_MEMORY}GB cpu_type=${RD_CPU_TYPE:-<default>}"
        # The definitive check that the -cpu override took effect: lima's host
        # agent logs the actual qemu command line here.
        if [[ $RD_USE_VZ != true ]]; then
            echo "qemu -cpu from ha.stderr.log: $(grep -oE -- '-cpu [^ ]+' "$lima_home/0/ha.stderr.log" 2>/dev/null | head -n1 || echo '<not found>')"
        fi
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

# Copy a script from the workspace, which is inside the home directory the
# guest mounts, to somewhere the guest can run it without the mount.
guest_script() {
    rdctl shell cp "$here/$1" "/tmp/$1"
    echo "/tmp/$1"
}

load_tar() {
    local script
    script=$(guest_script tar-loop.sh)
    RD_TIMEOUT=$(( DURATION + 300 )) guest sh "$script" "$DURATION" "$RD_VM_CPUS"
}

# Two stress-ng invocations, so a stressor dying in one does not end the
# other.  A vm worker's core would be its whole mapping, so cap that one.
load_stress() {
    local vm=$(( RD_VM_CPUS / 2 ))
    local cpu=$(( RD_VM_CPUS - vm ))
    local vm_pid cpu_pid rc=0
    # The runners' DNS drops packages, so one apk lookup often fails.
    retry 180 10 guest apk add --no-progress stress-ng
    RD_TIMEOUT=$(( DURATION + 300 )) guest sh -c 'ulimit -c 524288; exec "$@"' -- \
        stress-ng --vm "$vm" --vm-bytes 20% --vm-method all --verify \
        --timeout "${DURATION}s" --timestamp --metrics-brief \
        > "$LOGS_DIR/stress-vm.log" 2>&1 &
    vm_pid=$!
    RD_TIMEOUT=$(( DURATION + 300 )) guest sh -c 'ulimit -c unlimited; exec "$@"' -- \
        stress-ng --cpu "$cpu" --cpu-method all --verify \
        --timeout "${DURATION}s" --timestamp --metrics-brief \
        > "$LOGS_DIR/stress-cpu.log" 2>&1 &
    cpu_pid=$!
    wait "$vm_pid" || rc=$?
    wait "$cpu_pid" || rc=$?
    cat "$LOGS_DIR/stress-vm.log" "$LOGS_DIR/stress-cpu.log"
    return "$rc"
}

# "std" matches nothing in this image because the SUSE package ships
# GOROOT/src without its go.mod, so name the roots instead; their closure is
# 216 packages and holds everything the extension images compile.
load_go() {
    local image=registry.suse.com/bci/golang:1.27
    local packages='net/http crypto/tls go/types encoding/json/v2 html/template os/user'
    local end n=0 crashed=0 rc out
    if ! retry 180 10 ctrctl pull --quiet "$image"; then
        echo "go: image pull failed, guest engine unreachable"
        return
    fi
    end=$(( $(date +%s) + DURATION ))
    while (( $(date +%s) < end )); do
        n=$(( n + 1 ))
        log "go build -a $packages, build $n"
        rc=0
        # GOTRACEBACK=crash re-raises the fatal signal under SIG_DFL after the
        # traceback, so a corrupted compiler dumps core; the ulimit and the
        # mount let it land where collect-cores.sh finds it.
        out=$("$timeout_cmd" --kill-after=5 $(( DURATION + 600 )) \
            "${engine[@]}" run --rm --env CGO_ENABLED=0 --env GOTRACEBACK=crash \
            --ulimit core=-1 --volume /tmp/cores:/tmp/cores "$image" \
            sh -c "ulimit -c unlimited; go build -a $packages" 2>&1) || rc=$?
        printf '%s\n' "$out"
        log "build $n exited $rc"
        if (( rc != 0 )); then
            # A crash marker is a corrupted compiler; anything else (a lost
            # daemon socket, a wedged guest) is not a build result, so stop
            # rather than spin thousands of instant failures.
            if printf '%s' "$out" | grep -qE "$go_signatures"; then
                crashed=$(( crashed + 1 ))
            else
                echo "go: build $n failed with no crash marker, guest likely wedged; stopping"
                break
            fi
        fi
    done
    echo "go: $n builds, $crashed crashed"
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

collect_cores() {
    local script
    script=$(guest_script collect-cores.sh)
    RD_TIMEOUT=300 guest sh "$script" > "$LOGS_DIR/cores.tar.gz" 2> "$LOGS_DIR/cores.txt" || true
    if [[ ! -s $LOGS_DIR/cores.tar.gz ]]; then
        rm -f "$LOGS_DIR/cores.tar.gz"
    fi
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
        grep -E 'stress-ng: (fail|error):|unexpected signal|(passed|failed|skipped): ' \
            "$LOGS_DIR/stress-vm.log" "$LOGS_DIR/stress-cpu.log" || true
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
    if [[ $platform == darwin && $RD_USE_VZ == true ]]; then
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
        grep -E -A12 "$signatures" "$LOGS_DIR/dmesg.log" || echo "none in dmesg"
        grep -E "$signatures" "$serial" 2>/dev/null || echo "none in ${serial##*/}"
        echo '```'
        if [[ -f $LOGS_DIR/cores.txt ]]; then
            echo
            echo "### Core dumps"
            echo
            echo '```'
            cat "$LOGS_DIR/cores.txt"
            echo '```'
        fi
    } > "$summary"
    cat "$summary"
    if [[ -n ${GITHUB_STEP_SUMMARY:-} ]]; then
        cat "$summary" >> "$GITHUB_STEP_SUMMARY"
    fi
}

trap write_summary EXIT
start_rancher_desktop
prepare_guest
record_info
log "segfaults in dmesg after boot: $(guest dmesg | grep -c segfault || true)"
for load in $LOADS; do
    run_load "$load"
done
collect_cores
