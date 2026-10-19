# Security policy

## Reporting a vulnerability

Please do not open a public issue for a security problem. Use GitHub's **private vulnerability reporting** on this repository (Security → Report a vulnerability) or contact the maintainer directly. Include what you found, how to reproduce it and what an attacker could do. You will get an answer within a few days, and a fix or a mitigation before the problem is described publicly.

## Automated scanning

GitHub's free scanners run on this repository: CodeQL with Copilot Autofix, secret scanning with push protection, and Dependabot alerts and security updates. What runs when, how it works, what is AI in it and what it costs: [docs/security-model.md](docs/security-model.md#automated-scanning).

## What counts

Anything that lets pull request code, a commenter or a third party apply infrastructure changes, read credentials, bypass approvals, tamper with saved plans, or run commands on a runner outside the intended path. The design is described in [docs/security-model.md](docs/security-model.md).

## Supported versions

The latest `v0.x` release receives fixes; the major tag `v0` always points at it.
