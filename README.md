# Signpost

The front page for a goatnet's administrators. It links to every surface a node
runs and shows read-only health and usage beside them. On a full goatnet it detects
the surfaces itself, so the only required setting is the image.

Signpost is a single static Go binary with no third-party modules. It reads
from other systems and never changes them.

## What it shows

| Stat | Source | Credential |
|---|---|---|
| Up/down for each link | An HTTP request to the link | None |
| Connected and offline peers | NetBird `GET /api/peers` | NetBird token, auditor role |
| Targets, firing alerts, host CPU, memory and disk | Prometheus queries on each observability host | None |
| Organisations, topics, machines | Portal `GET /api/catalog`, `GET /api/machines` | Portal service key |
| Federation peerings: live, in setup, needing attention | Federation `GET /api/summary` | Federation service key |

A tile whose source isn't configured is left off the page.

Stats are collected in the background, every 30 seconds by default, so opening the
page adds no load to those systems. A source that fails keeps its last data, marked
stale with the reason. A failure never appears as a zero.

Apps are found while Signpost runs. With a `discover` block, it reads the `Host(...)`
names from the node's Traefik route files, mounted read-only, on every refresh. An app
in the catalogue appears when its route exists and goes when the route is removed, with
no redeploy. A routed app the catalogue doesn't name appears under "Other", named after
its hostname, until someone adds it to the catalogue. Links in `sections` are always
shown. If the route files can't be read, the last list stays on the page.

Links open inside the page, under a fixed header, with a Services menu to move
between them and the address bar following the open app (`#sentry`). Plain http
links, and links with `"frame": false`, open normally instead: use that for a site
that refuses to be framed, such as the AWS console.

Link checks do not follow redirects. A redirect or 401 counts as up, 403 as refused,
and 404 or 5xx as down.

## Access

Only the groups in `auth.required_groups` get in. Two layers enforce this:

1. An `oauth2-proxy` door, from goatcore's `oauth2-proxy-surface` role, handles login and group checks.
2. Signpost checks `X-Forwarded-Groups` again and refuses any request without a required group.

Traefik also limits the hostname to the mesh. Only `/healthz` is open, and it returns just `ok`.

Containers on the shared `mgmt` Docker network can reach port 8080 directly and could
set that header themselves. This is the same exposure as the other goatnet surfaces.

The portal has no read-only key, so Signpost's key is operator-level, like
Panoscope's and Flashlight's. It stays server-side and Signpost only sends GETs with it.

## Develop

```sh
go test ./...
go run ./cmd/signpost -config config.local.json -listen :8080
```

Every request except `/healthz` needs a group header, so test with:

```sh
curl -H 'X-Forwarded-Groups: PLATFORM-ADMIN' http://localhost:8080/
```

Set `SIGNPOST_NETBIRD_TOKEN`, `SIGNPOST_PORTAL_API_KEY` and `SIGNPOST_FEDERATION_KEY`
to enable those sources.

## Build and deploy

Pushing to `main` publishes `ghcr.io/<owner>/signpost` for arm64 and amd64, so a fork
publishes to its own namespace.

- **On a goatnet:** follow [deploy/README.md](deploy/README.md).
- **Anywhere else:** run the image behind an authenticating proxy that sets
  `X-Forwarded-Groups`, with a config like [examples/config.example.json](examples/config.example.json).

## Third-party

The page design in `internal/server/static/signpost.css` is adapted from the goat
identity console in goatcore, under Apache-2.0. Its licence and notice are in
`third_party/goatcore/`.

## Licence

Apache License 2.0. See [LICENSE](LICENSE).
