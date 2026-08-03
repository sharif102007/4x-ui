# 4x-ui v1.1.3

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

## Validation

- Root shell scripts pass `bash -n`.
- All translation TOML files parse successfully.
- Changed Go files parse without tree-sitter error nodes.
- A full Go build/test must still be run in CI because the review environment
  did not include the Go toolchain.
