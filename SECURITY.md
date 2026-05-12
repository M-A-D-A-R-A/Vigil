# Security Policy

Vigil handles logs and application metadata, so please treat security reports carefully.

## Supported Versions

Vigil is early alpha. Security fixes currently target the `main` branch and the latest tagged release.

## Reporting A Vulnerability

Please do not open a public issue for a vulnerability.

Report security issues by using GitHub private vulnerability reporting if it is enabled for the repository, or email:

```txt
andoriyanishant@gmail.com
```

Include:

- a short summary
- affected version or commit
- reproduction steps
- expected impact
- any suggested fix, if you have one

## Current Security Notes

- Vigil is designed first for local and trusted-network use.
- Do not expose an early alpha Vigil server directly to the public internet.
- Ingest keys should be treated like secrets.
- Logs can contain sensitive values. Redaction is on the roadmap, but users should avoid sending secrets to Vigil today.
