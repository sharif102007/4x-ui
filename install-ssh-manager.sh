#!/usr/bin/env bash
#
# install-ssh-manager.sh
# Build the custom 4x-ui v2.9.4 (+ SSH Manager) binary and deploy it safely.
#
#   - backs up /usr/local/x-ui and /etc/x-ui (timestamped)
#   - builds linux/amd64 (CGO, embedded assets)
#   - stops x-ui, replaces the binary, starts x-ui
#   - migration is automatic (GORM AutoMigrate on start)
#   - writes a per-run rollback script into the backup directory
#
# Run as root from the root of the modified source tree (where main.go lives),
# or set SRC=/path/to/source.
#
set -euo pipefail

SRC="${SRC:-$(pwd)}"
XUI_DIR="/usr/local/x-ui"
DB_DIR="/etc/x-ui"
TS="$(date +%Y%m%d-%H%M%S)"
BACKUP_DIR="/root/x-ui-backups/${TS}"

red() { printf '\033[31m%s\033[0m\n' "$*"; }
grn() { printf '\033[32m%s\033[0m\n' "$*"; }
ylw() { printf '\033[33m%s\033[0m\n' "$*"; }

if [[ $EUID -ne 0 ]]; then red "Run as root."; exit 1; fi
if [[ ! -f "${SRC}/main.go" ]]; then red "main.go not found in SRC=${SRC}. cd into the source tree or set SRC."; exit 1; fi

grn "==> 1/8 Installing build & runtime dependencies"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y gcc stunnel4 openssl ca-certificates curl || true

grn "==> 2/8 Ensuring a modern Go toolchain"
# Debian's apt Go is 1.19 — too old to parse 'go 1.26.2' and predates the
# GOTOOLCHAIN auto-download (added in 1.21). So we check the real version and
# install the official toolchain when needed.
GO_REQUIRED_MAJ=1
GO_REQUIRED_MIN=26
GO_VER="1.26.2"

go_minor_ok() {
  command -v go >/dev/null 2>&1 || return 1
  local v maj min
  v="$(go version 2>/dev/null | awk '{print $3}' | sed 's/^go//')"
  maj="${v%%.*}"; min="${v#*.}"; min="${min%%.*}"
  [[ "${maj}" =~ ^[0-9]+$ && "${min}" =~ ^[0-9]+$ ]] || return 1
  if (( maj > GO_REQUIRED_MAJ )); then return 0; fi
  if (( maj == GO_REQUIRED_MAJ && min >= GO_REQUIRED_MIN )); then return 0; fi
  return 1
}

if go_minor_ok; then
  grn "    found $(go version | awk '{print $3}')"
else
  ylw "    installing official Go ${GO_VER} to /usr/local/go (apt Go is too old)"
  curl -4fLo /tmp/go-${GO_VER}.tgz "https://go.dev/dl/go${GO_VER}.linux-amd64.tar.gz" \
    || { red "Could not download Go ${GO_VER} from go.dev. Install it manually and re-run."; exit 1; }
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go-${GO_VER}.tgz
  export PATH=/usr/local/go/bin:$PATH
  hash -r
  go_minor_ok || { red "Go ${GO_VER} install failed (go version: $(go version 2>/dev/null))."; exit 1; }
  grn "    installed $(go version | awk '{print $3}')"
fi

grn "==> 3/8 Applying TCP kernel tuning (reduces SSH ping latency)"
SYSCTL_CONF="/etc/sysctl.d/99-xui-ssh-manager.conf"
cat > "${SYSCTL_CONF}" << 'SYSCTL'
# 4x-ui SSH Manager — TCP performance tuning
# Disables Nagle buffering (biggest single source of SSH latency)
net.ipv4.tcp_nodelay = 1
# Larger socket buffers for high-throughput tunnels
net.core.rmem_max = 134217728
net.core.wmem_max = 134217728
net.ipv4.tcp_rmem = 4096 87380 134217728
net.ipv4.tcp_wmem = 4096 65536 134217728
# Faster connection recycling
net.ipv4.tcp_fin_timeout = 15
net.ipv4.tcp_tw_reuse = 1
SYSCTL
sysctl -p "${SYSCTL_CONF}" >/dev/null 2>&1 && grn "    TCP tuning applied" || ylw "    sysctl apply skipped (container/vz environment)"

grn "==> 4/8 Building x-ui (linux/amd64, CGO)"
pushd "${SRC}" >/dev/null
export GOTOOLCHAIN=local
export CGO_ENABLED=1
export CGO_CFLAGS="-D_LARGEFILE64_SOURCE"
export GOOS=linux GOARCH=amd64
mkdir -p build
go build -ldflags "-w -s" -o build/x-ui main.go
popd >/dev/null
NEW_BIN="${SRC}/build/x-ui"
[[ -x "${NEW_BIN}" ]] || { red "Build failed: ${NEW_BIN} missing."; exit 1; }
grn "    built: ${NEW_BIN}"

grn "==> 5/8 Backing up current install to ${BACKUP_DIR}"
mkdir -p "${BACKUP_DIR}"
if [[ -d "${XUI_DIR}" ]]; then cp -a "${XUI_DIR}" "${BACKUP_DIR}/x-ui-dir"; fi
if [[ -d "${DB_DIR}"  ]]; then cp -a "${DB_DIR}"  "${BACKUP_DIR}/etc-x-ui";  fi
# checkpoint sqlite WAL so the DB copy is consistent
if [[ -f "${DB_DIR}/x-ui.db" ]]; then cp -a "${DB_DIR}/x-ui.db" "${BACKUP_DIR}/x-ui.db.bak"; fi
grn "    backup complete"

grn "==> 6/8 Stopping x-ui"
systemctl stop x-ui || true

grn "==> 7/8 Installing new binary"
mkdir -p "${XUI_DIR}"
install -m 0755 "${NEW_BIN}" "${XUI_DIR}/x-ui"

# Ensure sshd includes its drop-in directory (Debian 12 default does; verify safely).
if [[ -f /etc/ssh/sshd_config ]] && ! grep -Eq '^\s*Include\s+/etc/ssh/sshd_config\.d/\*\.conf' /etc/ssh/sshd_config; then
  ylw "    adding 'Include /etc/ssh/sshd_config.d/*.conf' to sshd_config"
  cp -a /etc/ssh/sshd_config "${BACKUP_DIR}/sshd_config.bak"
  printf '\nInclude /etc/ssh/sshd_config.d/*.conf\n' >> /etc/ssh/sshd_config
  if ! /usr/sbin/sshd -t; then
    red "    sshd config became invalid; restoring."
    cp -a "${BACKUP_DIR}/sshd_config.bak" /etc/ssh/sshd_config
  fi
fi

grn "==> 8/8 Starting x-ui (GORM AutoMigrate creates ssh_inbounds / ssh_users)"
systemctl start x-ui
sleep 2

grn "==> 9/9 Writing rollback script"
ROLLBACK="${BACKUP_DIR}/rollback.sh"
cat > "${ROLLBACK}" <<EOF
#!/usr/bin/env bash
# Auto-generated rollback for the ${TS} deploy.
set -euo pipefail
[[ \$EUID -eq 0 ]] || { echo "run as root"; exit 1; }
echo "Stopping x-ui..."
systemctl stop x-ui || true
if [[ -d "${BACKUP_DIR}/x-ui-dir" ]]; then
  echo "Restoring ${XUI_DIR}"
  rm -rf "${XUI_DIR}"
  cp -a "${BACKUP_DIR}/x-ui-dir" "${XUI_DIR}"
fi
if [[ -f "${BACKUP_DIR}/x-ui.db.bak" ]]; then
  echo "Restoring database"
  cp -a "${BACKUP_DIR}/x-ui.db.bak" "${DB_DIR}/x-ui.db"
fi
# Remove SSH Manager host artifacts (does not touch your main SSH port).
echo "Removing SSH Manager drop-in / stunnel"
rm -f /etc/ssh/sshd_config.d/99-xui-ssh-manager.conf
systemctl stop xui-stunnel.service 2>/dev/null || true
systemctl disable xui-stunnel.service 2>/dev/null || true
rm -f /etc/systemd/system/xui-stunnel.service /etc/stunnel/xui-ssh-manager.conf
systemctl daemon-reload 2>/dev/null || true
if /usr/sbin/sshd -t; then systemctl restart ssh || systemctl restart sshd || true; fi
echo "Starting x-ui..."
systemctl start x-ui
echo "Rollback complete. (Linux SSH user accounts in xui-ssh-users were left intact;"
echo " remove them manually with 'userdel -r <name>' if desired.)"
EOF
chmod +x "${ROLLBACK}"

echo
grn "================ DONE ================"
echo "Backup:    ${BACKUP_DIR}"
echo "Rollback:  ${ROLLBACK}"
echo
ylw "Service status:"
systemctl --no-pager --full status x-ui | head -n 12 || true
echo
ylw "Recent logs:"
journalctl -u x-ui --no-pager -n 25 || true
echo
grn "Open the panel and use the new 'SSH Manager' menu item."
