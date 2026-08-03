# 4x-ui v1.1.6

## Branding

- Replaces the remaining stock `3X` artwork in both README logo variants with
  the correct `4X` branding.
- Keeps separate transparent 400 x 200 assets for GitHub light and dark modes.

## Included from v1.1.5

- Explicit TCP_NODELAY handling for the SSH payload gateway and managed
  stunnel sockets.
- `x-ui diag latency` for checking the low-latency TCP path and distinguishing
  Nagle delay from loaded-link bufferbloat.

## Validation

- Synchronizes the internal version, README, release notes, and tag examples to
  `v1.1.6`.
- The tag workflow remains the authoritative gate for vet, staticcheck, race
  tests, cross-builds, and release packaging.
