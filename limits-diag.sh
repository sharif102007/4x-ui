#!/usr/bin/env bash
#
# 4x-ui limits diagnostics
#
# Dumps everything needed to answer "is the speed/traffic limit actually being
# enforced?" without guessing: the nftables tables the panel owns, their byte
# counters, any tc shaping state, and the relevant kernel modules.
#
#   limits-diag.sh            full report
#   limits-diag.sh counters   byte counters only (safe to poll)

set -uo pipefail

SSH_TABLE="fourxui_ssh"
XRAY_TABLE="fourxui_xray"

green='\033[0;32m'
yellow='\033[0;33m'
red='\033[0;31m'
plain='\033[0m'

hdr() { echo -e "\n${green}=== $* ===${plain}"; }
warn() { echo -e "${yellow}$*${plain}"; }
bad() { echo -e "${red}$*${plain}"; }

have() { command -v "$1" >/dev/null 2>&1; }

show_table() {
    local table="$1"
    if ! have nft; then
        bad "nft not installed - no nftables limit can be enforced"
        return
    fi
    if nft list table inet "${table}" >/dev/null 2>&1; then
        nft list table inet "${table}"
    else
        warn "table inet ${table} does not exist (no active limits from this subsystem)"
    fi
}

show_counters() {
    local table="$1"
    if ! have nft; then
        return
    fi
    if ! nft list table inet "${table}" >/dev/null 2>&1; then
        warn "table inet ${table}: absent"
        return
    fi
    # counter lines look like:  counter xray_2a1b3c_up { packets 12 bytes 3456 }
    nft -a list table inet "${table}" 2>/dev/null |
        awk '/counter [a-zA-Z0-9_]+ \{/ {
                name=$2
                for (i=1;i<=NF;i++) {
                    if ($i=="packets") pkts=$(i+1)
                    if ($i=="bytes") byts=$(i+1)
                }
                printf "  %-28s packets=%-12s bytes=%s\n", name, pkts, byts
            }'
}

show_tc() {
    if ! have tc; then
        warn "tc not installed (iproute2 missing) - no queue-based shaping possible"
        return
    fi
    local dev
    for dev in $(ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | cut -d'@' -f1); do
        case "${dev}" in
            lo) continue ;;
        esac
        local qdisc
        qdisc=$(tc qdisc show dev "${dev}" 2>/dev/null)
        if [[ -n "${qdisc}" ]]; then
            echo "--- ${dev} ---"
            echo "${qdisc}"
            tc -s class show dev "${dev}" 2>/dev/null | head -40
            tc filter show dev "${dev}" 2>/dev/null | head -20
        fi
    done
}

show_modules() {
    local m
    for m in nf_tables nf_conntrack sch_htb sch_ingress act_mirred act_connmark cls_fw ifb tcp_bbr; do
        if [[ -d "/sys/module/${m}" ]]; then
            echo "  loaded      ${m}"
        elif have modinfo && modinfo "${m}" >/dev/null 2>&1; then
            echo "  available   ${m} (not loaded)"
        else
            echo "  MISSING     ${m}"
        fi
    done
}

do_counters() {
    hdr "SSH byte counters (${SSH_TABLE})"
    show_counters "${SSH_TABLE}"
    hdr "Xray byte counters (${XRAY_TABLE})"
    show_counters "${XRAY_TABLE}"
}

do_full() {
    hdr "tooling"
    for c in nft tc ip ss iperf3 sysctl; do
        if have "${c}"; then
            echo "  present     ${c}"
        else
            echo "  MISSING     ${c}"
        fi
    done

    hdr "kernel modules"
    show_modules

    hdr "nftables: ${SSH_TABLE}"
    show_table "${SSH_TABLE}"

    hdr "nftables: ${XRAY_TABLE}"
    show_table "${XRAY_TABLE}"

    do_counters

    hdr "tc shaping state"
    show_tc

    hdr "socket summary"
    if have ss; then
        ss -s 2>/dev/null
        echo
        echo "listening sockets:"
        ss -tulnp 2>/dev/null | head -30
    else
        warn "ss not available"
    fi

    hdr "congestion control"
    sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null | sed 's/^/  active:    /'
    sysctl -n net.ipv4.tcp_available_congestion_control 2>/dev/null | sed 's/^/  available: /'
    sysctl -n net.core.default_qdisc 2>/dev/null | sed 's/^/  qdisc:     /'

    hdr "how to interpret this"
    cat <<'EOF'
  * A table listed as "absent" means that subsystem is enforcing nothing.
  * Byte counters that stay at 0 while a client is transferring data mean the
    packet mark is not reaching the chain: the rule exists but never matches.
  * Counters rising while throughput is unlimited means marking works but the
    rate rule is not effective.
  * Verify real throughput separately, e.g.:
      iperf3 -s                      (on a second host)
      iperf3 -c <host> -t 30         TCP
      iperf3 -c <host> -u -b 100M    UDP
EOF
}

case "${1:-full}" in
    counters) do_counters ;;
    full) do_full ;;
    *)
        echo "usage: $0 {full|counters}"
        exit 1
        ;;
esac
