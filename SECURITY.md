# Security Policy

Silo's core purpose is isolation — running development tools inside a VM so they cannot reach SSH keys, cloud credentials, or other projects on the host. We take reports about isolation issues seriously.

## Scope

### In scope

- **Isolation boundary issues.** Code running inside a silo VM reading, writing, or influencing host state outside the declared mounts (project directory, explicit cache mounts).
- **Unintended exposure of host data.** A tool seeing an environment variable or file from the host that was not listed in `.siloconf` under `pass_env` / `pass_files`.
- **Network allow-list deviations.** A tool reaching a network destination that the `network.proxy.allow` list does not permit.
- **Credential handling mistakes.** Silo itself logging, persisting, or otherwise exposing user credentials it was entrusted with.
- **Integrity of the install path.** Issues affecting the correctness or integrity of the shim directory, the OCI image cache, or the rootfs cache in a way that would influence future runs.

### Out of scope

- Issues in the underlying Apple Containerization framework — please report those upstream at https://github.com/apple/containerization.
- Issues in the tool images themselves (for example, an advisory affecting `python:3.12-slim`). The upstream image is responsible for those; silo's role is to contain any consequences.
- Crashes or errors that stay inside a single run and do not cross an isolation boundary — please file those as regular issues.
- Denial-of-service style resource pressure on the host from a silo VM. VM resource limits are configurable; runaway guests are expected to remain contained by the VM boundary.

## How to report

Please report privately via GitHub Security Advisories:

https://github.com/rchekalov/silo/security/advisories/new

Please do not open a public issue for a security-sensitive finding.

If GitHub Security Advisories is not available to you, open a regular issue asking for a private contact channel — without including any details in the issue body.

## What to expect

- Acknowledgement within about 72 hours.
- An initial assessment within about 7 days: in scope or not, severity, and a rough timeline.
- Coordinated disclosure: once a fix is ready, we agree on a disclosure date and CVE assignment if applicable.

## Supported versions

Silo is pre-1.0. Only the latest minor version (currently 0.4.x) receives security fixes. Please update to new minor releases promptly.
