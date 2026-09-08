#!/usr/bin/env bash

# Boot one plain Lima VM on a hosted runner, optionally with extra kernel
# parameters, and measure two things about the guest:
#
#   clock       which clocksource the kernel picked, and how long a clock read
#               takes.  Nested under Apple's hypervisor the guest cannot
#               calibrate the TSC, marks it unstable and falls back to HPET,
#               where every read traps to the hypervisor.
#   stability   whether CPU-bound work corrupts itself, which on macos-15-intel
#               it does within minutes.
#
# Measuring the pair is the point.  If a kernel parameter that gives the guest
# a fast clock also stops the corruption, the clock was the cause; if the
# crashes continue, it is a separate symptom and no clock fix will help.
#
# Rancher Desktop is not involved.  The corruption reproduces in RD's Alpine
# guest and in RD2's openSUSE guest, so it belongs to the hypervisor.
#
# Environment:
#   LOGS_DIR   directory for the logs (required)
#   VM_TYPE    vz or qemu (default: vz)
#   CMDLINE    extra kernel parameters (default: none).  tsc_early_khz=auto
#              is replaced with the host's own TSC frequency in kHz.
#   CPU_TYPE   QEMU -cpu model, e.g. "max" (default: lima's; ignored under vz)
#   CPUS       vCPUs for the VM (default: 4)
#   MEMORY     memory for the VM in GB (default: 6)
#   LOADS      loads to run, in order (default: clock stress go)
#   DURATION   seconds per load (default: 600)

set -o errexit -o nounset -o pipefail

: "${LOGS_DIR:?}"
: "${VM_TYPE:=vz}"
: "${CMDLINE:=}"
: "${CPU_TYPE:=}"
: "${CPUS:=4}"
: "${MEMORY:=6}"
: "${LOADS:=clock stress go}"
: "${DURATION:=600}"

here=$(cd "$(dirname "$0")" && pwd)
instance=clock
summary="$LOGS_DIR/summary.md"
clocksource_dir=/sys/devices/system/clocksource/clocksource0
signatures='segfault|general protection|traps:|Oops|BUG:|Call Trace|fatal signal'
go_signatures='internal compiler error|split stack overflow|signal: segmentation fault|fatal error|unexpected signal|bad g in signal handler|SIGSEGV: segmentation violation|SIGBUS: |SIGILL: |panic: runtime error|unexpected fault address'

# Ubuntu boots EFI through its own GRUB, so a kernel parameter is one snippet
# in /etc/default/grub.d plus a reboot.  Lima's Alpine ISO and Rancher
# Desktop's image would both have to be rebuilt instead.
image_x86_64=https://cloud-images.ubuntu.com/releases/resolute/release-20260720/ubuntu-26.04-server-cloudimg-amd64.img
digest_x86_64=sha256:117816726abbdefc5ef3e38902e81a76f1c76c3610e709999d0885f9d5d9b477
image_aarch64=https://cloud-images.ubuntu.com/releases/resolute/release-20260720/ubuntu-26.04-server-cloudimg-arm64.img
digest_aarch64=sha256:7bcf159e29ad0000bfed9c57875908c39268f5ed1257f4958fa6a9f5f60edd54

# vz is macOS-only, so a Linux control run gets QEMU/KVM whatever it asked for.
if [[ $(uname -s) != Darwin ]]; then
    VM_TYPE=qemu
fi

export LIMA_HOME="${LIMA_HOME:-$HOME/.lima}"
instance_dir="$LIMA_HOME/$instance"
mkdir -p "$LOGS_DIR"

log() {
    printf '%s %s\n' "$(date -u +%FT%TZ)" "$*"
}

# Homebrew's coreutils installs the GNU tools with a g prefix.
timeout_cmd=timeout
if command -v gtimeout >/dev/null; then
    timeout_cmd=gtimeout
fi

# A wedged guest is one of the failures this script measures, hence the
# timeout; GUEST_TIMEOUT is in seconds.  The instance has no mounts, so the
# host's working directory does not exist in the guest.
guest() {
    "$timeout_cmd" --kill-after=5 "${GUEST_TIMEOUT:-120}" \
        limactl shell --workdir / "$instance" "$@"
}

root() {
    guest sudo "$@"
}

# tsc=reliable cannot rescue this guest on its own: with no frequency,
# tsc_init() returns at arch/x86/kernel/tsc.c:1541, before every consumer of
# tsc_clocksource_reliable, so the TSC is never registered as a clocksource.
# tsc_early_khz= is the one guest-side way to hand the kernel a frequency, and
# macOS knows the number.  QEMU/HVF also publishes it in CPUID leaf 0x40000010
# but leaves the hypervisor signature blank, so Linux never looks.
resolve_cmdline() {
    local hz khz
    [[ $CMDLINE == *tsc_early_khz=auto* ]] || return 0
    hz=$(sysctl -n machdep.tsc.frequency 2>/dev/null || echo 0)
    if (( hz == 0 )); then
        echo "tsc_early_khz=auto needs machdep.tsc.frequency, which this host lacks" >&2
        return 1
    fi
    khz=$(( hz / 1000 ))
    CMDLINE=${CMDLINE//tsc_early_khz=auto/tsc_early_khz=$khz}
    log "resolved kernel parameters: $CMDLINE"
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

# The guest has no mounts and no containerd, so nothing but the hypervisor and
# the load can explain a crash.
write_config() {
    local config="$LOGS_DIR/$instance.yaml"
    cat > "$config" <<EOF
vmType: "$VM_TYPE"
cpus: $CPUS
memory: "${MEMORY}GiB"
disk: "20GiB"
mounts: []
containerd:
  system: false
  user: false
images:
- location: "$image_x86_64"
  arch: "x86_64"
  digest: "$digest_x86_64"
- location: "$image_aarch64"
  arch: "aarch64"
  digest: "$digest_aarch64"
EOF
    # QEMU answers CPUID leaves 0x15 and 0x16 with zeros whatever the model, so
    # no model can advertise a TSC frequency; the knob is here to check that
    # against a real guest.
    if [[ -n $CPU_TYPE ]]; then
        cat >> "$config" <<EOF
vmOpts:
  qemu:
    cpuType:
      x86_64: "$CPU_TYPE"
EOF
    fi
}

start_vm() {
    write_config
    log "starting the guest from $LOGS_DIR/$instance.yaml:"
    cat "$LOGS_DIR/$instance.yaml"
    limactl start --yes --timeout=30m "$LOGS_DIR/$instance.yaml"
    limactl list
}

# Reboot into a known kernel command line.  console=hvc0 makes a VZ guest write
# its boot to lima's serialv.log, the only evidence left when a boot wedges, and
# a baseline run takes the same reboot, so the two conditions differ in one
# parameter.  Files in /etc/default/grub.d are sourced after /etc/default/grub,
# so this appends to the image's own command line.
apply_cmdline() {
    local want="console=hvc0 console=tty1 console=ttyS0 $CMDLINE" actual param
    log "kernel parameters: $want"
    root tee /etc/default/grub.d/99-clock.cfg <<EOF
GRUB_CMDLINE_LINUX_DEFAULT="\$GRUB_CMDLINE_LINUX_DEFAULT $want"
EOF
    GUEST_TIMEOUT=300 root update-grub
    log "restarting the guest"
    limactl stop --yes "$instance"
    limactl start --yes --timeout=30m "$instance"
    # A parameter that did not take makes every later reading meaningless.
    actual=$(guest cat /proc/cmdline)
    log "guest /proc/cmdline: $actual"
    for param in $want; do
        if [[ " $actual " != *" $param "* ]]; then
            echo "Kernel parameter $param is missing from /proc/cmdline" >&2
            return 1
        fi
    done
}

prepare_guest() {
    root sysctl -w kernel.print-fatal-signals=1
    root mkdir -p /var/tmp/cores
    root chmod 1777 /var/tmp/cores
    root sysctl -w kernel.core_pattern=/var/tmp/cores/core.%e.%p
}

install_packages() {
    # The runners' DNS drops packets, so an apt run often fails once.
    retry 300 15 root apt-get update
    retry 300 15 root env DEBIAN_FRONTEND=noninteractive \
        apt-get install --yes --no-install-recommends stress-ng golang-go
    # cpuid reads the TSC leaves, and Debian builds it for x86 only.
    root env DEBIAN_FRONTEND=noninteractive \
        apt-get install --yes --no-install-recommends cpuid || true
}

host_info() {
    if [[ $(uname -s) == Darwin ]]; then
        echo "host: $(sysctl -n machdep.cpu.brand_string), $(sysctl -n hw.ncpu) CPUs, $(( $(sysctl -n hw.memsize) / 1024 / 1024 )) MB"
        # hv_vmm_present=1 means macOS is itself a guest, so our VM is nested.
        echo "host virtualized (kern.hv_vmm_present): $(sysctl -n kern.hv_vmm_present 2>/dev/null || echo '?')"
        echo "host tsc frequency: $(sysctl -n machdep.tsc.frequency 2>/dev/null || echo '?')"
        echo "host cpu topology: physicalcpu=$(sysctl -n hw.physicalcpu) logicalcpu=$(sysctl -n hw.logicalcpu) packages=$(sysctl -n hw.packages)"
    else
        echo "host:$(grep -m1 'model name' /proc/cpuinfo | cut -d : -f 2-), $(nproc) CPUs, $(free -m | awk '/^Mem:/ { print $2 }') MB"
    fi
}

# Everything that says which clock the guest ended up with, recorded once per
# boot so a run with a kernel parameter shows the before and the after.
record_info() {
    local tag=$1 leaf
    {
        echo "=== $tag"
        echo "runner: ${ImageOS:-?} ${ImageVersion:-?}, $(uname -m)"
        host_info
        echo "vm: type=$VM_TYPE cpus=$CPUS memory=${MEMORY}GB cpu_type=${CPU_TYPE:-<default>} cmdline=${CMDLINE:-<none>}"
        # Lima logs the driver it really used, and for QEMU the command line;
        # never trust the requested type without checking.
        echo "lima driver: $(grep -o 'Using internal driver [^,]*' "$instance_dir/ha.stderr.log" 2>/dev/null | tail -1 | tr -d '\\"')"
        if [[ $VM_TYPE != vz ]]; then
            echo "qemu args:"
            grep -oE -- '-(accel|cpu|smp|m) [^ ]+' "$instance_dir/ha.stderr.log" 2>/dev/null | sort -u || echo '<none found>'
        fi
        echo "guest: $(guest uname -a)"
        echo "guest cmdline: $(guest cat /proc/cmdline)"
        echo "guest clocksource available: $(guest cat $clocksource_dir/available_clocksource)"
        echo "guest clocksource current: $(guest cat $clocksource_dir/current_clocksource)"
        echo "guest tsc/clocksource dmesg:"
        root dmesg | grep -iE 'tsc|clocksource|hpet|kvm-clock' || echo '<none>'
        echo "guest cpuid TSC leaves:"
        for leaf in 0x15 0x16 0x40000000 0x40000010; do
            guest cpuid -1 -r -l "$leaf"
        done
        guest nproc
        guest grep -m1 -E 'model name|^Model' /proc/cpuinfo
        guest grep -m1 -E '^flags|^Features' /proc/cpuinfo
    # A probe that fails on this architecture must not end the run, and `|| true`
    # exempts every command in the group from errexit.
    } > "$LOGS_DIR/info-$tag.txt" 2>&1 || true
    cat "$LOGS_DIR/info-$tag.txt"
}

# How expensive is one clock read?  A healthy guest reads it in userspace
# through the vDSO and manages billions of iterations; an HPET guest traps to
# the hypervisor every read and manages millions.
load_clock() {
    local half=$(( DURATION / 2 )) mode rc attempt built=''
    limactl copy "$here/../guest-stress/vdso-probe.go" "$instance:/var/tmp/vdso-probe.go"
    # The build is itself a compiler run that may fault, so keep trying.
    for attempt in 1 2 3 4 5; do
        if GUEST_TIMEOUT=600 guest go build -o /var/tmp/vdso-probe /var/tmp/vdso-probe.go; then
            built=yes
            break
        fi
        log "vdso-probe build attempt $attempt failed"
    done
    if [[ -z $built ]]; then
        echo "clock: could not build the probe after 5 attempts"
        return
    fi
    for mode in clock noclock; do
        log "vdso-probe mode=$mode for ${half}s"
        rc=0
        GUEST_TIMEOUT=$(( half + 180 )) guest sh -c \
            "ulimit -c unlimited; GOTRACEBACK=crash /var/tmp/vdso-probe -mode $mode -seconds $half" || rc=$?
        log "vdso-probe mode=$mode exited $rc"
    done
}

# The fastest reproducer: the CPU stressor took a SIGSEGV in nearly every
# macos-15-intel run, between 46 s and 142 s in.  The vm stressor is the
# control that has always passed, showing quiescent guest RAM is intact.
load_stress() {
    local vm=$(( CPUS / 2 ))
    local cpu=$(( CPUS - vm ))
    local vm_pid cpu_pid rc=0
    GUEST_TIMEOUT=$(( DURATION + 300 )) guest sh -c \
        "ulimit -c 524288; exec stress-ng --temp-path /var/tmp --vm $vm --vm-bytes 20% --vm-method all --verify --timeout ${DURATION}s --timestamp --metrics-brief" \
        > "$LOGS_DIR/stress-vm.log" 2>&1 &
    vm_pid=$!
    GUEST_TIMEOUT=$(( DURATION + 300 )) guest sh -c \
        "ulimit -c unlimited; exec stress-ng --temp-path /var/tmp --cpu $cpu --cpu-method all --verify --timeout ${DURATION}s --timestamp --metrics-brief" \
        > "$LOGS_DIR/stress-cpu.log" 2>&1 &
    cpu_pid=$!
    wait "$vm_pid" || rc=$?
    wait "$cpu_pid" || rc=$?
    cat "$LOGS_DIR/stress-vm.log" "$LOGS_DIR/stress-cpu.log"
    return "$rc"
}

# The reproducer that gives a rate: compiling the Go standard library forks one
# compiler process per CPU, and about two builds in five died on
# macos-15-intel with a stack pointer nowhere near their goroutine's stack.
load_go() {
    local end n=0 crashed=0 rc out
    end=$(( $(date +%s) + DURATION ))
    while (( $(date +%s) < end )); do
        n=$(( n + 1 ))
        log "go build -a std, build $n"
        rc=0
        # GOTRACEBACK=crash re-raises the fatal signal under SIG_DFL after the
        # traceback, so a corrupted compiler leaves a core behind.
        out=$(GUEST_TIMEOUT=$(( DURATION + 600 )) guest sh -c \
            'ulimit -c unlimited; GOTRACEBACK=crash go build -a std' 2>&1) || rc=$?
        printf '%s\n' "$out"
        log "build $n exited $rc"
        if (( rc != 0 )); then
            # Anything without a crash marker is a wedged guest, not a build
            # result, so stop rather than spin through thousands of them.
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
    local name=$1 rc=0 start end
    if ! declare -F "load_$name" >/dev/null; then
        echo "Unknown load: $name" >&2
        exit 1
    fi
    start=$(date -u +%FT%TZ)
    log "=== $name: start"
    "load_$name" 2>&1 | tee "$LOGS_DIR/$name.log" || rc=$?
    end=$(date -u +%FT%TZ)
    log "=== $name: exit $rc"
    printf '%s %s %s %s\n' "$name" "$rc" "$start" "$end" >> "$LOGS_DIR/loads.txt"
}

collect_cores() {
    GUEST_TIMEOUT=300 root ls -l /var/tmp/cores > "$LOGS_DIR/cores.txt" 2>&1 || true
    cat "$LOGS_DIR/cores.txt"
}

# The lines from a load's log that say how it went.
result_lines() {
    case $1 in
    clock)
        grep -E '^mode=|^anomaly:|could not build' "$LOGS_DIR/clock.log" || true
        echo "crash markers: $(grep -cE "$go_signatures" "$LOGS_DIR/clock.log" || true)"
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
    local serial
    root dmesg > "$LOGS_DIR/dmesg.log" 2>&1 || true
    cp "$instance_dir"/*.log "$LOGS_DIR/" 2>/dev/null || true
    {
        echo "## Lima clock: ${ImageOS:-?} $(uname -m), $VM_TYPE, $CPUS vCPUs, ${MEMORY} GB, cmdline \`${CMDLINE:-<none>}\`, ${DURATION}s per load"
        echo
        echo '```'
        grep -hE '^(guest (cmdline|clocksource)|lima driver|host virtualized)|Marking TSC' \
            "$LOGS_DIR"/info-*.txt || true
        echo '```'
        echo
        if [[ -f $LOGS_DIR/loads.txt ]]; then
            while read -r name rc start end; do
                echo "### $name (exit $rc, $start to $end)"
                echo
                echo '```'
                result_lines "$name"
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
        for serial in "$LOGS_DIR"/serial*.log; do
            [[ -f $serial ]] || continue
            grep -E "$signatures" "$serial" || echo "none in ${serial##*/}"
        done
        echo '```'
        if [[ -s $LOGS_DIR/cores.txt ]]; then
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
resolve_cmdline
start_vm
install_packages
record_info boot1
apply_cmdline
record_info boot2
prepare_guest
for load in $LOADS; do
    run_load "$load"
done
collect_cores
