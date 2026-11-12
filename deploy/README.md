# Deploying Signpost to a goatnet

Signpost's role runs on a full goatnet core host, from a goatcore checkout. It
detects the node's surfaces, so the only required setting is the image.

## What it works out for itself

| Thing | How |
|---|---|
| Links | Found live from this host's Traefik routes, which are mounted read-only into the container. `signpost_catalogue` names and places them; an unnamed route shows under `signpost_other_section`. Add your own with `signpost_extra_links`; hide some with `signpost_hidden_surfaces` |
| Prometheus and Grafana | Hosts in the `<site_id>_obs_hosts` inventory group |
| Portal stats | Used when the host has a portal route. The service key is generated and registered |
| Federation stats | Used when the host has a federation route and Federation's `summary-keys.d` exists. A key is generated and dropped there; Signpost reads `http://federation:8090` over the mgmt network |
| Login | Hydra client and cookie secrets are generated and kept in `/etc/goat/signpost/`. Groups follow the identity console |
| Certificates, mesh-only rule | Follow the portal's settings |

The play prints what it detected before changing anything.

## Steps

Run these from `<goatcore>/ops/ansible`, with `<signpost>` as a checkout of this repo.

**1. Copy the role in:**

```sh
cp -r <signpost>/deploy/ansible/roles/signpost roles/
cp <signpost>/deploy/ansible/signpost.yml .
```

**2. Set the image** in `group_vars/<site>_hosts/signpost.yml`, using the digest from the
`image` workflow's run summary:

```yaml
signpost_image: "ghcr.io/<owner>/signpost@sha256:<digest>"
signpost_site_name: "My node"   # optional, defaults to site_id
```

**3. If the image is private,** log in once on the core as root, with a GitHub token limited to `read:packages`:

```sh
sudo docker login ghcr.io -u <owner>
```

**4. Add the name to mesh DNS.** Add `signpost` to `isitup_services` in
`group_vars/<site>_hosts/vars.yml` and to `ops/dns/<site>/mesh-hosts`, then redeploy mesh DNS.

**5. Record it in goatcore's registers:**

- `ops/permissions/group-permissions.yml`: a `door: group-gated` row granting the same groups as Signpost.
- `ops/pins/fleet-pins.yaml`: a `signpost` row, modelled on `flashlight`.

**6. Deploy:**

```sh
../scripts/with-secrets ../scripts/ansible-preflight.sh \
  -i inventory/mgmt-hosts.ini signpost.yml -e target=<site>_hosts
```

The play fails if an anonymous request is not redirected to the login.

**7. Optional: mesh peer stats.** Create a NetBird service user with the `auditor` role, add
its token to your secrets as `<SITE_ID_UPPER>_SIGNPOST_NETBIRD_TOKEN`, and re-run step 6. The
Mesh peers tile is left off until then.

## Check it by hand

From a mesh peer, open `https://signpost.<site_id>.<site_dns_zone>`:

1. A login in an allowed group reaches the page.
2. A login in no allowed group is refused.
3. Every tile shows data or names a reason, and none is stuck on "Checking".

## Requirements

- A goatcore checkout providing the `oauth2-proxy-surface` and `rollback-ledger` roles and
  `tasks/assert-portal-key-accepted.yml`.
- Traefik, Hydra and publicly trusted certificates on the host. Goatlite is not supported,
  because its private CA can't be trusted by `oauth2-proxy-surface`. The play stops with that
  reason rather than leaving a broken login.

## Settings

Everything is in [`roles/signpost/defaults/main.yml`](ansible/roles/signpost/defaults/main.yml). The common ones:

```yaml
signpost_hidden_surfaces: [brief, dataflows]
signpost_extra_links:
  - {section: Tools, name: Wiki, url: "https://wiki.example.net/", description: "Team notes", probe: true}
  # A site that refuses to be framed opens normally instead:
  - {section: Tools, name: Cloud console, url: "https://console.example.com/", description: "Separate sign-in", probe: false, frame: false}
signpost_allowed_groups: "PLATFORM-ADMIN"
```

## Rollback

Remove `/etc/traefik/dynamic/signpost.yml`, then run `docker compose down` in `/opt/signpost` and
`/opt/mgmt-host/oauth2-proxy-signpost`. Nothing else depends on Signpost. To also drop its portal
access, delete `/opt/goat-zenoh-portal/operator-keys.d/signpost.key`, and for Federation,
`/etc/goat/federation/app/summary-keys.d/signpost.key`.
