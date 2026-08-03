# 4x-ui v1.1.7

## SSH connection recovery

- Fully rolls back the explicit TCP_NODELAY experiment from the custom SSH
  payload gateway.
- Removes the added TCP_NODELAY options from managed stunnel configuration.
- Restores the previously working SSH payload and TLS transport path without
  changing payload parsing, certificates, ports, users, or traffic policies.
- Removes the TCP_NODELAY-specific diagnostic mode and unit tests.

## Branding

- Keeps the corrected transparent `4X` README artwork for both light and dark
  GitHub themes.

## Validation

- Synchronizes the internal version, README, release notes, and tag examples to
  `v1.1.7`.
- The tag workflow remains the authoritative gate for vet, staticcheck, race
  tests, cross-builds, and release packaging.
