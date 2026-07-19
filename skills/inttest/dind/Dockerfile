# byre's sacrificial integration-test host, as a privileged container.
#
# Stands in for the Lima VM (skills/inttest/byre-inttest.yaml) on hosts that
# can't nest VMs -- e.g. a Hetzner cloud devbox, where /dev/kvm doesn't exist
# and QEMU falls back to full software emulation.
#
# It runs its OWN dockerd, so the gated suite's containers, images and
# networks are invisible to the engine hosting the byre box. Weaker isolation
# than a VM (shared kernel), stronger than sharing the daemon.
#
# The package list mirrors the Lima template's provision block -- keep them
# in step.
FROM debian:trixie

ARG INTTEST_USER=dev

ENV DEBIAN_FRONTEND=noninteractive
# Debian trixie ships Go 1.24; go.mod's floor is 1.25. auto lets the toolchain
# fetch a newer one instead of hard-failing (same trick byre.preset uses).
ENV GOTOOLCHAIN=auto

# podman + crun + uidmap/passt/slirp4netns: the rootless podman path (ADR 0032)
# is part of the gated suite. aardvark-dns is a netavark Recommends that
# --no-install-recommends would drop -- without it container DNS is off and the
# netns-init helper races v6 address setup, failing the v6 firewall guard.
# tmux: the TUI tier (BYRE_TUI_TESTS=1). openssh-server: the wrapper's transport.
RUN apt-get update && apt-get install -y --no-install-recommends \
      git rsync ca-certificates curl golang-go \
      podman crun uidmap passt slirp4netns aardvark-dns tmux \
      openssh-server sudo iproute2 iptables \
 && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://get.docker.com | sh

RUN useradd -m -s /bin/bash "${INTTEST_USER}" \
 && usermod -aG docker "${INTTEST_USER}" \
 && echo "${INTTEST_USER} ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/inttest \
 && chmod 440 /etc/sudoers.d/inttest

# Rootless podman over bare ssh: no user dbus, so buildah RUN steps die on
# "sd-bus: Interactive authentication required" unless the runtime is crun with
# cgroupfs (runc insists on a systemd slice rootlessly).
#
# DELIBERATE DIVERGENCE from the Lima template: it also runs
# `loginctl enable-linger` to keep the user runtime dir alive between ssh
# sessions. There is no systemd here (the entrypoint is dockerd + sshd), so
# linger has nothing to enable -- and rootless podman passes the full gated
# suite without it (verified 2026-07-19). Don't "restore parity" by adding a
# loginctl call that cannot work, and don't drop linger from Lima, where it
# is load-bearing.
RUN mkdir -p /home/${INTTEST_USER}/.config/containers \
 && printf '[engine]\nruntime = "crun"\ncgroup_manager = "cgroupfs"\n' \
      > /home/${INTTEST_USER}/.config/containers/containers.conf \
 && chown -R "${INTTEST_USER}:${INTTEST_USER}" /home/${INTTEST_USER}/.config

# The pubkey is mounted at run time (-v) to /etc/byre-inttest/authorized_key,
# and the entrypoint INSTALLS a copy as authorized_keys -- it is deliberately
# not mounted there directly. The ssh-loop tier (BYRE_SSH_LOOP_TESTS=1, which
# the wrapper always sets) edits the ssh user's ~/.ssh and restores it on
# cleanup; a read-only bind mount fails those tests with
# "authorized_keys: read-only file system". The image still carries no key.
ENV INTTEST_USER=${INTTEST_USER}
RUN mkdir -p /home/${INTTEST_USER}/.ssh /run/sshd /etc/byre-inttest \
 && chmod 700 /home/${INTTEST_USER}/.ssh \
 && chown -R "${INTTEST_USER}:${INTTEST_USER}" /home/${INTTEST_USER}/.ssh \
 && ssh-keygen -A

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

EXPOSE 22
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
