#!/usr/bin/env bash
#
# 4x-ui queue-based traffic shaper
#
# Replaces the effect of the nftables drop-policer with a real queueing shaper:
#   * upload   -> HTB on the WAN interface egress, classified by packet mark
#   * download -> ingress redirect to an IFB device, mark restored from conntrack
#                 with `action connmark`, then HTB on the IFB egress
#
# Rates and marks are atomically published by the panel under /run/4xui-shaper.
# nftables remains the fallback policer until HTB/IFB is confirmed healthy.
# Once queueing is active, only the nft drop-policer rules are removed; mark and
# traffic-counter rules remain in place.
#
#   4xui-shaper.sh apply [iface]      install shaping
#   4xui-shaper.sh rollback [iface]   remove everything this script created
#   4xui-shaper.sh status [iface]     show installed classes and counters
#   4xui-shaper.sh check              report whether the host can support it
#
# SAFETY
#   * A default class at full link speed carries all unclassified traffic, so
#     SSH and the panel itself are never shaped.
#   * Every apply verifies connectivity afterwards and rolls back automatically
#     if the default gateway stops answering.
#   * rollback is always safe to run, including when nothing is installed.
#
# KNOWN INTERACTION
#   Installing a root qdisc replaces whatever `net.core.default_qdisc` put there
#   (fq under BBR). fq_codel is attached as the leaf qdisc of every class to keep
#   queue management sane. `rollback` deletes the root qdisc, after which the
#   kernel restores the configured default.

set -uo pipefail

IFB_DEV="ifb-4xui"
DEFAULT_CLASSID=9999
XRAY_TABLE="fourxui_xray"
SSH_TABLE="fourxui_ssh"
STATE_DIR="${XUI_SHAPER_STATE_DIR:-/run/4xui-shaper}"
STATE_SIGNATURE="${STATE_DIR}/active.signature"

red='\033[0;31m'
green='\033[0;32m'
yellow='\033[0;33m'
plain='\033[0m'

info() { echo -e "${green}[ok]${plain}   $*"; }
warn() { echo -e "${yellow}[warn]${plain} $*"; }
err() { echo -e "${red}[err]${plain}  $*" >&2; }

require_root() {
    if [[ "${EUID}" -ne 0 ]]; then
        err "must run as root"
        exit 1
    fi
}

# ---------------------------------------------------------------- capability

detect_iface() {
    local dev
    dev=$(ip route show default 2>/dev/null | awk '/default/ {for(i=1;i<=NF;i++) if($i=="dev") {print $(i+1); exit}}')
    if [[ -z "${dev}" ]]; then
        return 1
    fi
    echo "${dev}"
}

default_gateway() {
    ip route show default 2>/dev/null |
        awk '/default/ {for(i=1;i<=NF;i++) if($i=="via") {print $(i+1); exit}}'
}

# link_bits returns a ceiling for the root class. Virtual NICs often report no
# speed at all, in which case a deliberately high ceiling means "do not cap".
link_bits() {
    local iface="$1" mbit=""
    if [[ -r "/sys/class/net/${iface}/speed" ]]; then
        mbit=$(cat "/sys/class/net/${iface}/speed" 2>/dev/null)
    fi
    if [[ ! "${mbit}" =~ ^[0-9]+$ ]] || [[ "${mbit}" -le 0 ]]; then
        mbit=10000
    fi
    echo $((mbit * 1000 * 1000))
}

module_ok() {
    local m="$1"
    [[ -d "/sys/module/${m}" ]] && return 0
    modprobe "${m}" >/dev/null 2>&1 && return 0
    return 1
}

do_check() {
    local ok=0 iface
    for c in tc ip nft; do
        if command -v "${c}" >/dev/null 2>&1; then
            echo "  present     ${c}"
        else
            echo "  MISSING     ${c}"
            ok=1
        fi
    done
    for m in sch_htb sch_fq_codel sch_ingress act_mirred act_connmark cls_fw ifb; do
        if module_ok "${m}"; then
            echo "  loadable    ${m}"
        else
            echo "  MISSING     ${m}"
            ok=1
        fi
    done
    if iface=$(detect_iface); then
        echo "  wan iface   ${iface}"
    else
        echo "  MISSING     default route / WAN interface"
        ok=1
    fi
    if [[ "${ok}" -eq 0 ]]; then
        info "this host can run the queue-based shaper"
    else
        err "requirements missing; shaping would not work here"
    fi
    return "${ok}"
}

# ------------------------------------------------------------------ nft parse

# read_limits emits "mark up_bits down_bits" lines, parsed from the panel's own
# nftables tables. Chain context decides direction: the output chain carries
# upload rules, prerouting carries download.
read_limits_from_nft() {
    local table
    for table in "${XRAY_TABLE}" "${SSH_TABLE}"; do
        nft list table inet "${table}" 2>/dev/null
    done | awk '
        /chain[ \t]+output/     { chain="up";   next }
        /chain[ \t]+prerouting/ { chain="down"; next }
        /meta mark [0-9]+ limit rate over [0-9]+ bytes\/second/ {
            mark=""; rate=""
            for (i=1;i<=NF;i++) {
                if ($i=="mark" && $(i-1)=="meta") mark=$(i+1)
                if ($i=="over") rate=$(i+1)
            }
            if (mark!="" && rate!="") {
                bits=rate*8
                if (chain=="up")   up[mark]=bits
                if (chain=="down") down[mark]=bits
                marks[mark]=1
            }
        }
        END {
            for (m in marks) printf "%s %d %d\n", m, (m in up?up[m]:0), (m in down?down[m]:0)
        }
    ' | sort -n
}

read_limits() {
    local file
    local -a state_files=()
    for file in "${STATE_DIR}/xray.limits" "${STATE_DIR}/ssh.limits"; do
        if [[ -f "${file}" ]]; then
            state_files+=("${file}")
        fi
    done
    if [[ "${#state_files[@]}" -eq 0 ]]; then
        read_limits_from_nft
        return
    fi
    cat "${state_files[@]}" | awk '
            NF == 3 && $1 ~ /^[0-9]+$/ {
                mark=$1; up[mark]+=$2; down[mark]+=$3; seen[mark]=1
            }
            END {
                for (mark in seen) printf "%s %d %d\n", mark, up[mark], down[mark]
            }
        ' | sort -n
}

rate_state_complete() {
    [[ -f "${STATE_DIR}/xray.limits" && -f "${STATE_DIR}/ssh.limits" ]]
}

limits_signature() {
    local iface="$1" limits="$2"
    if command -v sha256sum >/dev/null 2>&1; then
        printf '%s\n%s\n' "${iface}" "${limits}" | sha256sum | awk '{print $1}'
    else
        printf '%s\n%s\n' "${iface}" "${limits}" | cksum | awk '{print $1":"$2}'
    fi
}

shaping_healthy() {
    local iface="$1"
    ip link show "${IFB_DEV}" >/dev/null 2>&1 || return 1
    tc qdisc show dev "${iface}" root 2>/dev/null | grep -Eq '^qdisc htb 1:' || return 1
    tc qdisc show dev "${iface}" ingress 2>/dev/null | grep -Eq '^qdisc ingress ffff:' || return 1
    tc qdisc show dev "${IFB_DEV}" root 2>/dev/null | grep -Eq '^qdisc htb 1:' || return 1
}

# Remove all policer verdict rules in one atomic nft transaction. Marking and
# named counters stay installed, so traffic accounting continues unchanged.
disable_policers() {
    local table chain handle commands=""
    for table in "${XRAY_TABLE}" "${SSH_TABLE}"; do
        for chain in output prerouting; do
            while read -r handle; do
                [[ -z "${handle}" ]] && continue
                commands+="delete rule inet ${table} ${chain} handle ${handle}"$'\n'
            done < <(nft -a list chain inet "${table}" "${chain}" 2>/dev/null |
                awk '/limit rate over/ && / drop/ && /# handle [0-9]+/ {print $NF}')
        done
    done
    [[ -z "${commands}" ]] && return 0
    printf '%s' "${commands}" | nft -f -
}

fallback_burst_bytes() {
    local rate_bytes="$1" burst
    burst=$((rate_bytes * 2))
    if [[ "${burst}" -lt 1048576 ]]; then
        burst=1048576
    elif [[ "${burst}" -gt 33554432 ]]; then
        burst=33554432
    fi
    echo "${burst}"
}

# Restore policer verdicts from the complete desired-rate state. This is used
# when queueing is rolled back or cannot be installed, so a previous successful
# shaper run can never leave another subsystem silently unlimited.
restore_policers() {
    local table chain handle commands="" file mark up down rate burst

    # Remove any existing policers first; delete + add is one nft transaction,
    # so failure leaves the previous ruleset intact rather than half-updated.
    for table in "${XRAY_TABLE}" "${SSH_TABLE}"; do
        for chain in output prerouting; do
            while read -r handle; do
                [[ -z "${handle}" ]] && continue
                commands+="delete rule inet ${table} ${chain} handle ${handle}"$'\n'
            done < <(nft -a list chain inet "${table}" "${chain}" 2>/dev/null |
                awk '/limit rate over/ && / drop/ && /# handle [0-9]+/ {print $NF}')
        done
    done

    for table in "${XRAY_TABLE}" "${SSH_TABLE}"; do
        if [[ "${table}" == "${XRAY_TABLE}" ]]; then
            file="${STATE_DIR}/xray.limits"
        else
            file="${STATE_DIR}/ssh.limits"
        fi
        [[ -f "${file}" ]] || continue
        nft list chain inet "${table}" output >/dev/null 2>&1 || continue
        nft list chain inet "${table}" prerouting >/dev/null 2>&1 || continue
        while read -r mark up down; do
            [[ "${mark}" =~ ^[0-9]+$ ]] || continue
            if [[ "${up}" -gt 0 ]] 2>/dev/null; then
                rate=$((up / 8))
                burst=$(fallback_burst_bytes "${rate}")
                commands+="add rule inet ${table} output meta mark ${mark} limit rate over ${rate} bytes/second burst ${burst} bytes drop"$'\n'
            fi
            if [[ "${down}" -gt 0 ]] 2>/dev/null; then
                rate=$((down / 8))
                burst=$(fallback_burst_bytes "${rate}")
                commands+="add rule inet ${table} prerouting meta mark ${mark} limit rate over ${rate} bytes/second burst ${burst} bytes drop"$'\n'
            fi
        done <"${file}"
    done

    [[ -z "${commands}" ]] && return 0
    printf '%s' "${commands}" | nft -f -
}

# ------------------------------------------------------------------- tc setup

# build_htb installs a root HTB with an unlimited default class on the given
# device, then one class per mark. Reads "mark up down" lines on stdin and uses
# the column named by $2 ("up" or "down").
build_htb() {
    local dev="$1" direction="$2" ceil_bits="$3"
    local cid=1 mark up down rate

    tc qdisc del dev "${dev}" root 2>/dev/null || true
    tc qdisc add dev "${dev}" root handle 1: htb "default" "${DEFAULT_CLASSID}" r2q 10 || return 1
    tc class add dev "${dev}" parent 1: classid 1:1 htb rate "${ceil_bits}bit" ceil "${ceil_bits}bit" || return 1
    # Unclassified traffic - SSH, the panel, unlimited clients - lands here at
    # full link speed. Without this class a misconfiguration could stall the box.
    tc class add dev "${dev}" parent 1:1 classid "1:${DEFAULT_CLASSID}" \
        htb rate "${ceil_bits}bit" ceil "${ceil_bits}bit" || return 1
    tc qdisc add dev "${dev}" parent "1:${DEFAULT_CLASSID}" handle "${DEFAULT_CLASSID}:" fq_codel || true

    while read -r mark up down; do
        [[ -z "${mark}" ]] && continue
        if [[ "${direction}" == "up" ]]; then
            rate="${up}"
        else
            rate="${down}"
        fi
        [[ "${rate}" -le 0 ]] 2>/dev/null && continue

        cid=$((cid + 1))
        if [[ "${cid}" -ge "${DEFAULT_CLASSID}" ]]; then
            warn "too many limited clients for one qdisc; stopping at ${cid}"
            break
        fi
        tc class add dev "${dev}" parent 1:1 classid "1:${cid}" \
            htb rate "${rate}bit" ceil "${rate}bit" || return 1
        tc qdisc add dev "${dev}" parent "1:${cid}" handle "${cid}:" fq_codel || true
        tc filter add dev "${dev}" parent 1: protocol all prio 1 \
            handle "${mark}" fw flowid "1:${cid}" || return 1
    done
    return 0
}

setup_ingress() {
    local iface="$1"
    ip link show "${IFB_DEV}" >/dev/null 2>&1 || ip link add "${IFB_DEV}" type ifb || return 1
    ip link set "${IFB_DEV}" up || return 1
    tc qdisc del dev "${iface}" ingress 2>/dev/null || true
    tc qdisc add dev "${iface}" handle ffff: ingress || return 1
    # `action connmark` copies the conntrack mark into skb->mark. The tc ingress
    # hook runs before netfilter prerouting, so without this the fw filter on the
    # IFB device would see no mark at all and nothing would be classified.
    tc filter add dev "${iface}" parent ffff: protocol all prio 1 u32 match u32 0 0 \
        action connmark action mirred egress redirect dev "${IFB_DEV}" || return 1
    return 0
}

# ------------------------------------------------------------------- commands

do_rollback() {
    require_root
    local iface="${1:-}"
    if [[ -z "${iface}" ]]; then
        iface=$(detect_iface) || iface=""
    fi
    if [[ -n "${iface}" ]]; then
        tc qdisc del dev "${iface}" root 2>/dev/null || true
        tc qdisc del dev "${iface}" ingress 2>/dev/null || true
    fi
    tc qdisc del dev "${IFB_DEV}" root 2>/dev/null || true
    ip link set "${IFB_DEV}" down 2>/dev/null || true
    ip link del "${IFB_DEV}" 2>/dev/null || true
    if [[ -f "${STATE_SIGNATURE}" ]]; then
        rm -f "${STATE_SIGNATURE}"
    fi
    if rate_state_complete && ! restore_policers; then
        warn "could not restore nftables fallback policers"
    fi
    info "shaping removed${iface:+ from ${iface}}; kernel default qdisc restored"
    return 0
}

connectivity_ok() {
    local gw
    gw=$(default_gateway)
    if [[ -z "${gw}" ]]; then
        # Nothing to probe against; do not claim failure and roll back a working
        # setup on that basis.
        return 0
    fi
    ping -c 2 -W 2 "${gw}" >/dev/null 2>&1
}

do_apply() {
    require_root
    local iface="${1:-}"
    if [[ -z "${iface}" ]]; then
        iface=$(detect_iface) || {
            err "no default route; pass the interface explicitly"
            exit 1
        }
    fi
    if ! ip link show "${iface}" >/dev/null 2>&1; then
        err "interface ${iface} does not exist"
        exit 1
    fi
    if ! do_check >/dev/null 2>&1; then
        err "host requirements not met; run: 4xui-shaper.sh check"
        exit 1
    fi

    local limits
    limits=$(read_limits)
    if [[ -z "${limits}" ]]; then
        warn "no rate rules found in nftables (${XRAY_TABLE} / ${SSH_TABLE})"
        warn "nothing to shape - enable a speed limit in the panel first"
        do_rollback "${iface}" >/dev/null
        return 0
    fi

    local ceil_bits
    ceil_bits=$(link_bits "${iface}")

    mkdir -p "${STATE_DIR}"
    local signature current_signature=""
    signature=$(limits_signature "${iface}" "${limits}")
    if [[ -r "${STATE_SIGNATURE}" ]]; then
        current_signature=$(cat "${STATE_SIGNATURE}" 2>/dev/null)
    fi
    if [[ "${signature}" == "${current_signature}" ]] && shaping_healthy "${iface}"; then
        if rate_state_complete && ! disable_policers; then
            err "queue is healthy but nftables policer removal failed"
            return 1
        fi
        info "shaping policy unchanged and healthy; live qdisc preserved"
        return 0
    fi

    echo "Installing shaping on ${iface} (ceiling ${ceil_bits} bit/s)"
    echo "${limits}" | sed 's/^/  mark rate: /'

    if ! echo "${limits}" | build_htb "${iface}" up "${ceil_bits}"; then
        err "egress shaping failed; rolling back"
        do_rollback "${iface}"
        exit 1
    fi

    if ! setup_ingress "${iface}"; then
        err "ingress redirect failed; rolling back"
        do_rollback "${iface}"
        exit 1
    fi
    if ! echo "${limits}" | build_htb "${IFB_DEV}" down "${ceil_bits}"; then
        err "ingress shaping failed; rolling back"
        do_rollback "${iface}"
        exit 1
    fi

    if ! connectivity_ok; then
        err "gateway stopped responding after applying shaping; rolling back"
        do_rollback "${iface}"
        exit 1
    fi

    printf '%s\n' "${signature}" >"${STATE_SIGNATURE}"
    chmod 600 "${STATE_SIGNATURE}"
    if rate_state_complete && ! disable_policers; then
        err "could not atomically disable nftables policers; rolling back to policer-only mode"
        do_rollback "${iface}"
        return 1
    fi

    info "shaping active on ${iface} (download via ${IFB_DEV})"
    echo
    echo "The nftables drop policer is disabled while this queue is healthy."
    echo "Marking and traffic counters remain active. To measure:"
    echo "  iperf3 -c <host> -t 30        TCP"
    echo "  iperf3 -c <host> -u -b 100M   UDP"
    echo "Remove with: 4xui-shaper.sh rollback"
    return 0
}

do_status() {
    local iface="${1:-}"
    if [[ -z "${iface}" ]]; then
        iface=$(detect_iface) || iface=""
    fi
    if [[ -z "${iface}" ]]; then
        err "no interface detected"
        return 1
    fi
    echo "=== ${iface} egress (upload) ==="
    tc -s qdisc show dev "${iface}" 2>/dev/null
    tc -s class show dev "${iface}" 2>/dev/null
    tc filter show dev "${iface}" 2>/dev/null
    echo
    echo "=== ${iface} ingress hook ==="
    tc -s qdisc show dev "${iface}" ingress 2>/dev/null
    tc filter show dev "${iface}" parent ffff: 2>/dev/null
    echo
    if ip link show "${IFB_DEV}" >/dev/null 2>&1; then
        echo "=== ${IFB_DEV} (download) ==="
        tc -s qdisc show dev "${IFB_DEV}" 2>/dev/null
        tc -s class show dev "${IFB_DEV}" 2>/dev/null
        tc filter show dev "${IFB_DEV}" 2>/dev/null
    else
        warn "${IFB_DEV} not present - download shaping is not installed"
    fi
    echo
    echo "=== desired marks and rates ==="
    read_limits | sed 's/^/  /' || true
    return 0
}

case "${1:-}" in
    apply) do_apply "${2:-}" ;;
    rollback) do_rollback "${2:-}" ;;
    status) do_status "${2:-}" ;;
    check) do_check ;;
    *)
        echo "usage: $0 {apply|rollback|status|check} [interface]"
        exit 1
        ;;
esac
