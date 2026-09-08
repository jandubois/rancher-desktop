#!/bin/sh

# Stream a tarball of the core dumps under /tmp/cores to stdout, with the
# binaries and shared libraries a debugger needs to read them.  The listing
# goes to stderr.  Cores over 200 MB stay behind.
#
# Usage: collect-cores.sh > cores.tar.gz 2> cores.txt

dir=/tmp/cores
ls -la "$dir" >&2
files=$(find "$dir" -type f -size -200000k)
[ -n "$files" ] || exit 0

# shellcheck disable=SC2086 # one path per word, by construction
set -- $files /lib/ld-musl-*.so.1
for bin in /bin/busybox /usr/bin/stress-ng; do
    [ -e "$bin" ] || continue
    set -- "$@" "$bin"
    for lib in $(ldd "$bin" 2>/dev/null | awk '/=>/ { print $3 }'); do
        [ -e "$lib" ] && set -- "$@" "$lib"
    done
done
echo "bundling: $*" >&2
tar czf - "$@"
