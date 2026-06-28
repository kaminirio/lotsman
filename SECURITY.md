# Security Policy

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues,
discussions, or pull requests.**

Instead, report them privately using GitHub's
[private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability):
go to the **Security** tab of this repository and click **Report a vulnerability**.

Please include:

- A description of the vulnerability and its impact.
- Steps to reproduce, or a proof-of-concept.
- Affected version(s) / commit, and any relevant configuration.

We will acknowledge your report, keep you updated on progress, and credit you in the
fix (unless you prefer to remain anonymous).

## Supported versions

Lotsman is pre-1.0 and under active development. Security fixes are applied to the
`main` branch. Until a stable release line exists, only `main` is supported.

## Scope notes

Lotsman is a self-hosted platform that reads cluster telemetry. When deploying:

- The in-cluster agent dials **out** to the control plane (egress-only); it does not
  expose an inbound port.
- Treat the agent enrollment token, OAuth/SSO config, and database URL as secrets —
  supply them via environment / Kubernetes Secrets, never commit them.
- RBAC scope is config-driven; review `docs/wiki/concepts/Authentication and RBAC.md`
  before exposing the control plane.
