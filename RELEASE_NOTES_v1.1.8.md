# 4x-ui v1.1.8

This release fixes the connection and ping regression by comparing the current
tree with the supplied working `4x-ui-1.1.2` archive.

## Connection and latency fixes

- Restored the working release's stable traffic-shaper state guard. Repeated
  policy notifications no longer delete and rebuild the live WAN/IFB qdisc
  tree when its enabled state has not changed.
- Added startup detection for the persistent `ifb-4xui` device. After an x-ui
  process restart, stale shaping is now reconciled instead of being forgotten.
- The installer and updater now roll back the previous queue tree before
  replacing an installed release, preventing old HTB/IFB state from surviving
  an upgrade.
- Kept the working SSH payload gateway, SSH system integration, and stunnel
  transport behavior unchanged; those core files already matched the supplied
  working release.
- No experimental `TCP_NODELAY` sysctl or stunnel socket directive is added.
  TCP_NODELAY is not a Linux sysctl, and forcing unsupported stunnel syntax can
  prevent the service from accepting connections.

## Release consistency

- Updated the internal panel version and release examples to `1.1.8` / `v1.1.8`.
- Retained the corrected transparent 4X light and dark logos.
