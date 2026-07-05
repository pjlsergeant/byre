#!/usr/bin/env bash
# byre-firewall — apply default-deny egress to the box's network namespace.
#
# Runs OUTSIDE the box: byre launches this as the entrypoint of a
# run-to-completion helper container that shares ONLY the box's netns
# (--net=container:<box>, -u 0:0, --cap-add NET_ADMIN). The box has neither
# root nor NET_ADMIN, so the rules programmed here are out of the agent's
# reach. The box's launcher is meanwhile waiting at the launch gate; we only
# open it (by listening on the gate port) after the rules are applied and
# verified. Every failure in this script therefore fails CLOSED: the gate
# never opens, the box times out and exits.
#
# This file is also baked into the box's image (inert there — no privileges
# inside), which is what lets the helper reuse the box's own image.
set -euo pipefail

log() { echo "byre-firewall: $*" >&2; }
die() { log "FATAL: $* — the launch gate stays shut; the box will exit (fail closed)."; exit 1; }

# The default allowlist: the built-in agents' API/auth endpoints, github, and
# the package registries the built-in templates imply. Static v1 snapshot —
# generous but bounded. Extend per project with FIREWALL_ALLOW (space- or
# comma-separated hostnames) in byre.config env.
DEFAULT_ALLOW=(
  # anthropic / claude
  api.anthropic.com console.anthropic.com claude.ai statsig.anthropic.com
  # openai / codex
  api.openai.com auth.openai.com chatgpt.com
  # google / gemini
  generativelanguage.googleapis.com cloudcode-pa.googleapis.com
  oauth2.googleapis.com accounts.google.com
  # git hosting
  github.com api.github.com codeload.github.com
  objects.githubusercontent.com raw.githubusercontent.com
  # package registries (node / go / python / debian)
  registry.npmjs.org
  proxy.golang.org sum.golang.org storage.googleapis.com
  pypi.org files.pythonhosted.org
  deb.debian.org security.debian.org
)

GATE_FILE=/etc/byre/launch-gate
[ -s "$GATE_FILE" ] || die "no gate file at $GATE_FILE (image mismatch?)"
gate_port="$(tr -cd '0-9' < "$GATE_FILE")"
[ -n "$gate_port" ] || die "gate file holds no port"

# Assemble the domain list: defaults + FIREWALL_ALLOW extension.
domains=("${DEFAULT_ALLOW[@]}")
for d in $(echo "${FIREWALL_ALLOW:-}" | tr ',' ' '); do
  domains+=("$d")
done

# Resolve every domain to its current A/AAAA records via getent (libc — no
# extra tooling). Per-domain failure is a warning (the host may simply not
# exist yet); resolving NOTHING means DNS itself is broken, which would leave
# an all-DROP box — die instead so the failure is legible at launch.
v4s=() v6s=()
for d in "${domains[@]}"; do
  ips="$(getent ahosts "$d" 2>/dev/null | awk '{print $1}' | sort -u)" || true
  if [ -z "$ips" ]; then
    log "warning: cannot resolve $d — it will be blocked"
    continue
  fi
  for ip in $ips; do
    case "$ip" in
      *:*) v6s+=("$ip") ;;
      *) v4s+=("$ip") ;;
    esac
  done
done
[ "${#v4s[@]}" -gt 0 ] || die "resolved no allowlisted IPv4 addresses (is DNS working?)"

# Rules are appended (allows) with the policy flipped to DROP LAST, per family
# — the box's launcher is parked at the gate, so nothing runs during assembly.
# -w waits on the xtables lock rather than racing it.
ipt() { iptables -w "$@"; }
ipt6() { ip6tables -w "$@"; }

# Is there a v6 stack in this netns at all? A host without one has nothing to
# leak through v6 — skip rather than dying on missing modules.
ip6_ok=
if ip6tables -w -L OUTPUT >/dev/null 2>&1; then
  ip6_ok=1
else
  log "IPv6 unavailable in this netns; skipping ip6tables (nothing to leak through)"
fi

# Baseline, both families: loopback (covers Docker's embedded resolver at
# 127.0.0.11) and established/related return traffic.
ipt -A OUTPUT -o lo -j ACCEPT
ipt -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
if [ -n "$ip6_ok" ]; then
  ipt6 -A OUTPUT -o lo -j ACCEPT
  ipt6 -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
fi

# DNS: allow port 53 ONLY to the nameservers this box actually uses (from
# /etc/resolv.conf), not to every host — an unscoped port-53 allow is a direct
# exfil channel to any attacker-run resolver. The loopback resolver (Docker's
# 127.0.0.11) is already covered above; an external resolver (host-network /
# custom --dns) gets a scoped allow here. Resolving through YOUR nameserver is
# still a DNS-tunneling channel (the documented v1 hole; v2 may force a
# filtering resolver), but no longer an open channel to an arbitrary one.
for ns in $(awk '/^nameserver/ {print $2}' /etc/resolv.conf 2>/dev/null | sort -u); do
  case "$ns" in
    127.*|::1) continue ;; # loopback resolver already allowed via -o lo
    *:*)
      [ -n "$ip6_ok" ] || continue
      ipt6 -A OUTPUT -d "$ns" -p udp --dport 53 -j ACCEPT
      ipt6 -A OUTPUT -d "$ns" -p tcp --dport 53 -j ACCEPT
      ;;
    *)
      ipt -A OUTPUT -d "$ns" -p udp --dport 53 -j ACCEPT
      ipt -A OUTPUT -d "$ns" -p tcp --dport 53 -j ACCEPT
      ;;
  esac
done

# Allowlisted hosts, then DROP policy — v4 always, v6 when present.
for ip in "${v4s[@]}"; do
  ipt -A OUTPUT -d "$ip" -j ACCEPT
done
ipt -P OUTPUT DROP
if [ -n "$ip6_ok" ]; then
  for ip in "${v6s[@]+"${v6s[@]}"}"; do
    ipt6 -A OUTPUT -d "$ip" -j ACCEPT
  done
  ipt6 -P OUTPUT DROP
fi

# Self-verify. The deny probe is the security property: a connect to a
# non-allowlisted address must FAIL (DROP = the connect hangs; timeout kills
# it). If it gets through, the wall isn't real — die, gate stays shut.
deny_probe=1.1.1.1
for ip in "${v4s[@]}"; do
  [ "$ip" = "$deny_probe" ] && deny_probe="" && break
done
if [ -n "$deny_probe" ]; then
  if timeout 3 bash -c "exec 3<>/dev/tcp/$deny_probe/443" 2>/dev/null; then
    die "deny probe reached $deny_probe:443 — rules are not effective"
  fi
fi
# The allow probe is availability, not security: warn only (a flaky edge or
# an odd port must not brick the launch — the deny posture still holds).
if ! timeout 5 bash -c "exec 3<>/dev/tcp/${v4s[0]}/443" 2>/dev/null; then
  log "warning: allow probe to ${v4s[0]}:443 failed — allowlisted egress may be broken"
fi

log "egress deny-by-default applied: ${#v4s[@]} IPv4 / ${#v6s[@]} IPv6 allowlisted addresses"

# Open the launch gate: listen once on the loopback port; the box's launcher
# poll-connects and proceeds. Shared netns = shared loopback, so this is the
# whole signaling channel — stateless, nothing to go stale across restarts.
# The timeout stops the helper hanging forever if the launcher already died.
timeout 60 nc -l 127.0.0.1 "$gate_port" >/dev/null 2>&1 || true
