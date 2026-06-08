# Rementor Mock Stack

This mock stack gives Rementor something realistic to route without requiring
private services.

It starts four loopback HTTP servers:

| Service | Port | Purpose |
|---|---:|---|
| remote stack | `18080` | remote `web`, `orders-api`, and `billing-api` behavior |
| local web | `28080` | local replacement for the web frontend |
| local orders | `28081` | local replacement for `orders-api` |
| local billing | `28082` | local replacement for `billing-api` |

## Start The Mock Stack

```bash
go run ./examples/mock-stack
```

## Register Demo Apps

In another terminal, start Rementor:

```bash
make dev
make build-ctl
PATH="$PWD/dist:$PATH" ./examples/mock-stack/register-demo.sh
```

Then open the UI:

```text
http://localhost:5173
```

## Try The Local/Remote Switch

Route everything remote:

```bash
rementorctl app list demo
```

Switch only `orders-api` to local:

```bash
rementorctl app toggle demo orders-api
```

With nginx routing configured, requests with the host `api.localhost` and path
`/orders` now hit the local orders implementation while the rest of the
workspace can stay remote.
