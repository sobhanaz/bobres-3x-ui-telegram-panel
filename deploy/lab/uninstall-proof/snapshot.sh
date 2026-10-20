#!/bin/bash
# Snapshot of the host state an install could change, for proving `bobres uninstall --purge` on a lab
# server (deploy/lab/README.md, "Proving the uninstall"). Run it INSIDE the server, with the output
# directory outside the roots it scans (/etc /usr/local /opt /root ...), for example:
#   bash snapshot.sh /snapshots/day0          before BOBRES is installed
#   bash snapshot.sh /snapshots/afterpurge    after the purge
#   bash compare.sh day0 afterpurge -v        what differs, file by file
# Everything is sorted and free of timestamps and counters, so two snapshots of an idle server are identical.
set -u
out=$1
rm -rf "$out"
mkdir -p "$out"
export LC_ALL=C

# --- file names and metadata (not content) ---
meta() { find "$@" -xdev -printf '%p|%y|%m|%U:%G|%l\n' 2>/dev/null | sort; }
{
  meta /etc
  meta /usr/local /opt /root /srv /home /boot /media /mnt
  meta /var/spool /var/backups /var/mail /var/opt /var/cache
  meta /usr/share/keyrings /usr/lib/systemd /lib/systemd
  meta /usr/bin /usr/sbin /usr/libexec /usr/lib/x86_64-linux-gnu/docker /usr/share/doc
} | grep -v -e '^/etc/resolv.conf|' -e '^/etc/hosts|' -e '^/etc/hostname|' -e '^/etc/mtab|' >"$out/files-meta.txt"

# /var/lib and /var/log: names only, Docker's and containerd's data only one level deep (their content is volumes)
{
  find /var/lib -xdev \( -path /var/lib/docker -o -path /var/lib/containerd \) -prune -o -printf '%p|%y\n' 2>/dev/null
  find /var/lib/docker /var/lib/containerd -xdev -maxdepth 1 -printf '%p|%y\n' 2>/dev/null
} | sort >"$out/var-lib.txt"
find /var/log -xdev -printf '%p|%y\n' 2>/dev/null | sort >"$out/var-log.txt"
find /run -xdev -maxdepth 2 -printf '%p|%y\n' 2>/dev/null | sort >"$out/run.txt"

# --- content hashes of configuration ---
find /etc /usr/local /opt /root -xdev -type f -print0 2>/dev/null | sort -z |
  xargs -0 sha256sum 2>/dev/null | grep -v -e ' /etc/resolv.conf$' -e ' /etc/hosts$' -e ' /etc/hostname$' >"$out/hashes.txt"

# --- systemd ---
systemctl list-unit-files --no-legend --no-pager 2>/dev/null | awk '{print $1, $2}' | sort >"$out/unit-files.txt"
systemctl list-units --all --no-legend --no-pager 2>/dev/null | sed 's/^[ ●*]*//' | awk '{print $1, $2, $3, $4}' | sort >"$out/units.txt"
systemctl list-timers --all --no-legend --no-pager 2>/dev/null | awk '{print $(NF-1), $NF}' | sort >"$out/timers.txt"
systemctl --failed --no-legend --no-pager 2>/dev/null | awk '{print $1}' | sort >"$out/failed-units.txt"

# --- firewall ---
{
  echo "ufw: $(command -v ufw || echo none)"
  command -v ufw >/dev/null && { ufw status verbose 2>&1; ufw show added 2>&1; }
} >"$out/ufw.txt"
iptables-save 2>/dev/null | sed 's/\[[0-9]*:[0-9]*\]//' | grep -v '^#' | sort >"$out/iptables.txt"
ip6tables-save 2>/dev/null | sed 's/\[[0-9]*:[0-9]*\]//' | grep -v '^#' | sort >"$out/ip6tables.txt"

# --- packages, users, groups ---
dpkg-query -W -f='${Package} ${Version} ${db:Status-Abbrev}\n' 2>/dev/null | sort >"$out/packages.txt"
apt-mark showmanual 2>/dev/null | sort >"$out/apt-manual.txt"
getent passwd | cut -d: -f1,3,4,6,7 | sort >"$out/passwd.txt"
getent group | sort >"$out/group.txt"

# --- docker ---
{
  if command -v docker >/dev/null 2>&1; then
    echo "docker: installed ($(docker --version 2>&1))"
    echo "--- containers"; docker ps -a --format '{{.Names}}|{{.Image}}|{{.Labels}}' 2>&1 | sort
    echo "--- images"; docker images --digests --format '{{.Repository}}:{{.Tag}}@{{.Digest}}|{{.ID}}' 2>&1 | sort
    echo "--- volumes"; docker volume ls --format '{{.Name}}' 2>&1 | sort
    echo "--- networks"; docker network ls --format '{{.Name}}|{{.Driver}}' 2>&1 | sort
  else
    echo "docker: not installed"
  fi
} >"$out/docker.txt"

# --- misc ---
cat /etc/fstab >"$out/fstab.txt" 2>&1
swapon --show >"$out/swapon.txt" 2>&1
findmnt -rn -o TARGET,FSTYPE 2>/dev/null | sort >"$out/mounts.txt"
ps -eo user:20,comm 2>/dev/null | sort -u >"$out/processes.txt"
ss -H -tulpn 2>/dev/null | awk '{print $1, $5, $7}' | sort >"$out/listening.txt"
{ ls -la /etc/cron.d /var/spool/cron 2>&1; crontab -l 2>&1; } | sed 's/ [A-Z][a-z][a-z] [ 0-9][0-9] [0-9:]* / DATE /' >"$out/cron.txt"
echo "snapshot $(basename "$out"): $(wc -l <"$out/files-meta.txt") files, $(wc -l <"$out/packages.txt") packages"
