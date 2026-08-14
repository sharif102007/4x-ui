# 4x-ui v1.1.6

## Ping and latency

- Changed download shaping so only clients with an actual download speed limit are redirected through `ifb-4xui`; unlimited/game traffic stays on the normal WAN ingress path.
- A download-only limit no longer replaces the WAN root qdisc. Upload HTB is installed only when an upload limit exists.
- Increased the bounded nftables fallback burst allowance to reduce short UDP/game burst drops while HTB remains the primary rate shaper.
- Reduced SSH Manager counter polling from every 5 seconds to every 15 seconds to lower periodic process/DB activity on small VPS plans.
- Enabled `TCP_NODELAY` on both sides of generated stunnel services and explicitly on both sockets of the Go payload gateway for lower interactive SSH latency.

## SSH UDP relay

- Fixed first-time UDP relay setup using a dedicated long-running installer path instead of the general 30-second SSH command timeout.
- UDPGW now prefers a persistent `xui-udpgw@<port>.service` systemd unit with automatic restart and startup at boot.
- If a distro package is unavailable, 4x-ui installs build dependencies and builds only `badvpn-udpgw` automatically.
- Multiple SSH inbounds may safely share the default UDPGW port (7300); one listener is created per unique relay port instead of one process per inbound.
- Increased UDPGW per-client destination capacity from 10 to 64 connections for games, voice traffic and DNS-heavy clients.
- UDP relay startup is health-checked by confirming the loopback TCP listener. Setup errors now fail reconciliation so Add/Edit/Enable rolls back instead of showing a non-working relay as active.
- Added relay-port conflict validation against SSH public/backend/gateway, Xray, panel and subscription ports.
- Diagnostics now correctly inspect the UDPGW TCP listener and report 4x-ui systemd UDPGW units.
- Uninstall stops/removes only UDPGW units recorded as owned by 4x-ui.

## VPS auto setup

When UDP Relay is enabled on an SSH inbound, no separate manual VPS UDPGW install is required. 4x-ui automatically:

1. detects an existing `badvpn-udpgw`;
2. tries the distro package where available;
3. otherwise installs build dependencies and builds UDPGW;
4. creates/enables the persistent `xui-udpgw@<port>.service`; and
5. verifies that `127.0.0.1:<port>` is listening before the inbound is accepted as working.

The systemd-managed relay survives panel restarts and upgrades. Non-systemd hosts use a supervised in-process fallback.

## Existing v1.1.5 fixes preserved

- Ubuntu/OpenSSH `Match LocalPort` / `sshd-socket-generator` compatibility fix remains intact.
- SSH inbound Add/Edit/Delete/Enable/Disable rollback behavior remains intact.

## Release

- Version bumped to `1.1.6`.
- Existing GitHub Actions binary and Docker release workflows remain unchanged.
- No `release-from-vps.sh` helper is included.
