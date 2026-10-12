# A "fresh Ubuntu 24.04 server" as a container: systemd is PID 1, so installers that use
# systemctl (3x-ui's install.sh, Docker's get.docker.com, the BOBRES installer) work as on
# a real VPS. Nothing of the project is pre-installed: you log in and install it yourself.
# Needs `privileged: true` (deploy/lab/docker-compose.yml): systemd and Docker-in-Docker.
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    container=docker

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      systemd systemd-sysv dbus openssh-server sudo \
      ca-certificates curl wget gnupg openssl \
      iproute2 iputils-ping iptables net-tools dnsutils socat \
      procps psmisc less nano vim-tiny tzdata lsb-release \
      tar gzip unzip xz-utils cron logrotate \
 && rm -rf /var/lib/apt/lists/* \
 # Units that make no sense inside a container (the network and DNS come from Docker).
 && systemctl mask systemd-networkd.service systemd-networkd.socket \
      systemd-networkd-wait-online.service systemd-resolved.service \
      systemd-udevd.service systemd-udevd-control.socket systemd-udevd-kernel.socket \
      systemd-modules-load.service systemd-firstboot.service \
 # SSH: root with a key only (deploy/lab/docker-compose.yml mounts your public key).
 # Setting a root password later (`passwd`) also allows password logins.
 && mkdir -p -m 700 /root/.ssh \
 && printf 'PermitRootLogin yes\nPasswordAuthentication yes\nPubkeyAuthentication yes\n' \
      > /etc/ssh/sshd_config.d/10-lab.conf \
 && systemctl enable ssh

STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
