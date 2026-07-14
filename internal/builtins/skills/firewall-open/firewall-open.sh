#!/usr/bin/env bash
# byre-firewall-open — open egress with the config's closures dropped.
#
# The firewall skill's sibling, sharing its vehicle: byre runs this OUTSIDE
# the box as the entrypoint of a run-to-completion helper container that
# shares ONLY the box's netns (--net=container:<box>, -u 0:0, --cap-add
# NET_ADMIN). The box has neither root nor NET_ADMIN, so the drops are out of
# the agent's reach. The box's launcher waits at the launch gate; we open it
# only after the drops are applied and verified. Every failure fails CLOSED:
# the gate never opens, the box times out and exits — byre never launches a
# box under a posture claim it can't make.
#
# What this does NOT claim: the blocking is an IP snapshot resolved now,
# aimed at well-behaved clients (telemetry SDKs). A host rotating IPs
# mid-session, or a process resolving through another channel, can slip it.
# Best-effort hygiene on an open network — the deny-by-default firewall is
# the containment posture.
#
# This file is also baked into the box's image (inert there — no privileges
# inside), which is what lets the helper reuse the box's own image.
set -euo pipefail

log() { echo "byre-firewall-open: $*" >&2; }
die() { log "FATAL: $* — the launch gate stays shut; the box will exit (fail closed)."; exit 1; }

GATE_FILE=/etc/byre/launch-gate
[ -s "$GATE_FILE" ] || die "no gate file at $GATE_FILE (image mismatch?)"
gate_port="$(tr -cd '0-9' < "$GATE_FILE")"
[ -n "$gate_port" ] || die "gate file holds no port"

# The denylist: BYRE_EGRESS_DENY, the config's `!host[:port]` closures that
# survived the cascade (ADR 0030), stripped of the '!'. Space- or comma-
# separated. A PORTLESS entry drops every port and protocol to the host; a
# ported one drops that TCP/UDP port (UDP too — QUIC rides udp/443).
entries=()
for e in $(echo "${BYRE_EGRESS_DENY:-}" | tr ',' ' '); do
  entries+=("$e")
done

# Resolve each closure to per-IP drop rules. Unlike the deny-by-default
# helper (where an unresolved host stays blocked — the safe direction), an
# unresolved host HERE would stay silently reachable while status claims it
# blocked, so resolution failure is fatal (grilled 2026-07-14).
v4rules=() v6rules=()   # elements: "ip port" (port "" = every port/protocol)
for e in "${entries[@]+"${entries[@]}"}"; do
  if [[ "$e" == *:* ]]; then host="${e%:*}"; port="${e##*:}"; else host="$e"; port=""; fi
  # Mirror byre's Go validation (config.ParseEgress) for the ejected /
  # hand-edited case; byre itself never emits a bad entry.
  if [ -n "$port" ]; then
    case "$port" in *[!0-9]*) die "bad denylist entry '$e'" ;; esac
    if [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
      die "denylist entry '$e': port out of range"
    fi
  fi
  ips="$(getent ahosts "$host" 2>/dev/null | awk '{print $1}' | sort -u)" || true
  [ -n "$ips" ] || die "cannot resolve $host — it would stay reachable under an 'N hosts blocked' claim"
  for ip in $ips; do
    case "$ip" in
      *:*) v6rules+=("$ip $port") ;;
      *) v4rules+=("$ip $port") ;;
    esac
  done
done

ipt() { iptables -w "$@"; }
ipt6() { ip6tables -w "$@"; }

# Is there a v6 stack in this netns at all? Without one there is nothing to
# drop over v6 — skip rather than dying on missing modules. But when closures
# DID resolve to v6 addresses AND the namespace has non-loopback v6
# interfaces (read straight from /proc — no iproute2 in the image), a broken
# ip6tables would leave those addresses silently reachable under an
# "N hosts blocked" claim: die instead, same ruling as an unresolvable host.
ip6_ok=
if ip6tables -w -L OUTPUT >/dev/null 2>&1; then
  ip6_ok=1
elif [ "${#v6rules[@]}" -eq 0 ] || [ ! -e /proc/net/if_inet6 ]; then
  # Nothing resolved to v6, or the kernel has no v6 stack at all (file
  # absent) — nothing to block there.
  log "IPv6 unavailable in this netns; skipping ip6tables (nothing to block there)"
else
  # v6-resolved closures exist: skip ONLY on proof of safety (grep exit 1:
  # file readable, lo-only or empty). A non-lo interface (0) or an
  # unreadable file (2) both mean those addresses may stay reachable under
  # an "N hosts blocked" claim — die either way.
  rc=0
  grep -qv ' lo$' /proc/net/if_inet6 || rc=$?
  if [ "$rc" -eq 1 ]; then
    log "IPv6 unavailable in this netns; skipping ip6tables (nothing to block there)"
  else
    die "closures resolved to IPv6 addresses but ip6tables is unavailable while this netns has IPv6 interfaces (or /proc/net/if_inet6 is unreadable) — they would stay reachable"
  fi
fi

# The drops. Policy stays ACCEPT — this is a denylist on an open network, and
# nothing else changes: no baseline rules, DNS untouched (names still resolve;
# only connecting to these IPs is refused).
add_drop() { # add_drop <family:4|6> <ip> <port-or-empty>
  local fam="$1" ip="$2" port="$3" cmd
  case "$fam" in 4) cmd=ipt ;; *) cmd=ipt6 ;; esac
  if [ -n "$port" ]; then
    "$cmd" -A OUTPUT -d "$ip" -p tcp --dport "$port" -j DROP
    "$cmd" -A OUTPUT -d "$ip" -p udp --dport "$port" -j DROP
  else
    "$cmd" -A OUTPUT -d "$ip" -j DROP
  fi
}
for r in "${v4rules[@]+"${v4rules[@]}"}"; do
  read -r ip port <<<"$r" || true
  add_drop 4 "$ip" "${port:-}"
done
if [ -n "$ip6_ok" ]; then
  for r in "${v6rules[@]+"${v6rules[@]}"}"; do
    read -r ip port <<<"$r" || true
    add_drop 6 "$ip" "${port:-}"
  done
fi

# Self-verify. The deny probe is the posture's promise: a connect to a
# blocked ip:port must FAIL (DROP = the connect hangs; timeout kills it).
# Reaching it means the drops aren't effective — die, gate stays shut. A
# portless closure is probed at :443 (any port would do — the rule is
# port-agnostic). An unreachable-anyway host also fails the connect, which
# verifies nothing extra but costs nothing. Skipped when the denylist is
# empty (legal: an open box that says so).
if [ "${#v4rules[@]}" -gt 0 ]; then
  read -r probe_ip probe_port <<<"${v4rules[0]}" || true
  if timeout 3 bash -c "exec 3<>/dev/tcp/$probe_ip/${probe_port:-443}" 2>/dev/null; then
    die "deny probe reached $probe_ip:${probe_port:-443} — drops are not effective"
  fi
fi
# Same check per family: an IPv6-only closure must not go unverified (a
# netns without v6 connectivity fails the connect anyway — unreachable and
# blocked read the same, and both keep the promise).
if [ -n "$ip6_ok" ] && [ "${#v6rules[@]}" -gt 0 ]; then
  read -r probe6_ip probe6_port <<<"${v6rules[0]}" || true
  if timeout 3 bash -c "exec 3<>/dev/tcp/$probe6_ip/${probe6_port:-443}" 2>/dev/null; then
    die "deny probe reached [$probe6_ip]:${probe6_port:-443} — v6 drops are not effective"
  fi
fi
# The open probe is availability, not security: the network is supposed to be
# OPEN, so a bug dropping everything should be seen. Warn only — an isolated
# network must not brick the launch. Candidates skip IPs the denylist owns.
open_probe=""
for cand in 1.1.1.1 8.8.8.8 9.9.9.9; do
  clash=
  for r in "${v4rules[@]+"${v4rules[@]}"}"; do
    read -r ip _ <<<"$r" || true
    [ "$ip" = "$cand" ] && { clash=1; break; }
  done
  [ -z "$clash" ] && { open_probe="$cand"; break; }
done
if [ -n "$open_probe" ]; then
  if ! timeout 5 bash -c "exec 3<>/dev/tcp/$open_probe/443" 2>/dev/null; then
    log "warning: open probe to $open_probe:443 failed — the open network may not actually be reachable"
  fi
fi

log "open-denylist applied: ${#entries[@]} host(s), ${#v4rules[@]} IPv4 / ${#v6rules[@]} IPv6 drop rules"

# Open the launch gate: listen once on the loopback port; the box's launcher
# poll-connects and proceeds. Same single-hook caveat as the firewall: this
# hook opening the gate itself is only sound while byre permits one
# netns_init per box (skill resolution enforces it) — see commands/netns.go.
timeout 60 nc -l 127.0.0.1 "$gate_port" >/dev/null 2>&1 || true
