#!/usr/bin/env bash
# Run a spike script inside QEMU (do not run host spikes at the same time:
# /run/netns is shared with the guest) with a stock Ubuntu 24.04 kernel (6.8.0-generic).
# The host filesystem is shared; the spike repo is writable so results land in results/.
#   ./vm.sh s02-s10-classification/run.sh
# Works without a terminal too (script(1) provides the pseudo-terminal that vng needs).
set -euo pipefail
REPO=$(cd "$(dirname "$0")" && pwd)
KVER=${KVER:-6.8.0-142-generic}
ACCEL=--disable-kvm; [ -e /dev/kvm ] && ACCEL=""
CMD=(vng -r "$KVER" $ACCEL --memory "${VM_MEM:-2G}" --cpus "${VM_CPUS:-2}"
  --rwdir="$REPO" --exec "cd $REPO && modprobe -a sch_netem sch_htb cls_fw cls_u32 cls_flower ifb act_mirred sch_prio 2>/dev/null; bash $1 > $REPO/results/tmp/vm-\$(basename \$(dirname $1))-\$(basename $1 .sh).log 2>&1")
# vng refuses to start without a pseudo-terminal (scripts, CI): provide one with script(1)
if [ -t 0 ] && [ -t 1 ]; then
  exec "${CMD[@]}"
fi
exec script -qec "$(printf '%q ' "${CMD[@]}")" /dev/null
