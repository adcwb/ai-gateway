# Security Policy

## Supported versions

Security fixes are applied to the latest release line. Older tags do not receive backports until the project reaches v1.0.

## Reporting a vulnerability

Please **do not open a public GitHub issue** for security problems.

Report privately via **GitHub Security Advisories** ("Report a vulnerability" on the repository's Security tab). Include reproduction steps, affected configuration (database driver, deployment mode), and impact assessment if possible.

You can expect an acknowledgement within 72 hours and a status update within 14 days.

## Scope notes for deployers

- The management API is only protected when `system.admin_token` / `AIGW_ADMIN_TOKEN` is set — an empty token logs a startup warning and leaves `/ai/gateway/*` open. Never expose an unconfigured instance to an untrusted network.
- `system.encryption_key` must be exactly 32 bytes and treated as a production secret: it encrypts virtual keys and upstream provider keys at rest.
- The metrics listener (`:9090`) is unauthenticated by design; bind it to an internal interface.
- Audit log bodies may contain prompts/completions; control access to the database accordingly.
