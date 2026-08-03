# 4x-ui v1.1.5

## TCP latency

- Explicitly enables TCP_NODELAY on every client socket accepted by the custom
  SSH payload gateway.
- Explicitly enables TCP_NODELAY on the gateway's loopback connection to
  OpenSSH.
- Adds `TCP_NODELAY=1` for both public and backend sockets of every managed
  stunnel service, covering SSH TLS and SSH TLS payload modes.
- Logs socket-option failures without rejecting an otherwise working SSH
  connection.
- Adds unit coverage for the Go socket helper and generated stunnel config.

## Diagnostics

- Adds `x-ui diag latency` to verify stunnel NoDelay, congestion control, and
  the default queue discipline.
- Distinguishes Nagle delay from bufferbloat and does not add an invalid
  `net.ipv4.tcp_nodelay` sysctl.

## Validation

- Synchronizes the internal version, documentation, and tag examples to
  `v1.1.5`.
- The tag workflow remains the authoritative gate for vet, staticcheck, race
  tests, cross-builds, and release packaging.
