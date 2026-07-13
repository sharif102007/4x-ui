# Per-user traffic and speed limits

4x-ui applies Linux nftables policies for SSH users and Xray clients. The
installer adds `nftables` and `iproute2` on supported Linux distributions.

## SSH Manager

- Linux UID rules mark each managed SSH user's tunneled connections.
- Named nftables counters record upload and download bytes every five seconds.
- Total-flow exhaustion locks the Linux account automatically.
- Upload and download rates are enforced independently in bits per second.

## Xray inbounds

Clients with an email can enable separate upload and download limits in the
client editor. 4x-ui clones the default outbound, adds a socket mark and a user
routing rule at runtime, then enforces the rates with nftables. This covers the
multi-user VLESS, VMess, Trojan, Shadowsocks, and Hysteria client models.

The existing Xray statistics database remains the source of truth for traffic
quota, reset, expiry, and automatic client disable operations.

## Concurrent session limits

- Xray client and SSH user editors expose `Concurrent Sessions` (`0` means
  unlimited).
- SSH limits are enforced before PAM opens a new authenticated session, so an
  existing connection is not kicked when the limit is reached.
- Xray limits use the access-log session gate and a short activity window. The
  public Xray API does not expose a native per-user connection counter; the
  gate therefore treats recently active source sessions as the count and
  Fail2Ban rejects excess source addresses through the existing IP-limit
  enforcement path.
- `IP Limit` and `Concurrent Sessions` are independent fields; when both are
  set, the stricter active limit wins.

## Runtime requirements

- The panel service must run as root or with permission to manage nftables.
- When any Xray `IP Limit` or `Concurrent Sessions` value is enabled, 4x-ui
  automatically enables the Xray access log and bootstraps Fail2Ban together
  with `nftables`, `iptables`, and `iproute2`. It creates and enables the
  `3x-ipl` jail without an interactive menu step. A one-time Xray restart may
  occur when an existing configuration still has access logging set to `none`.
- Docker deployments require host networking plus `NET_ADMIN` and `NET_RAW`.
- Install and update automatically apply the conservative `4x-ui` host
  performance profile: TCP keepalive, connection backlog, file-descriptor
  and task limits, low swap pressure, and a systemd OOM preference for the
  panel. It is also available as `x-ui optimize-system`.
- Existing custom rules are not flushed: 4x-ui owns only the
  `inet fourxui_ssh` and `inet fourxui_xray` tables.
- If a policy cannot be installed, the panel logs the full nftables error.
