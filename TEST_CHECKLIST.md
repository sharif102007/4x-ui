# SSH Manager — Test Checklist

Run these on the Debian 12 VPS after `install-ssh-manager.sh`. Keep a **second SSH
session open on your existing port** the whole time so you can never be locked out.

Legend: `PANEL` = the 4x-ui web UI; `VPS` = a root shell on the server; `CLIENT` =
your laptop / phone tunneling app.

---

## 0. Sanity / no regressions
- [ ] `systemctl status x-ui` is **active (running)**.
- [ ] PANEL loads, you can log in with your existing admin creds.
- [ ] PANEL → Inbounds: all your existing Xray/VLESS/VMess/Trojan inbounds are present
      and unchanged; Xray is running (Overview page shows running).
- [ ] New **SSH Manager** item is in the sidebar; opens a page with exactly two tabs:
      **SSH Inbounds** and **SSH Users** (no dashboard).
- [ ] Resize the browser / open on a phone: tables scroll horizontally, the layout
      stacks cleanly, the sidebar collapses to the drawer.

## 1. Create a user
- [ ] SSH Users → Add SSH User: username `tuser1`, click **Generate** for a password,
      Enabled on, Save. Toast = success.
- [ ] VPS: `id tuser1` shows group `xui-ssh-users`.
- [ ] VPS: `getent passwd tuser1` shows shell `/usr/sbin/nologin`.
- [ ] Protected-user guard: try to add user `root` (or `www-data`) → rejected.
- [ ] Bad name guard: try `bad name!` → rejected (only A-Z a-z 0-9 _ -).

## 2. Normal SSH + custom port from panel
- [ ] SSH Inbounds → Add: name `normal`, mode **Normal SSH**, Host = your VPS IP,
      Listen Port = a free port (e.g. **2201**), click **Check** → "available", Save.
- [ ] VPS: `ss -ltnp | grep ':2201'` shows sshd listening.
- [ ] VPS: `grep -n 'Port 2201' /etc/ssh/sshd_config.d/99-xui-ssh-manager.conf`.
- [ ] Your **original** SSH port still works (test in the spare session / a new login).
- [ ] CLIENT: `ssh -p 2201 tuser1@<VPS-IP>` authenticates (password from step 1).
      Tunnel test: `ssh -p 2201 -N -D 1080 tuser1@<VPS-IP>` then browse via SOCKS 1080.
- [ ] Client string shown by the panel (eye/QR icon) is `HOST:2201@tuser1:<pass>`.

## 3. Port-conflict rejection
- [ ] Try to add another inbound on port **2201** → rejected ("already used").
- [ ] Try to add an inbound on your **panel port** or an **Xray inbound port** → rejected.
- [ ] Try a port held by another process (e.g. 22 if sshd already there via main config
      and not managed) → live probe rejects it.

## 4. SNI-only (TLS), blank payload
- [ ] Ensure stunnel installed: `dpkg -l | grep stunnel4` (installer does this).
- [ ] SSH Inbounds → Add: mode **SSH + TLS/SNI**, Host = your domain (or IP),
      Listen Port (public TLS) = **8443**, Backend OpenSSH Port = **2202**,
      Certificate = Self-signed, Save.
- [ ] VPS: `systemctl status xui-stunnel` active; `ss -ltnp | grep ':8443'` (stunnel)
      and `ss -ltnp | grep ':2202'` (sshd).
- [ ] CLIENT (SNI tunneling app, e.g. an SSH-over-TLS client): Host=`<domain>` Port=`8443`
      SNI=`<domain>` **payload blank**, user `tuser1`. Connects and tunnels.
- [ ] Quick manual TLS check from the VPS itself:
      `openssl s_client -connect 127.0.0.1:8443 -servername <domain> </dev/null` shows a
      TLS handshake; piping an SSH client through it reaches the SSH banner.

## 5. SNI + Payload (CONNECT / GET / WebSocket)
- [ ] SSH Inbounds → Add: mode **SSH + TLS/SNI + Payload**, Host=`<domain>`,
      Listen Port = **8444**, Backend OpenSSH Port = **2203**, Self-signed, Save.
- [ ] VPS: `ss -ltnp | grep ':8444'` (stunnel) and a loopback gateway port is up
      (`ss -ltnp | grep 127.0.0.1` shows an extra high port); `ss -ltnp | grep ':2203'`.
- [ ] CLIENT, payload **WebSocket** template (from the panel examples):
      `GET / HTTP/1.1[crlf]Host: <domain>[crlf]Upgrade: websocket[crlf]Connection: Upgrade[crlf][crlf]`
      → connects.
- [ ] CLIENT, payload **CONNECT** template:
      `CONNECT <domain>:8444 HTTP/1.1[crlf]Host: telegram.org[crlf][crlf]` → connects.
- [ ] CLIENT, payload **GET** template:
      `GET / HTTP/1.1[crlf]Host: instagram.com[crlf][crlf]` → connects.
- [ ] Custom Host header (telegram.org / instagram.com / anything) still routes to the
      backend SSH — confirms the gateway ignores Host and is not pinned to one payload.

## 6. Enable / disable / delete
- [ ] Toggle an inbound off → its public port disappears from `ss -ltnp` (sshd port is
      removed from the drop-in / stunnel service stops); toggle on → reappears.
- [ ] Disable `tuser1` → `ssh` with its password is refused (account locked);
      enable → works again.
- [ ] Set an **expiry date** in the past on a user → login refused after save
      (`chage -l tuser1` shows the expiry).
- [ ] Delete `tuser1` → `id tuser1` returns "no such user"; row gone from the panel.
- [ ] Delete an inbound → its port no longer listens; remaining inbounds unaffected.

## 7. Safety / lock-out protection
- [ ] Edit the sshd drop-in by hand to something invalid, then trigger a reconcile
      (toggle any inbound) → the code runs `sshd -t`, **rolls back**, returns an error,
      and SSH keeps running on the previous good config. (Then re-toggle to re-apply.)
- [ ] `journalctl -u x-ui | grep ssh-manager` shows create/edit/delete/enable/reconcile
      log lines for the actions above.
- [ ] If `ssh.socket` was active before, it is now disabled
      (`systemctl is-enabled ssh.socket` = disabled/masked) and `ssh.service` is active.

## 8. Persistence
- [ ] `systemctl restart x-ui` → on boot the panel reconciles: stunnel + gateways +
      sshd ports come back for all enabled inbounds (`ss -ltnp` matches the panel).

---

# 1.1.1 changes — verification

None of the items below were compiled or run before delivery. Work top to bottom;
each section is independent, so a failure in one does not block the others.

## Build gate (do first)
- [ ] GitHub Actions build succeeds for every architecture.
- [ ] `gofmt -l .` prints nothing.
- [ ] `go vet ./...` clean.
- [ ] `go test ./...` passes (48 existing tests, none were modified).

## A. SSH keepalive / reconnect
- [ ] VPS: `grep -E 'TCPKeepAlive|ClientAlive' /etc/ssh/sshd_config.d/99-xui-ssh-manager.conf`
      shows `TCPKeepAlive yes`, `ClientAliveInterval 30`, `ClientAliveCountMax 6`.
- [ ] VPS: `sshd -t` exits 0 (the panel runs this before restarting; confirm by hand too).
- [ ] Your **existing** SSH session survives the sshd restart.
- [ ] CLIENT: connect a tunnel, leave it fully idle 10+ minutes on mobile data,
      then use it — it should still be alive. This is the regression the change targets.
- [ ] VPS: `ss -tnp | grep sshd` shows dead peers being reaped rather than accumulating.

## B. SSH expiry auto-disable
- [ ] Set a test user's expiry to ~2 minutes out. Within 5s of expiry the panel
      shows the user **Disabled** (previously it stayed Enabled).
- [ ] VPS: `passwd -S <user>` shows the account locked.
- [ ] Log shows `ssh-manager: disabled <user> after expiry`.
- [ ] A user whose expiry is 0 (never) is **not** disabled.

## C. UDP relay MTU  ⚠ highest-risk change
- [ ] VPS: `journalctl -u x-ui | grep udpgw` — relay starts, no crash loop.
- [ ] VPS: `ps aux | grep udpgw` shows `--udp-mtu 8192`.
- [ ] CLIENT: UDP through the tunnel still works (DNS, game/voice traffic).
- [ ] **If udpgw fails to start**: the installed badvpn build does not know this
      flag. Set `XUI_UDPGW_MTU=0` in `/etc/default/x-ui`, `x-ui restart` — the flag
      is then omitted entirely and behaviour returns to 1.1.0.

## D. Firewall / port conflicts
- [ ] Create a Hysteria2 inbound on a free port. With ufw active,
      `ufw status | grep <port>` shows a **udp** rule (previously tcp only).
- [ ] A VLESS/TCP inbound still gets a tcp rule only.
- [ ] An inbound with listen `127.0.0.1` gets **no** firewall rule.
- [ ] Try to create an Xray inbound on **80** or **444** (your SSH inbounds) → rejected
      with "port N is already used by SSH inbound ...".
- [ ] Try to create one on **18493** (the panel) → rejected.
- [ ] Existing inbounds still save unchanged when you edit something unrelated.

## E. Shaper auto-apply
- [ ] With no speed limits set: `x-ui shaper status` shows nothing installed.
- [ ] Set a download limit on one client → within seconds `x-ui shaper status`
      shows HTB classes, log shows `shaper: queue-based traffic shaping active`.
- [ ] Measure actual throughput for that client — it should now land near the
      configured rate instead of far below it.
- [ ] Panel and SSH are **not** shaped (they ride the default class).
- [ ] Remove the last speed limit → shaping is rolled back automatically.
- [ ] On a kernel without htb/ifb: one warning, panel keeps working on the policer.
- [ ] `x-ui restart` with limits configured → shaping comes back on its own.

## F. Fail2Ban
- [ ] Fresh install on a clean Debian 12: `fail2ban-client status 3x-ipl` works
      without any manual step.
- [ ] Re-running `install.sh` on an existing box prints "already configured, skipping".
- [ ] `x-ui iplimit remove` then `x-ui iplimit install` both work non-interactively.
- [ ] Deliberately break the package install → panel install still completes.

## G. UI (implicit globals)
- [ ] Inbounds page: Edit, Reset Traffic, Delete Client, QR code, Export links,
      Copy, and the per-client enable toggle each work **first click**.
- [ ] Open two row dropdowns quickly in succession — no cross-talk, no wrong
      inbound shown in the modal.
- [ ] Client Information opens first click (regression check on the 1.1.0 fix).
- [ ] Browser console clean during all of the above.
- [ ] Repeat on mobile.

## H. Resource limits / runtime
- [ ] VPS: `systemctl show x-ui -p LimitNOFILE` reports 1048576.
- [ ] Panel log at startup contains `runtime: soft memory limit set to N MiB`.
- [ ] `XUI_MEMORY_LIMIT=0` in the environment file → log says the limit is disabled.
- [ ] Under load, `systemctl status x-ui` shows no "too many open files".

## I. No regressions
- [ ] All pre-existing inbounds, clients, SSH users and traffic counters intact
      after the update.
- [ ] Reboot the VPS: everything above still holds.

## J. Access log / IP limit (added after the first 1.1.1 pass)
- [ ] Panel log at startup: `access log: enabled at /var/log/x-ui/access.log ...`
      (only on the first run; it is not rewritten afterwards).
- [ ] PANEL → Xray Configs: the `log.access` field now has a path.
- [ ] Set a client's IP limit to 1, connect from two devices → the second is
      limited. Previously this silently did nothing.
- [ ] `x-ui banlog` shows entries.
- [ ] If you had deliberately set `log.access` yourself, it is **unchanged**.

## K. iptables dependency
- [ ] VPS: `command -v iptables` succeeds after a fresh install.
- [ ] `fail2ban-client status 3x-ipl` shows the jail actually banning
      (the 3x-ipl action uses iptables, not nftables).

## L. UDP diagnostics
- [ ] `x-ui diag udp` prints kernel UDP counters, socket buffers, conntrack
      usage and udpgw state without error.
- [ ] With iperf3 on a second host: `x-ui diag udp <peer-ip> 10` reports
      throughput and Lost/Total Datagrams both directions.
- [ ] `x-ui diag full` and `x-ui diag counters` still behave as before.
