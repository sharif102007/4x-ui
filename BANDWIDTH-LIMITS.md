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

## Runtime requirements

- The panel service must run as root or with permission to manage nftables.
- Docker deployments require host networking plus `NET_ADMIN` and `NET_RAW`.
- Existing custom rules are not flushed: 4x-ui owns only the
  `inet fourxui_ssh` and `inet fourxui_xray` tables.
- If a policy cannot be installed, the panel logs the full nftables error.
