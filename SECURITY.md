# Security Model

Rementor is designed as a local developer tool. It is not intended to be run as
a hosted gateway or exposed to untrusted networks.

## Local Control Plane

The server accepts loopback bind addresses only. It refuses `0.0.0.0`, public
interfaces, and hostnames that are not `localhost` because the control plane is
not designed as a remotely authenticated service.

Browser RPC calls include a CSRF guard. CLI and MCP calls use the local Connect
RPC API directly.

The container demo publishes port `8080` to `127.0.0.1` by default. Setting
`REMENTOR_DEMO_HOST=0.0.0.0` or using `docker run -p 0.0.0.0:8080:8080`
intentionally exposes the demo to other machines and should only be used on
trusted networks.

Generated proxy routes include CORS headers for local development origins only:
`localhost`, `127.0.0.1`, `::1`, and `*.localhost`. They do not intentionally
allow arbitrary public origins to read responses from services reachable through
Rementor.

Route proof headers use the `X-Rementor-*` namespace. Generated locations hide
upstream copies before adding their own values, and incoming correlation IDs are
validated or replaced with a generated request ID before being forwarded.

## Local Data

Rementor stores workspace definitions, application metadata, routing state, and
route patterns in SQLite under the XDG data directory:

```text
~/.local/share/rementor/rementor.db
```

Rementor creates its app-specific XDG data, cache, and config directories with
`0700` permissions and creates the SQLite database with `0600` permissions.
Treat the database as sensitive and do not copy it into bug reports.

## System Changes

The container demo runs nginx and Rementor as an unprivileged user. Rementor
does not install system nginx configuration, edit `/etc/hosts`, create DNS
services, or install sudoers rules.

For a manually managed nginx instance, review the generated files under
`~/.config/rementor/nginx/` before including that directory. Do not make a
root-owned nginx master load configuration from a directory writable by
untrusted users.

## Reporting

Do not open a public issue containing a credential, private URL, local database,
or sensitive log. Use GitHub's private vulnerability reporting feature for
security reports.
