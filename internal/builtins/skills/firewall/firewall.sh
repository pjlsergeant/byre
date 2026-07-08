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

GATE_FILE=/etc/byre/launch-gate
[ -s "$GATE_FILE" ] || die "no gate file at $GATE_FILE (image mismatch?)"
gate_port="$(tr -cd '0-9' < "$GATE_FILE")"
[ -n "$gate_port" ] || die "gate file holds no port"

# The allowlist entries: BYRE_EGRESS, the union byre computes of every enabled
# skill's declared egress AND the user's `egress` config key (ADR 0019),
# already normalized to host:port. NO host list is hardcoded here — each agent
# skill brings its own endpoints and byre unions them. Space- or
# comma-separated. (FIREWALL_ALLOW retired with ADR 0019 — move any value into
# the config's `egress` list.)
entries=()
for e in $(echo "${BYRE_EGRESS:-}" | tr ',' ' '); do
  entries+=("$e")
done
requested=${#entries[@]}

# Resolve each entry to per-(ip,port) accept rules (getent = libc, no extra
# tooling). Per-entry resolution failure is a warning — that host stays blocked.
v4rules=() v6rules=()   # elements: "ip port"
probe_host="" probe_port=""
for e in "${entries[@]+"${entries[@]}"}"; do
  if [[ "$e" == *:* ]]; then host="${e%:*}"; port="${e##*:}"; else host="$e"; port=443; fi
  # Match byre's Go validation (config.ParseEgress): numeric, 1..65535. byre
  # validates everything it puts in BYRE_EGRESS, but this script also runs
  # ejected (byre ejectfirewall) where the value is hand-editable — so reject
  # an out-of-range port here rather than let iptables fail at launch.
  case "$port" in ''|*[!0-9]*) log "warning: bad egress entry '$e' — skipping"; continue ;; esac
  if [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
    log "warning: egress '$e' port out of range — skipping"; continue
  fi
  ips="$(getent ahosts "$host" 2>/dev/null | awk '{print $1}' | sort -u)" || true
  if [ -z "$ips" ]; then
    log "warning: cannot resolve $host — it will be blocked"
    continue
  fi
  for ip in $ips; do
    case "$ip" in
      *:*) v6rules+=("$ip $port") ;;
      *)
        v4rules+=("$ip $port")
        [ -n "$probe_host" ] || { probe_host="$ip"; probe_port="$port"; }
        ;;
    esac
  done
done

# An EMPTY allowlist is a legitimate, maximally-locked box (only DNS + loopback
# leave); a NON-empty request that resolved nothing means DNS is broken. Die
# only on the latter — a deliberate lockdown is not a failure.
if [ "$requested" -gt 0 ] && [ "${#v4rules[@]}" -eq 0 ] && [ "${#v6rules[@]}" -eq 0 ]; then
  die "requested $requested host(s) but resolved none (is DNS working?)"
fi

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

# Allowlisted (ip, port) rules — TCP to the EXACT port each host was listed for
# (default 443), not all-ports to the IP (shared cloud/CDN addresses front many
# services; scoping to the port is what "allow HTTPS to this host" means). Then
# flip each family's policy to DROP.
for r in "${v4rules[@]+"${v4rules[@]}"}"; do
  read -r ip port <<<"$r"
  ipt -A OUTPUT -d "$ip" -p tcp --dport "$port" -j ACCEPT
done
ipt -P OUTPUT DROP
if [ -n "$ip6_ok" ]; then
  for r in "${v6rules[@]+"${v6rules[@]}"}"; do
    read -r ip port <<<"$r"
    ipt6 -A OUTPUT -d "$ip" -p tcp --dport "$port" -j ACCEPT
  done
  ipt6 -P OUTPUT DROP
fi

# Self-verify. The deny probe is the security property: a connect to a
# non-allowlisted address:PORT must FAIL (DROP = the connect hangs; timeout
# kills it). If it gets through, the wall isn't real — die, gate stays shut.
# The match is on the exact ip+port pair, since rules are port-scoped now: an
# allow of 1.1.1.1:80 must NOT suppress a 1.1.1.1:443 deny check. We pick from
# a small set of stable public IPs so we can always find one that is not
# allowlisted at :443 (a config allowlisting all of them at :443 is pathological
# and would just fall through to no probe — accepted).
deny_probe=""
for cand in 1.1.1.1 8.8.8.8 9.9.9.9; do
  clash=
  for r in "${v4rules[@]+"${v4rules[@]}"}"; do
    read -r ip port <<<"$r"
    [ "$ip" = "$cand" ] && [ "$port" = "443" ] && { clash=1; break; }
  done
  [ -z "$clash" ] && { deny_probe="$cand"; break; }
done
if [ -n "$deny_probe" ]; then
  if timeout 3 bash -c "exec 3<>/dev/tcp/$deny_probe/443" 2>/dev/null; then
    die "deny probe reached $deny_probe:443 — rules are not effective"
  fi
else
  log "warning: could not pick a non-allowlisted deny probe; skipping the drop check"
fi
# The allow probe is availability, not security: warn only (a flaky edge must
# not brick the launch — the deny posture still holds). Skipped for a
# deliberately empty allowlist (no host to probe).
if [ -n "$probe_host" ]; then
  if ! timeout 5 bash -c "exec 3<>/dev/tcp/$probe_host/$probe_port" 2>/dev/null; then
    log "warning: allow probe to $probe_host:$probe_port failed — allowlisted egress may be broken"
  fi
fi

log "egress deny-by-default applied: ${#v4rules[@]} IPv4 / ${#v6rules[@]} IPv6 allow rules"

# Open the launch gate: listen once on the loopback port; the box's launcher
# poll-connects and proceeds. Shared netns = shared loopback, so this is the
# whole signaling channel — stateless, nothing to go stale across restarts.
# The timeout stops the helper hanging forever if the launcher already died.
timeout 60 nc -l 127.0.0.1 "$gate_port" >/dev/null 2>&1 || true
