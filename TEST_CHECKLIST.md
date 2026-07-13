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
