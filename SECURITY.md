# Security Policy

Aegis is, itself, a **security control**: it is the single choke point between
your data and the Agents / applications that query it. This document covers how
to deploy it safely and how to report vulnerabilities.

## Reporting a vulnerability

Please **do not** open a public GitHub issue for security reports.

- Email: **security@aegis-gateway.dev** (replace with your project's address)
- Or use GitHub's private vulnerability reporting on the repository.

We aim to acknowledge within 3 business days and provide a remediation timeline.

## Deployment hardening checklist

Aegis ships with **demo defaults that are unsafe for production**. Before
exposing it:

- [ ] **Rotate all seed credentials.** The first boot seeds
      `admin/admin123`, `analyst/analyst123`, `mcp-agent/mcp123`. Change or
      disable them via the admin API / your own provisioning.
- [ ] **Set `AEGIS_JWT_SECRET`** (or `config.json` `jwt_secret`). An empty /
      default secret lets anyone forge a platform JWT.
- [ ] **Set `AEGIS_MASK_SECRET`** (or `config.json` `mask_secret`). This key
      drives the keyed masking strategies `tokenize` and `fpe`. If it is unset,
      Aegis starts with an **unsafe development default** and logs a startup
      warning — do not run that way in production.
- [ ] **Change `AEGIS_MCP_API_KEY`** (`mcp.api_key`). The default `mcp-demo-key`
      maps to the `mcp-agent` (analyst) account.
- [ ] **Terminate TLS** in front of Aegis (reverse proxy / ingress). Aegis does
      not terminate HTTPS itself.
- [ ] **Network isolation.** Only the Aegis process should hold database
      credentials; Apps and Agents authenticate to Aegis, never to the DB.
- [ ] **Audit logs.** Ship `audit_logs` (and the structured `governance
      decision` events) to your SIEM. They are your forensic source of truth.

## Trust model

- **Default deny.** Nothing is queryable until explicitly granted at table level.
- **Governance is non-bypassable by design.** NL2SQL output, curated metrics,
  and cost estimates all reuse the same `permission.Rewrite` → `proxy.Execute`
  path. There is no "admin fast path" that skips masking for non-`admin` roles.
- **`admin` is a superuser** (bypasses row-level governance) — protect those
  accounts accordingly and prefer SSO/OIDC or LDAP over local passwords.
- **Write protection.** No-WHERE writes are blocked for governed principals and
  affected-row caps are enforced; `admin` is exempt by policy.

## Supported versions

Security fixes target the latest `main`. Pin a commit and watch the repo for
advisories if you run a build from source.
