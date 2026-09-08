#!/bin/sh

# Unpack the apkovl the way the guest's init does at boot, in parallel, for a
# fixed time.  Prints one line per worker with its extraction and failure
# counts, then everything tar wrote to stderr.
#
# Usage: tar-loop.sh <seconds> <workers>

duration=$1
workers=$2
# The boot medium is /media/sda under VZ but a different device under QEMU,
# so find the apkovl rather than assume the path.
# shellcheck disable=SC2012 # the name is fixed and alphanumeric
archive=$(ls /media/*/alpine.apkovl.tar.gz 2>/dev/null | head -n 1)
if [ -z "$archive" ]; then
    echo "apkovl archive not found under /media" >&2
    exit 1
fi
end=$(( $(date +%s) + duration ))
# shellcheck disable=SC3045 # busybox ash has it
ulimit -c unlimited

worker() {
    dir=/var/tmp/tar-loop/$1
    err=/var/tmp/tar-loop/$1.err
    n=0
    failed=0
    while [ "$(date +%s)" -lt "$end" ]; do
        rm -rf "$dir"
        mkdir -p "$dir"
        tar -xzf "$archive" -C "$dir" 2>>"$err" || failed=$((failed + 1))
        n=$((n + 1))
    done
    echo "worker $1: $n extractions, $failed failed"
    rm -rf "$dir"
}

rm -rf /var/tmp/tar-loop
mkdir -p /var/tmp/tar-loop
i=0
while [ "$i" -lt "$workers" ]; do
    worker "$i" &
    i=$((i + 1))
done
wait
cat /var/tmp/tar-loop/*.err
