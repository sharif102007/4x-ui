# 4x-ui v1.1.5

## SSH inbound reliability fix

- Fixed Ubuntu/OpenSSH `sshd-socket-generator` failures when SSH Server Message uses `Match LocalPort`.
- 4x-ui now probes the distro socket generator before systemd can run it. When the known `lport not in connection test specification` bug is detected, 4x-ui switches to the documented classic `ssh.service` compatibility path by masking only the broken generator override.
- The generator override is ownership-marked. Uninstall removes it only when 4x-ui created it, then restores the normal `ssh.socket` path.
- Per-inbound SSH Server Message/Banner remains available; the fix does not silently remove the banner feature.

## SSH inbound CRUD rollback

- Fixed `Delete` reporting failure after the database row had already been removed.
- Add, Edit, Delete, Enable, and Disable now restore the previous database/runtime state when SSH reconciliation fails.
- This prevents the panel state and live host SSH configuration from drifting apart after a failed operation.

## Release

- Version bumped to `1.1.5`.
- Existing GitHub Actions release flow remains intact: pushing tag `v1.1.5` runs analysis/tests and builds Linux/Windows release assets; the Docker workflow runs from the same tag.
- No VPS release helper is included; repository/tag publishing uses the operator's existing Git commands.
