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

## GitHub Actions

- Builds and releases only `x-ui-linux-amd64.tar.gz` plus its SHA-256 file.
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
