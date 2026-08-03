# 4x-ui v1.1.9

## Focused Linux release target

- GitHub Actions now builds one Linux release asset: `x-ui-linux-amd64.tar.gz`.
- The same statically linked amd64 build supports x86_64 Debian, Ubuntu, and
  AlmaLinux VPS hosts; distribution-specific binaries are unnecessary.
- Removed Linux arm64, armv7, armv6, armv5, 386, and s390x release jobs.
- Docker publishing now targets `linux/amd64` only and no longer initializes
  QEMU for unused cross-platform images.
- Installer and updater now stop early with a clear message on non-amd64 CPUs,
  instead of attempting to download a release asset that is no longer built.

## Unchanged behavior

- Debian/Ubuntu systemd and AlmaLinux/RHEL systemd installation support stays
  intact.
- The v1.1.8 connection, ping, stale-qdisc, and traffic-shaper fixes remain in
  place.
- The Windows amd64 workflow is unchanged.
