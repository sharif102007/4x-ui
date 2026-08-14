# 4x-ui v1.1.7

## Fast SSH inbound save

- Added no-op detection for the generated OpenSSH drop-in. If the effective SSH
  config is unchanged and sshd is healthy, Save no longer rewrites, validates,
  or restarts the SSH service.
- Added no-op detection for the dedicated stunnel config and systemd unit. A
  healthy unchanged stunnel service is reused without daemon-reload, enable, or
  restart.
- A previously 4x-ui-owned UDPGW listener now has a fast health-check path. If
  the systemd template is unchanged and the local listener is healthy, Save
  skips repeated systemctl operations and the 8-second startup wait.
- If a UDPGW unit is active but its listener is unhealthy, 4x-ui restarts it
  immediately instead of waiting for the full health-check timeout first.

## Automatic Debian/Ubuntu package recovery

- First-time UDP Relay setup now runs `dpkg --configure -a` automatically.
- Runs `apt-get -f install -y` to repair broken dependencies before installing
  BadVPN/build dependencies.
- apt operations use `DPkg::Lock::Timeout=90` and dpkg lock failures are retried
  briefly, so unattended-upgrades or another legitimate package operation can
  finish without manual intervention.
- 4x-ui never deletes dpkg/apt lock files and never kills the active package
  manager.
- Package repair/install/build remains first-enable-only; once
  `badvpn-udpgw` exists, normal SSH inbound saves do not run apt/dpkg.

## Preserved fixes

- Keeps the v1.1.6 selective HTB/IFB latency changes, TCP_NODELAY tuning, UDPGW
  persistence/health validation, shared relay ports, and automatic VPS relay
  setup.
- Keeps the v1.1.5 OpenSSH `Match LocalPort` compatibility and CRUD rollback
  protections.

## Release

- Version bumped to `1.1.7`.
- Existing GitHub binary-release and Docker/GHCR workflows are preserved.
- No VPS release helper script is included.
