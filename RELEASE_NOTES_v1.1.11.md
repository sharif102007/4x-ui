# 4x-ui v1.1.11

## Current release

- Base: 3x-ui v2.9.4
- Project: 4x-ui
- Go module: `github.com/sharif102007/4x-ui/v2`
- Panel command: `x-ui`
- Xray core packaged by release workflow: v26.4.25
- Go toolchain declared by the project: 1.26.2

## English-only panel and More Information fix

4x-ui is now permanently English-only (`en-US`). The panel no longer detects or switches language from browser locale, `Accept-Language`, or a language cookie. This removes the confirmed fresh-cookie bug where the first **More Information** click could trigger a full page reload while language state was being initialized.

Removed from the project:
- All non-English translation TOML catalogs
- All translated README files
- Login language selector
- Panel Settings language selector
- Subscription-page language selector
- Telegram Bot language selector/configuration
- Client-side `LanguageManager` and language reload logic

Only `web/translation/translate.en_US.toml` is loaded. Date and relative-time formatting always use `en-US`. Existing `tgLang` settings are removed from upgraded databases on startup.


## SSH Manager navigation refresh

The SSH Manager tab selector has been simplified and restyled for both desktop and mobile:
- `SSH Inbounds` is now labeled **Inbound**.
- `SSH Users` is now labeled **Client**.
- Both tabs are centered inside a compact rounded box.
- The active tab receives a clear boxed highlight instead of relying on the default underline.
- The mobile layout keeps both tabs equal-width and centered.

This is a UI-only change; SSH inbound and client management behavior is unchanged.

## Xray

- Stock 3x-ui Xray management retained: VLESS, VMess, Trojan, Shadowsocks and supported transports/settings.
- Per-client traffic quota, expiry, reset and enable/disable retained.
- Per-client download/upload speed limits with Xray socket marks, `inet fourxui_xray`, nftables and HTB/IFB shaping.
- Routing safety keeps API/block rules ahead of generated speed rules and preserves normal routing when no speed limit is active.
- Gaming/ping optimization keeps normal/unlimited traffic away from unnecessary IFB shaping.

### Native Payload Bypass

For WebSocket-compatible Xray inbounds, **Payload Bypass** can be enabled under Proxy Protocol.

- The normal inbound port remains the public port.
- Native Go gateway owns the public port.
- Xray is moved automatically to a hidden localhost backend port.
- Custom HTTP payload connections can receive the synthetic `101 Switching Protocols` handshake and then use raw bidirectional relay.
- Normal WebSocket and TLS/WSS passthrough are retained.
- `TCP_NODELAY` and keepalive are used.
- Python/Asyncio is not required.
- Payload Bypass and Proxy Protocol are kept mutually compatible by disabling the conflicting combination.
- The experimental Xray Payload Server Message feature is not included.

## SSH Manager

- Create, edit, delete, enable and disable SSH users.
- Password, expiry and total traffic quota management.
- Daily/weekly/monthly traffic reset.
- Separate upload/download speed limits.
- UID-based accounting through `inet fourxui_ssh`.
- Optimized shared counter polling.
- Managed SSH users are cleaned during uninstall.

### SSH Inbounds

Supported modes:
- Normal SSH
- Payload Only
- TLS/SNI
- TLS + Payload

Also retained:
- Native Go SSH payload gateway
- stunnel integration
- SSH Server Message/Banner
- `TCP_NODELAY` tuning
- Ubuntu `Match LocalPort` / `sshd-socket-generator` compatibility fix
- Rollback-aware Add/Edit/Delete/Enable/Disable operations
- Unchanged SSH/stunnel configs skip unnecessary restarts for faster Save

## UDP Relay

- BadVPN `badvpn-udpgw` integration
- Default port 7300 with custom port support
- Multiple SSH inbounds can share one UDPGW listener
- Persistent `xui-udpgw@<port>.service`
- Boot start, restart-on-failure and localhost listener health check
- Healthy UDPGW uses the fast Save path

Automatic VPS setup can:
- Recover interrupted `dpkg`
- Repair broken apt dependencies
- Install BadVPN package when available
- Build `badvpn-udpgw` when needed
- Create/start/enable the systemd service
- Verify the listener before reporting success

Package-manager lock files are never force-deleted.

## Database and runtime

- SQLite WAL
- Busy timeout
- Bounded connection pool
- Safer concurrent Xray/SSH traffic writes
- Counter baseline protection against double counting
- Soft Go memory target with `XUI_MEMORY_LIMIT` override

## Network and diagnostics

Optional tuning:
```bash
x-ui netopt apply
x-ui netopt status
x-ui netopt rollback
```

Diagnostics:
```bash
x-ui diag
x-ui diag counters
x-ui shaper check
x-ui shaper status
```

## UI

- Original 3x-ui-style structure retained.
- Themes: Light, Dark and stock Ultra Dark.
- Panel language: English only.

## GitHub release / Docker

Repository root remains GitHub Actions ready with:
- `.github/workflows/release.yml`
- `.github/workflows/docker.yml`
- `.github/workflows/codeql.yml`
- `go.mod`
- `Dockerfile`
- `main.go`

Docker image target:
`ghcr.io/sharif102007/4x-ui`

Release architectures include amd64, arm64, armv7, armv6, 386, armv5 and s390x.

Only the current `RELEASE_NOTES_v1.1.11.md` is kept in the repository.


- UI: SSH page tabs (Inbound / Client) restyled as centered rounded pill buttons to match action-button look on mobile and desktop.

- UI fix: replaced the fragile Ant Tabs header styling with two dedicated Inbound/Client action buttons matching the Create/Refresh button layout.
