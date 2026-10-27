# Security Policy

## Supported versions

Only the `main` branch is maintained.

## Reporting a vulnerability

Please report vulnerabilities privately through
[GitHub Security Advisories](https://github.com/ahmedelgmal310/sre-observability-slo/security/advisories/new)
rather than opening a public issue. Include steps to reproduce and the impact you
observed. You can expect an acknowledgement within 3 business days.

## Scope notes

- The fault-injection feature (`CHAOS_ENABLED=true`) lets any client degrade the
  services through W3C baggage. It is meant for demo and test environments and is
  off by default in the binaries; never enable it on an internet-facing deployment.
- The Compose stack binds every port to `127.0.0.1` and enables anonymous
  read-only Grafana access. It is a local lab, not a hardened production setup.
- Dependencies, base images and actions are pinned (digests / commit SHAs) and
  kept current by Dependabot. CI runs govulncheck and Gitleaks on every push.
