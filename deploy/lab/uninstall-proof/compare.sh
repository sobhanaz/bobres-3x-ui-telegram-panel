#!/bin/bash
# Runs INSIDE the lab server: compare two snapshots, file by file. Usage: compare.sh A B [-v]
A=/snapshots/$1
B=/snapshots/$2
echo "== $1 -> $2"
for f in files-meta var-lib var-log run hashes unit-files units timers failed-units ufw iptables ip6tables packages apt-manual passwd group docker fstab swapon mounts processes listening cron; do
  n=$(diff "$A/$f.txt" "$B/$f.txt" | grep -c '^[<>]')
  echo "$f: $n differing lines"
  if [ "${3:-}" = "-v" ] && [ "$n" != 0 ]; then diff "$A/$f.txt" "$B/$f.txt" | grep '^[<>]' | head -${4:-40}; fi
done
