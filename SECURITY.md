# Security Policy

## Supported versions

Security fixes are applied to the latest release on `main`. Older releases do not
receive backports — releases are cheap here, so the fix is a new version rather
than a patch to an old one.

## Reporting a vulnerability

Please **do not** open a public issue for a security vulnerability.

Report privately using GitHub Security Advisories:

1. Open [https://github.com/dpalfery/TerraGraph/security/advisories/new](https://github.com/dpalfery/TerraGraph/security/advisories/new)
2. Include steps to reproduce, the impact, and any suggested fix
3. You will get an acknowledgement, and disclosure will be coordinated with you

## What TerraGraph touches

Worth knowing when judging whether something is a vulnerability:

- **It reads, it does not write.** TerraGraph parses `.tf` files and, optionally,
  a `terraform show -json` document. It creates no files, mutates no state, and
  never invokes `terraform`.
- **It makes no network calls.** Not to a registry, not to a cloud provider, not
  for telemetry. Remote modules are deliberately left unresolved rather than
  fetched.
- **It needs no credentials.** Static analysis of HCL requires none, and the
  optional overlay is a file you generated yourself.
- **Plan documents are parsed narrowly.** A `terraform show -json` plan contains
  every attribute of every resource, before and after — for a real stack that is
  megabytes of provider state, much of it sensitive. TerraGraph decodes only the
  fields it needs for instance counts and pending actions, so attribute values
  never enter memory and cannot reach any output.
- **The MCP server is stdio-only.** It listens on no port and accepts no remote
  connections; its only client is the process that spawned it.

Things that *would* be vulnerabilities and are worth reporting: a crafted `.tf`
or plan file causing a crash, unbounded memory growth, or path traversal outside
the indexed repository; the installer accepting a tampered archive; or resource
attribute values from a plan appearing in tool output.

## Installer

[`install.sh`](install.sh) is fetched over HTTPS and verifies every download
against the SHA-256 published in each release's `checksums.txt`. It refuses to
install on a mismatch, and refuses to install at all if the checksums file cannot
be fetched — it fails closed rather than falling back to unverified binaries.

If you would rather not pipe a script into a shell, download the release archive
directly and verify it yourself:

```bash
sha256sum -c checksums.txt --ignore-missing
```

## Dependencies

Dependabot watches Go modules and GitHub Actions weekly. Third-party licences and
their obligations are listed in [NOTICE](NOTICE).
