#!/usr/bin/env bash

# Summarize the host's CPU idle time between two moments from the macOS
# telemetry sampler's log.  Reads top's "CPU usage" line, which is a live
# sample; the ps listing in the same file reports lifetime averages.
#
# Usage: host-idle.sh <host-probe.log> <start> <end>
# Times are UTC in the sampler's own %FT%TZ form, so they compare as strings.

set -o errexit -o nounset -o pipefail

probe=$1
start=$2
end=$3

awk -v start="$start" -v end="$end" '
    /^20[0-9][0-9]-[0-9][0-9]-[0-9][0-9]T/ { stamp = $1 }
    /^CPU usage:/ && stamp >= start && stamp <= end {
        idle = $7
        sub(/%/, "", idle)
        print idle
    }
' "$probe" | sort -n | awk '
    { idle[NR] = $1 }
    END {
        if (NR == 0) {
            print "no samples"
        } else {
            printf "%d samples, idle min %.1f%% median %.1f%% max %.1f%%\n",
                NR, idle[1], idle[int((NR + 1) / 2)], idle[NR]
        }
    }
'
