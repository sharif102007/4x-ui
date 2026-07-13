# HTTP Custom SSH compatibility fix

This build keeps modern OpenSSH algorithms first and adds legacy fallback
proposals used by older Android SSH libraries, including some HTTP Custom
versions.

Fixed error:

`Cannot negotiate, proposals do not match.`

The managed sshd drop-in now includes compatible fallback KEX, host-key,
cipher, and MAC algorithms. The existing `sshd -t` validation and automatic
rollback remain active.

After installing/updating this build, open SSH Manager and save/apply the SSH
port settings once so `/etc/ssh/sshd_config.d/99-xui-ssh-manager.conf` is
regenerated, then reconnect from HTTP Custom.
