# 4x-ui v2.9.4 + SSH Manager — Build & Deploy (Debian 12 amd64)

This is the stock **4x-ui v2.9.4** source with an added, self-contained **SSH Manager**
feature. No existing 4x-ui / Xray / VLESS / VMess / Trojan behaviour was changed; all
additions are new files plus small, additive edits to wiring files.

> Built for your own VPS, lawful private use. The panel must run as root (the stock
> systemd unit already does) because it manages OpenSSH, Linux users and the firewall.

---

## 1. Files added (new)

```
database/model/ssh_model.go          SshInbound + SshUser tables
web/service/ssh_system.go            Privileged host ops (sshd/firewall/users/stunnel/cert) — safe exec, no shell
web/service/ssh_payload_gateway.go   In-process payload gateway (CONNECT/GET/POST/WebSocket → backend SSH)
web/service/ssh_manager.go           Orchestration: CRUD, AES password-at-rest, port conflicts, reconcile
web/controller/ssh.go                REST API under the authenticated /panel group
web/html/ssh.html                    SSH Manager page (2 tabs: Inbounds, Users) — responsive
install-ssh-manager.sh               Installer (build, backup, swap, migrate, start, rollback script)
rollback-ssh-manager.sh              Generic rollback helper
TEST_CHECKLIST.md                    Exact verification steps
```

## 2. Files changed (additive only)

```
database/db.go                       Register SshInbound + SshUser for AutoMigrate
web/controller/xui.go                Add /panel/ssh page route + register SshController
web/web.go                           Start SSH runtime on boot, stop gateways on shutdown
web/html/component/aSidebar.html     Add "SSH Manager" sidebar menu item
web/translation/translate.en_US.toml Add menu.sshManager key (other locales fall back to EN)
```

The new DB tables are created automatically by GORM `AutoMigrate` on first start —
there is no manual SQL migration step.

## 3. Build command (Debian 12 amd64, native)

Requires Go **1.26.2** (the version in `go.mod`) and a C toolchain (CGO is needed by
the SQLite driver `mattn/go-sqlite3`).

> Do **not** use Debian's `apt install golang-go` — on Debian 12 that is Go **1.19**,
> which cannot even parse `go 1.26.2` and predates the `GOTOOLCHAIN` auto-download
> (added in Go 1.21). You will get:
> `invalid go version '1.26.2': must match format 1.23`.
> Install the official toolchain instead:

```bash
apt-get update
apt-get install -y gcc curl

# Official Go 1.26.2 (NOT the apt package)
curl -4fLo /tmp/go.tgz https://go.dev/dl/go1.26.2.linux-amd64.tar.gz
rm -rf /usr/local/go
tar -C /usr/local -xzf /tmp/go.tgz
export PATH=/usr/local/go/bin:$PATH
hash -r
go version            # must print: go version go1.26.2 linux/amd64

# Build
cd /root/4x-ui-2.9.4
export GOTOOLCHAIN=local
export CGO_ENABLED=1
export CGO_CFLAGS="-D_LARGEFILE64_SOURCE"
export GOOS=linux GOARCH=amd64
go build -ldflags "-w -s" -o build/x-ui main.go
```

`install-ssh-manager.sh` now performs this Go check/install automatically, so the
simplest path is just to (re-)run that script.

The binary embeds all HTML/JS/translations (`//go:embed`), so deploying = replacing
the single `/usr/local/x-ui/x-ui` binary. There are no separate asset files to copy.

## 4. Important: do not run the official 4x-ui updater after this build

`x-ui update` / the official `update.sh` pulls upstream release binaries and will
overwrite this custom binary. Use only `install-ssh-manager.sh` (or rebuild) to update.

## 5. Runtime artifacts the feature manages on the host

```
/etc/ssh/sshd_config.d/99-xui-ssh-manager.conf   Managed sshd drop-in (additive Port lines + optional Match Banner)
/etc/stunnel/xui-ssh-manager.conf                Managed stunnel config (TLS modes)
/etc/systemd/system/xui-stunnel.service          Isolated stunnel instance (won't disturb other stunnel use)
/etc/x-ui/ssh-manager/certs/ssh-<id>.{crt,key}   Self-signed certs (when chosen)
/etc/x-ui/ssh-manager/banners/ssh-<id>.txt       Optional per-inbound banner
Linux group: xui-ssh-users                       All managed SSH accounts join this group
```

Safety guarantees built into the code:
- sshd drop-in only **adds** `Port` lines — your current SSH port is never removed.
- `/usr/sbin/sshd -t` is run before every restart; config is **rolled back** if invalid.
- `ssh.socket` is disabled only when it conflicts (it ignores `Port` directives).
- Firewall: only **allow** rules are added (ufw/firewalld), never deny, never auto-enable.
- Users: only accounts inside `xui-ssh-users` are ever modified/deleted; a hard-coded
  protected list (root, ubuntu, debian, www-data, x-ui, nobody, …) is always refused.
- Passwords are set via `chpasswd` over **stdin** (never in argv); no shell concatenation.
- All API endpoints sit behind the existing 4x-ui admin session check.
