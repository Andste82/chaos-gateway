#!/usr/bin/env bash
# Runs the follow-up spikes that need the full Ubuntu kernel in one VM session:
#   ./vm.sh followups.sh
cd "$(dirname "$0")"
for s in s11-direction s12-attachment s14-local-replies; do
  echo "=== $s"
  bash "$s/run.sh" > "results/tmp/vm-$s.log" 2>&1
done
