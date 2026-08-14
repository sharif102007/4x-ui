# 4x-ui v1.1.4

## Theme

- Removed the custom AMOLED/Obsidian override.
- Removed the Eye Comfort theme and all translation entries for it.
- Restored the stock 3x-ui theme behavior: Light, Dark, and stock Ultra Dark.

## Network and limits

- Preserved the stock Xray API routing rule ahead of private-IP block rules.
- Preserved the original routing order when no Xray speed limit is enabled.
- Reduced the duplicated Xray socket-mark rule to one rule per nftables chain.
- Rebuilds active HTB shaping when a configured speed rate changes.
- Replaced per-user SSH nftables subprocess polling with one table read per poll.
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
  staticcheck, race tests, cross-compilation, and release asset packaging.
