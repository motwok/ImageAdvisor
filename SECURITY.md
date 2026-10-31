# Security Policy

ImageAdvisor runs as a container with access to the Docker socket and asks registries about the images
of the host's containers. That makes it security-sensitive by nature, and reports are taken seriously.

## Reporting a vulnerability

**Please report vulnerabilities privately; do not open a public issue, pull request, or discussion for a
security problem.**

Use **GitHub's private vulnerability reporting**:

1. Go to the repository's **Security** tab ->
   **[Report a vulnerability](https://github.com/motwok/ImageAdvisor/security/advisories/new)**.
2. Describe the issue with enough detail to reproduce and assess it.

This keeps the report private between you and the maintainer and integrates with GitHub Security
Advisories for a coordinated fix and disclosure.

### What to include

- A clear description of the vulnerability and its **impact**.
- **Steps to reproduce** (or a proof of concept).
- Affected **version** (image tag or digest) and your **Docker version**.
- Any suggested mitigation, if you have one.

## What to expect

This is a single-maintainer project, so responses are best-effort rather than bound to a fixed SLA. You
can expect an acknowledgement of your report, an assessment, and, for confirmed issues, a coordinated fix
and disclosure. Credit is given to reporters who want it.

Please give a reasonable amount of time for a fix before any public disclosure.

## Supported versions

Only the **latest release** (`:latest`) receives security fixes. Older releases are not patched; please
update to the newest one.

| Version | Supported |
|---|---|
| Latest release (`:latest`) | yes |
| Older releases | no |

## Automatic dependency releases

Updates of Go and of ImageAdvisor's dependencies are merged automatically by Dependabot once the build
and the smoke test pass, and **every such merge that changes the image is a release**. Security fixes
therefore reach `:latest` quickly, but a vulnerable or compromised Go release or dependency would be
shipped automatically as well. If you want to review updates first, pin a version tag instead of
`:latest`. Details and mitigations:
[RELEASING.md](RELEASING.md#dependency-updates-are-released-automatically).

## Scope

ImageAdvisor deliberately does things that can look alarming but are by design:

- it mounts the **Docker socket** (read-only use) to list containers and their images,
- it lets the **Docker daemon query registries** for the current digest of image tags,
- `/metrics` exposes **container and image names**. Do not publish its port to untrusted networks.

These documented behaviors are **not** vulnerabilities on their own. Genuine security issues include, for
example: any write or control call against the Docker API (pull, start, stop, remove, ...); changes on
the host; requests to hosts other than the registries of the containers' image references (for example
through a crafted image name); execution of untrusted or attacker-controlled code; or disclosure of data
beyond what is documented for `/metrics`.
