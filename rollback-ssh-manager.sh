#!/usr/bin/env bash
#
# rollback-ssh-manager.sh
# Roll back to the most recent backup created by install-ssh-manager.sh, or to a
# specific one passed as $1 (a directory under /root/x-ui-backups/).
#
#   ./rollback-ssh-manager.sh                 # newest backup
#   ./rollback-ssh-manager.sh 20260629-101500 # specific backup
#
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "Run as root."; exit 1; }

BASE="/root/x-ui-backups"
if [[ $# -ge 1 ]]; then
  DIR="${BASE}/$1"
else
  DIR="$(ls -1d ${BASE}/*/ 2>/dev/null | sort | tail -n1 || true)"
fi
DIR="${DIR%/}"

if [[ -z "${DIR}" || ! -d "${DIR}" ]]; then
  echo "No backup found under ${BASE}. Nothing to roll back."
  exit 1
fi

if [[ -x "${DIR}/rollback.sh" ]]; then
  echo "Running ${DIR}/rollback.sh"
  exec "${DIR}/rollback.sh"
fi

# Fallback if the per-run script is missing.
echo "Per-run rollback script not found in ${DIR}; doing a manual restore."
systemctl stop x-ui || true
[[ -d "${DIR}/x-ui-dir" ]] && { rm -rf /usr/local/x-ui; cp -a "${DIR}/x-ui-dir" /usr/local/x-ui; }
[[ -f "${DIR}/x-ui.db.bak" ]] && cp -a "${DIR}/x-ui.db.bak" /etc/x-ui/x-ui.db
rm -f /etc/ssh/sshd_config.d/99-xui-ssh-manager.conf
systemctl stop xui-stunnel.service 2>/dev/null || true
systemctl disable xui-stunnel.service 2>/dev/null || true
rm -f /etc/systemd/system/xui-stunnel.service /etc/stunnel/xui-ssh-manager.conf
systemctl daemon-reload 2>/dev/null || true
/usr/sbin/sshd -t && { systemctl restart ssh || systemctl restart sshd || true; }
systemctl start x-ui
echo "Manual rollback complete."
