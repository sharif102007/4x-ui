# 4x-ui v1.1.5

## Theme

- Removed the custom AMOLED/Obsidian override.
- Removed the Eye Comfort theme and all translation entries for it.
- Restored the stock 3x-ui theme behavior: Light, Dark, and stock Ultra Dark.

## Network and limits

- Applies `TCP_NODELAY` and `SO_KEEPALIVE` to both sides of the panel-managed
  stunnel connection and explicitly tunes both payload-gateway TCP sockets.
- Preserves live SSH sessions when the generated stunnel configuration and
  systemd unit are unchanged.
- Keeps the dedicated `xui-stunnel.service`; Debian's global
  `stunnel4.service` is not used by the panel.
- Moves the dedicated config outside `/etc/stunnel/*.conf`, preventing
  Debian's global daemon from loading the same listeners and causing
  `Address already in use` failures.
- Stops rebuilding a healthy HTB/IFB tree when the effective rate policy has
  not changed.
- Removes nftables drop-policer verdicts atomically after HTB/IFB is healthy,
  while retaining marks and traffic counters. The policer remains the fallback
  when queue shaping is unavailable.
- Preserved the stock Xray API routing rule ahead of private-IP block rules.
- Preserved the original routing order when no Xray speed limit is enabled.
- Reduced the duplicated Xray socket-mark rule to one rule per nftables chain.
- Rebuilds active HTB shaping only when a configured speed rate changes or the
  live queue is unhealthy.
- Replaced per-user SSH nftables subprocess polling with one table read per poll.
- Reduced the SSH accounting poll from every five seconds to every ten seconds
  to avoid periodic process/DB spikes on small VPS hosts.
- A failed SSH counter read no longer resets the accounting baseline and
  double-counts traffic on the next poll.
- Recreates deleted nftables tables and missing queue-state files after a
  firewall reload or service restart instead of silently leaving limits off.
- Publishes the latest Xray limit enforcement result in the server status API.

## SSH Manager reliability

- Tests the effective OpenSSH configuration with a complete `Match LocalPort`
  connection specification, fixing inbound delete/update failures on hosts
  with per-port banners.
- Serializes host reconciliation and rolls database changes back when sshd,
  stunnel, payload gateway, UDP relay, certificate, or user operations fail.
- Detects listen/backend/gateway/UDP port collisions before applying changes.
- Generates correct DNS or IP certificate SANs and regenerates a stale
  self-signed certificate when its configured identity changes.
- Keeps the dedicated stunnel service isolated and refuses to mark a TLS
  inbound active when stunnel is unavailable.
- Verifies that `badvpn-udpgw` is actually listening on its loopback port
  before reporting UDP relay reconciliation as successful; occupied ports or
  immediate process crashes now fail and roll the inbound change back.

## Installer

- Removed the legacy automatic 128 MiB TCP buffer tuning.
- Removed the invalid `net.ipv4.tcp_nodelay` sysctl attempt.
- Optional network tuning remains available through `x-ui netopt apply`.
- Stops the panel before migrations/settings changes and starts it only after
  update configuration and certificate checks complete.
- Adds bounded GitHub/ACME network operations and visible download progress.
- Installs stunnel with the other SSH Manager runtime dependencies.
- Supports Linux x86_64/amd64 only; the same release archive is used on Debian,
  Ubuntu, and AlmaLinux.
- Verifies the published SHA-256 and required archive paths before stopping or
  replacing an installed panel.
- Installs the CLI bundled in the same release archive, avoiding a mismatch
  between a tagged binary and the `main` branch script.

## GitHub Actions

- Builds `x-ui-linux-amd64.tar.gz` plus its SHA-256 file and the Windows amd64
  package.
- Builds Docker images only for `linux/amd64`; QEMU and unused architecture
  builds are removed.
- Uses the latest supported major releases of checkout/setup-go and Go 1.26.5.
- Verifies modules, Go formatting, vet, staticcheck, race tests, shell syntax,
  latency safeguards, tag/version agreement, static linking, and archive
  contents before publishing.

## Database and panel update

- Enables SQLite WAL and a busy timeout with a bounded connection pool to
  prevent periodic Xray and SSH traffic writers from failing immediately with
  `database is locked`.
- Propagates traffic write and transaction commit failures instead of logging
  them as successful updates.
- Delays the detached updater briefly so the web endpoint can return its JSON
  response before the service stops.
- Bounds nftables counter reads so shutdown cannot wait forever on the helper
  process.
- Makes inbound/client create, update, and delete transactions report commit
  failures before attempting an incremental Xray runtime sync.
- Prevents malformed client JSON from panicking common add, update, and delete
  paths.

## Repository cleanup

- Keeps the project screenshots, multilingual READMEs, contributor templates,
  runtime scripts, and release/security/container workflows.
- Consolidates duplicate SSH/network notes into `BANDWIDTH-LIMITS.md` and
  removes stale internal QA/Copilot/Dependabot files.
- Removes the daily one-day cache purge that discarded useful Go build caches
  and made the next release build slower.
- Removes the obsolete source-build SSH installer/rollback pair; the supported
  `install.sh`, `update.sh`, release archives, and built-in SSH Manager remain.

## Validation

- All shell scripts pass `bash -n`.
- All Go sources pass a full-repository `gofmt` parse/format check.
- Translation TOML, JSON, GitHub workflow/issue-form YAML, internal Markdown
  links, and screenshot files validate locally.
- The tag workflow remains the authoritative full gate for `go vet`,
  staticcheck, race tests, static amd64 compilation, and release packaging.
