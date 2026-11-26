# Security Policy

## Supported versions

Security fixes are released for the latest minor version of the current major
version (`github.com/zt4ff/gokue/v2`). Older major versions (`v1.x`) do not
receive fixes; please upgrade.

| Version | Supported |
| ------- | --------- |
| 2.x     | Yes       |
| < 2.0   | No        |

## Reporting a vulnerability

Please do **not** open a public issue for security problems.

Report it privately through GitHub:
[Report a vulnerability](https://github.com/zt4ff/gokue/security/advisories/new)
(Security tab → "Report a vulnerability").

Include the gokue version, Go version, a description of the impact and, if
possible, a minimal reproduction. You should get an acknowledgement within 7
days. Once a fix is ready it is released and a GitHub security advisory is
published, crediting the reporter unless you ask otherwise.

Vulnerabilities in dependencies (for example the MongoDB driver) should be
reported upstream; open a regular issue here if gokue needs to bump a version.
