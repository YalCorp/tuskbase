# Security Policy

Tuskbase is currently in a foundation stage. Runtime security guarantees will be documented as implementation lands.

## Reporting A Vulnerability

Please do not report security vulnerabilities in public issues.

Use GitHub private vulnerability reporting or GitHub Security Advisories for this repository. If that is unavailable, open a minimal public issue asking for a private security contact without including exploit details.

Helpful reports include:

- the affected version, commit, or branch,
- a clear description of the vulnerability,
- reproduction steps or proof of concept,
- impact assessment,
- any suggested mitigation.

## Current Posture

Planned security defaults:

- local-first operation,
- local API key for initial auth,
- no JWT, RBAC, cloud sync, or enterprise governance in Phase 1,
- no external embedding service required for default tests,
- no loss of canonical decisions when derived indexing fails.
