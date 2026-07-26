# Per-user traffic and speed limits

4x-ui applies Linux nftables policies for SSH users and Xray clients. The
installer adds `nftables` and `iproute2` on supported Linux distributions.

## SSH Manager

- Linux UID rules mark each managed SSH user's tunneled connections.
- Named nftables counters record upload and download bytes every five seconds.
- Total-flow exhaustion locks the Linux account automatically.
- Upload and download rates are enforced independently.

## Xray inbounds

Clients with an email can enable separate upload and download limits in the
client editor. 4x-ui clones the direct (`freedom`) outbound, adds a socket mark
and a user routing rule at runtime, then enforces the rates with nftables. This
covers the multi-user VLESS, VMess, Trojan, Shadowsocks, and Hysteria client
models.

The existing Xray statistics database remains the source of truth for traffic
quota, reset, expiry, and automatic client disable operations.

### Routing precedence

A per-client speed limit works by sending that client's traffic through a
specially marked outbound. Because a connection can only leave through one
outbound, this interacts with custom routing rules. The order 4x-ui installs is:

1. Rules targeting a `blackhole` outbound (or a tag named `block`/`blocked`)
2. Per-client speed rules
3. All other rules

So block rules are still enforced for rate-limited clients. However, **a
rate-limited client does not follow custom rules that route specific traffic to
a different proxy outbound** - the marked outbound is what carries the shaping
mark, so it wins. If you need chained or selective proxying for a client, do not
put a speed limit on that client.

## Enforcement mechanism and its limits

Rates are currently enforced with an nftables rate limiter
(`limit rate over ... drop`). This is a **policer**, not a shaper: it drops
packets that exceed the configured rate rather than queueing them. Consequences
you should expect, and which no amount of configuration removes:

- **TCP throughput lands below the configured rate.** Drops trigger congestion
  control and retransmission, so a 10 Mbps limit typically measures noticeably
  lower, and the gap widens with round-trip time.
- **UDP, QUIC and Hysteria see real packet loss** rather than added latency.
  Protocols with their own congestion control will react, but loss-sensitive
  application traffic inside the tunnel degrades more than it would under a
  queueing shaper.
- **Download-direction limiting is approximate.** Inbound packets are evaluated
  in the `prerouting` hook, which is after they have already crossed the
  server's uplink. Dropping them signals the sender to slow down; it does not
  reclaim bandwidth already consumed.

A queue-based shaper (`tc` HTB or TBF on egress, plus an `ifb` device with
`act_connmark` for the ingress direction) would give accurate rates in both
directions. That is **not implemented yet**. Treat the current limits as a coarse
cap, not a precise guarantee.

## Verifying enforcement

Do not trust the UI switch alone - check kernel state:

```bash
x-ui diag              # nftables tables, byte counters, tc state, modules
x-ui diag counters     # byte counters only, safe to poll
```

Interpretation:

- A table reported as absent means nothing is being enforced.
- Counters stuck at 0 while a client transfers data mean the packet mark is not
  reaching the chain: the rule exists but never matches.
- Counters rising while throughput is unlimited mean marking works but the rate
  rule is not effective.

Measure real throughput separately with `iperf3` from a second host.

If a policy cannot be installed, the panel now logs at **error** level and
records the failure, instead of leaving a limit switch that looks enabled while
nothing is enforced.

## Network tuning

```bash
x-ui netopt apply      # apply socket buffer / TCP / UDP / conntrack tuning
x-ui netopt status     # show current values of every managed key
x-ui netopt rollback   # restore the values saved on first apply
```

Only sysctl keys the running kernel actually exposes are written, so an
unsupported parameter is skipped rather than causing a boot-time error. Buffer
ceilings scale with total RAM (16MB on a sub-1GB box, up to 128MB at 4GB+).
BBR is selected only when the kernel offers it, and `tcp_tw_reuse` only when TCP
timestamps are enabled. `TCP_NODELAY` is a per-socket option with no sysctl
equivalent; it is set by the panel and Xray on their own sockets.

Original values are saved to `/etc/x-ui/network-sysctl-backup.conf` on the first
apply, and `x-ui uninstall` rolls them back before removing panel files.

## Runtime requirements

- The panel service must run as root or with permission to manage nftables.
- Docker deployments require host networking plus `NET_ADMIN` and `NET_RAW`.
- Existing custom rules are not flushed: 4x-ui owns only the
  `inet fourxui_ssh` and `inet fourxui_xray` tables.
