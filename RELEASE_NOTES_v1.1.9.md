# 4x-ui v1.1.9 — Latest Release and Complete Project Details

This is the single current release note for the repository. Older per-version
release-note files were consolidated here so the source tree keeps only the
latest maintained project documentation.

## Project identity

- Project: **4x-ui**
- Current version: **1.1.9**
- Base: forked and extended from 3x-ui 2.9.4
- Go module: `github.com/sharif102007/4x-ui/v2`
- Panel command remains `x-ui`
- Release Xray core: **v26.4.25**
- Go toolchain declared by the repository: **1.26.2**

### Build hotfix

- The Xray Payload Bypass runtime now uses the dedicated `xrayPayloadGateway`
  type name so it cannot collide with the pre-existing SSH `payloadGateway`
  type in the shared `web/service` Go package.


## v1.1.9 latest changes

### Final cleanup

- Removed the experimental Xray Payload Bypass Server Message/header feature.
  It was not reliably displayed by client applications and is no longer part
  of the UI, database model, gateway config, tests, or runtime path.
- Native Payload Bypass itself remains unchanged and supported.

### More Information click regression fix

- Removed the `@click.stop` modifier from the Ant Design menu item. On a component
  event this could intercept the menu event before `showInfo()` completed.
- `showInfo()` no longer awaits `defaultSettings`; the modal opens immediately
  from the already loaded inbound/client data. Subscription settings continue to
  retry independently for Add/Edit Client.

### Client Edit / Subscription race fix

- The Inbounds page now loads default/subscription settings before the initial
  inbound list becomes interactive. Previously both requests ran concurrently;
  a fast client Edit click could observe the default `subSettings.enable=false`
  and hide the Subscription field even though the subscription server was enabled.
- Add Client, Bulk Add and Edit Client now call a shared `ensureDefaultSettings()`
  guard. If the initial settings request failed, those actions retry it instead of
  permanently using the false defaults. More Information opens immediately and is
  intentionally independent of that settings request.

### More Information refresh fix

- Restored the normal Ant Design menu click path so the More Information action
  reaches `showInfo()` reliably on the first click.
- Fallback polling, invalidate-driven refreshes, and direct WebSocket full inbound
  snapshots are suppressed while an inbound/client/info modal is open. WebSocket
  traffic deltas still update counters in place, so the selected client object is
  not replaced underneath an open More Information modal.

## SSH Manager

4x-ui adds an integrated SSH Manager alongside the Xray panel.

### SSH users

- Create, edit, enable/disable and delete Linux SSH users from the panel.
- Passwords are applied to the host account; the panel stores only its encrypted
  representation required for managed client strings.
- Expiry date support with automatic account lock behavior.
- Total traffic quota and real-time usage accounting.
- Traffic reset schedules: `never`, `daily`, `weekly`, and `monthly`.
- Independent download and upload speed limits in Mbps.
- Manual traffic reset from the panel.
- Managed-account cleanup is included in uninstall handling.

### SSH inbound modes

The panel supports four managed SSH inbound modes:

1. `normal_ssh` — direct OpenSSH listener.
2. `ssh_payload_only` — payload gateway without TLS.
3. `ssh_tls_sni` — TLS/SNI through stunnel to OpenSSH.
4. `ssh_tls_payload` — TLS/SNI through stunnel and the payload gateway to
   OpenSSH.

Managed inbounds support public/listen port, backend SSH port, automatic payload
Gateway port where needed, certificate selection, enable/disable state, notes,
and optional per-inbound server banner/message behavior.

## SSH inbound reliability

- Ubuntu/OpenSSH `sshd-socket-generator` compatibility is handled for the known
  `Match LocalPort` / missing `lport` effective-config failure.
- When that distro generator is incompatible, 4x-ui switches to its owned
  classic `ssh.service` compatibility path without removing the server-message
  feature.
- Add, Edit, Delete, Enable and Disable are rollback-protected. A failed host
  reconciliation restores the previous database/runtime state instead of
  leaving panel and VPS state inconsistent.
- OpenSSH configuration is validated before a changed configuration is applied.
- v1.1.9 adds no-op detection: an unchanged healthy OpenSSH configuration is
  reused instead of being rewritten, revalidated and restarted on every Save.
- Generated stunnel configuration/systemd state also uses a no-op fast path when
  unchanged and healthy.

## SSH UDP Relay / BadVPN UDPGW

4x-ui integrates UDPGW for SSH clients that use UDP-over-SSH support such as
DarkTunnel / HTTP Custom compatible flows.

- UDP Relay can be enabled per SSH inbound.
- Default relay port is 7300; custom relay ports are supported.
- Multiple SSH inbounds may share one unique UDPGW listener/port safely.
- UDPGW is managed as persistent `xui-udpgw@<port>.service` units on systemd
  hosts with automatic restart and startup at boot.
- Non-systemd hosts retain a supervised fallback path.
- Relay startup is accepted only after the loopback TCP listener is healthy.
- Per-client destination capacity is configured to 64 for games, voice and
  DNS-heavy clients.
- Port-conflict validation covers managed SSH ports, Xray ports, panel ports and
  subscription ports.
- Diagnostics inspect the UDPGW TCP listener and 4x-ui-owned systemd units.
- Uninstall removes only relay services owned by 4x-ui.

### Automatic VPS UDP Relay setup

The operator does not need to manually install BadVPN for normal supported
Debian/Ubuntu deployments. On first enable, 4x-ui:

1. detects an existing `badvpn-udpgw` binary;
2. safely waits for active apt/dpkg package operations;
3. runs `dpkg --configure -a` automatically to recover an interrupted dpkg
   transaction;
4. runs `apt-get -f install -y` to repair broken package dependencies;
5. tries the distribution BadVPN package when available;
6. otherwise installs build dependencies and builds `badvpn-udpgw`;
7. creates/enables the persistent `xui-udpgw@<port>.service`; and
8. verifies `127.0.0.1:<port>` before accepting the inbound as working.

Package-manager lock files are never force-deleted and an active package manager
is never killed. Once UDPGW is installed and healthy, normal SSH inbound Saves
do not repeat apt/dpkg installation work.

### Fast Save path in v1.1.9

- A healthy 4x-ui-owned UDPGW listener is reused immediately.
- Repeated `systemctl enable/start` and the previous startup wait are skipped on
  unchanged healthy inbounds.
- An active unit with a failed listener is restarted immediately rather than
  waiting through the full pre-restart health timeout.

## Xray WebSocket Payload Bypass

4x-ui v1.1.9 adds a native Go payload gateway directly to Xray WebSocket
inbounds. In the inbound form, **Payload Bypass** appears immediately below
**Proxy Protocol** when Transmission is WebSocket.

- The configured inbound port remains the public client port; no extra public
  port field is required.
- When Payload Bypass is enabled, 4x-ui automatically allocates a hidden
  loopback backend port and makes Xray listen only on `127.0.0.1:<backend>`.
- The 4x-ui Go process owns the original public port and performs the payload
  handshake before relaying the connection to Xray.
- The gateway follows the demonstrated Asyncio proxy behavior: it reads the
  first HTTP header block with a five-second handshake timeout, replies with
  `HTTP/1.1 101 Switching Protocols`, discards that injected header, preserves
  any bytes already received after the first `CRLFCRLF`, and then relays TCP
  bidirectionally to Xray.
- Native TCP connections use TCP_NODELAY and keepalive; no Python/asyncio
  package, external script, or separate systemd proxy service is required.
- Genuine RFC6455 WebSocket handshakes containing `Sec-WebSocket-Key` are
  passed through unchanged, so ordinary WebSocket clients can still connect
  while the switch is enabled. TLS ClientHello records are also passed through
  unchanged, preserving normal WSS/TLS clients on the same public port.
- Payload Bypass is accepted only for WebSocket transmission. Changing the
  transmission away from WebSocket automatically turns the UI switch off.
- WebSocket Proxy Protocol is mutually exclusive with Payload Bypass; enabling
  Payload Bypass turns Proxy Protocol off and the backend validates the same rule.
- Public ports and hidden backend ports are conflict-checked. Imported/cloned
  configurations never reuse a hidden backend port from another VPS.
- Add, edit, enable, disable, delete, Xray restart, and panel restart all
  reconcile the native gateway lifecycle automatically.

Runtime path:

```text
Client / DarkTunnel / HTTP Custom
        ↓
Public inbound port (native 4x-ui Go gateway)
        ↓
Synthetic payload handshake / direct-WS passthrough
        ↓
127.0.0.1:<automatic hidden backend port>
        ↓
Xray WebSocket inbound
```

## Xray per-client bandwidth limits

4x-ui extends supported multi-user Xray client records with:

- `speedLimit`
- `downloadMbps`
- `uploadMbps`

For speed-limited clients, 4x-ui creates marked direct/freedom egress paths and
installs matching nftables/tc policy. The implementation preserves the Xray API
routing rule and block rules ahead of speed-limit routing, and preserves the
existing routing order when no client speed limit is active.

The normal Xray statistics/database remains responsible for traffic quota,
expiry, reset and automatic disable operations.

## Kernel shaping and ping/latency behavior

4x-ui owns only its dedicated nftables tables:

- `inet fourxui_xray`
- `inet fourxui_ssh`

When supported by the host kernel, rate enforcement uses HTB/IFB and
`act_connmark`, with `fq_codel` on limited classes.

Latency-related behavior in the current release:

- Only clients with an actual **download** speed limit are redirected through
  `ifb-4xui`; unlimited/game traffic stays on the normal WAN ingress path.
- A download-only limit does not replace the WAN root qdisc.
- Upload HTB is installed only when an upload limit exists.
- The nftables fallback policer has a larger bounded burst allowance to reduce
  avoidable short UDP/game burst drops.
- SSH counter collection was reduced from every 5 seconds to every 15 seconds
  to lower periodic helper/DB activity on smaller VPS plans.
- Generated stunnel services enable `TCP_NODELAY` on both sides.
- The Go payload gateway explicitly enables `TCP_NODELAY` on both TCP sockets.

If full queue shaping is not available, nftables remains the fallback rate
policer. A policer enforces limits by dropping excess packets, so kernel HTB/IFB
support is preferred for smooth UDP/QUIC behavior.

## SSH traffic and speed enforcement

- Managed SSH users are identified by Linux UID-based policy.
- nftables counters account upload/download usage.
- One table read collects managed-user counters per poll rather than launching a
  separate nft process per user.
- Failed counter reads do not reset the accounting baseline and therefore avoid
  double-counting on the next successful poll.
- Quota exhaustion or expiry locks the managed Linux account.
- Separate upload/download rate limits are supported.

## Database and runtime reliability

- SQLite WAL mode is enabled.
- A busy timeout and bounded connection pool reduce contention between periodic
  Xray/SSH writers.
- Traffic-write and transaction-commit failures are propagated rather than
  being logged as successful writes.
- nftables helper reads are bounded so shutdown is not left waiting forever.
- The panel applies a soft Go memory limit based on the container/host memory
  limit; `XUI_MEMORY_LIMIT` can override it.

## Network optimization

Optional tuning remains explicit:

```bash
x-ui netopt apply
x-ui netopt status
x-ui netopt rollback
```

- Only sysctl keys present on the running kernel are written.
- Buffer ceilings scale with available RAM.
- BBR is selected only when available.
- Original sysctl values are backed up for rollback.
- The invalid `net.ipv4.tcp_nodelay` sysctl attempt and the old automatic
  128 MiB TCP-buffer tuning were removed. TCP_NODELAY is applied at the relevant
  application socket level instead.

## Diagnostics

```bash
x-ui diag
x-ui diag counters
x-ui shaper check
x-ui shaper status
```

These commands help verify kernel marking, counters and shaping; a panel toggle
alone is not treated as proof that host enforcement is active.

## Installer, update and uninstall

- `install.sh` and `update.sh` are the supported deployment/update scripts.
- Required SSH Manager runtime dependencies include stunnel and the networking
  tools used for nftables/tc enforcement.
- Network/download operations use bounded retries/timeouts where applicable.
- The panel is stopped during migrations/settings changes and restarted after
  update configuration/certificate checks complete.
- Managed SSH accounts and 4x-ui-owned SSH/UDPGW/network state are cleaned up by
  uninstall logic without blindly deleting unrelated host state.
- No `release-from-vps.sh` helper is included.

## Panel and theme

- Project branding/module paths are 4x-ui-specific while the command remains
  `x-ui` for deployment compatibility.
- Current theme behavior follows the stock Light, Dark and Ultra Dark modes.
- The earlier custom AMOLED/Obsidian and Eye Comfort overrides are not part of
  the current source.

## GitHub Actions and releases

The source root contains the files required by Actions (`go.mod`, `Dockerfile`,
`main.go` and `.github/workflows`).

- `release.yml`: formatting, `go vet`, staticcheck and race tests gate the binary
  release, followed by cross-platform packaging.
- Linux targets include amd64, arm64, armv7, armv6, 386, armv5 and s390x.
- The release workflow packages Xray-core v26.4.25 and runtime scripts.
- `docker.yml`: tag pushes build/push multi-architecture images to
  `ghcr.io/sharif102007/4x-ui`.
- `codeql.yml`: repository security analysis.

Pushing a semantic tag such as `v1.1.9` triggers the binary and Docker release
flows according to the workflow definitions.

## Repository cleanup in this package

- Only `RELEASE_NOTES_v1.1.9.md` is kept.
- All historical per-version release-note files were consolidated into this document and removed.
- README links now point only to the current release note.
- No stale QA/Copilot/Dependabot helper files, VPS release helper, shell history,
  ACME home directory or temporary work directories are included.
- Current generic maintained docs such as `BANDWIDTH-LIMITS.md`,
  `BUILD_AND_DEPLOY.md` and `CONTRIBUTING.md` remain because they are not old
  version copies and are part of the maintained repository documentation.

## Current version

`1.1.9`


## UI reliability

- Fixed the intermittent client Subscription field by serializing/retrying default-settings loading before Add/Edit/Info modals; a failed settings request no longer silently opens a modal with `subSettings.enable=false`.
- Fixed the remaining More Information refresh race: polling, invalidate-driven REST refreshes, and direct WebSocket `inbounds` full snapshots are all prevented from replacing the inbound/client object graph while edit/info modals are open.
