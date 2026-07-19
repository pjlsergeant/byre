#!/bin/bash
# Start the container's own dockerd, then sshd in the foreground.
set -eu

# Install the mounted pubkey as a REAL file rather than binding it into place:
# the ssh-loop tier rewrites ~/.ssh and restores it, which a read-only mount
# would block. Re-installed each start, so a container restart is also the
# reset if a test leaves it dirty.
KEY_SRC=/etc/byre-inttest/authorized_key
AUTH="/home/${INTTEST_USER}/.ssh/authorized_keys"
if [ -f "$KEY_SRC" ]; then
  install -o "$INTTEST_USER" -g "$INTTEST_USER" -m 600 "$KEY_SRC" "$AUTH"
else
  echo "entrypoint: no pubkey at $KEY_SRC -- ssh will reject every key" >&2
fi

# Rootless podman's graph storage has the same overlay-on-overlay problem
# dockerd does, in a different place: mount a volume at this path too (see
# docs/BYRE-DEVELOPMENT.md) or podman fails with "'overlay' is not supported
# over overlayfs". A fresh volume mounts in root-owned; podman rootless needs
# it owned by the ssh user.
PODMAN_STORE="/home/${INTTEST_USER}/.local/share/containers"
mkdir -p "$PODMAN_STORE"
chown -R "$INTTEST_USER:$INTTEST_USER" "/home/${INTTEST_USER}/.local"

dockerd >/var/log/dockerd.log 2>&1 &

# Wait for the daemon rather than racing it: the first `byre-inttest` run
# otherwise lands before the socket exists and fails confusingly.
for i in $(seq 1 60); do
  if docker info >/dev/null 2>&1; then
    echo "entrypoint: dockerd up after ${i}s"
    break
  fi
  sleep 1
done
docker info >/dev/null 2>&1 || {
  echo "entrypoint: dockerd failed to start; last log lines:" >&2
  tail -20 /var/log/dockerd.log >&2
  exit 1
}

exec /usr/sbin/sshd -D -e
