# Examples

The examples directory contains neutral, local-only workflows for evaluating
Rementor without private infrastructure.

## Mock Stack

`mock-stack` starts remote and local service stand-ins on loopback ports. Use it
to demonstrate the core Rementor workflow:

1. route every service to the remote mock stack
2. switch one service to a local implementation
3. compare behavior through the same route
4. watch local and remote health state change in the UI

See `examples/mock-stack/README.md`.

## Container Demo

`docker` contains the unprivileged nginx configuration and entrypoint used by
the repository root `Dockerfile`. From the repository root:

```bash
docker compose up --build
```

Then open `http://localhost:8080`.

The Compose demo binds the host port to `127.0.0.1` by default. To expose it on
another interface for a trusted network demo, run:

```bash
REMENTOR_DEMO_HOST=0.0.0.0 docker compose up --build
```
