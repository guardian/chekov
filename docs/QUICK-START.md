# Quick Start Guide

## Prerequisites

- Docker Desktop or Docker Engine
- A trusted mkcert CA in the default macOS location, `~/Library/Application Support/mkcert`
- DNS or `/etc/hosts` entries for the generated hostnames if you want browser access without an external resolver
- Nothing else already listening on ports 80/443 on your machine

## Step 1: Start the published image

Replace `<published-tag>` with a tag containing the default-bridge fix:

```bash
docker run -d --name chekov --restart unless-stopped \
  -p 80:80 -p 443:443 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v "$HOME/Library/Application Support/mkcert:/etc/nginx/mkcert:ro" \
  'ghcr.io/guardian/chekov/nginx:<published-tag>'
```

Chekov generates wildcard certificates using your mkcert CA. The CA mount
exposes its private key to the container; the Docker socket also grants
powerful access to the Docker daemon despite the read-only mount. No separate
certificate or log directories are needed. Controller logs are available with
`docker exec chekov tail -n 100 /var/log/chekov/chekov.log`.

The previously published tag `sha-5a37866863aa3d6b5f1d1a78a317ede1c4851f13`
does not include the default-bridge fix; a new image must be published before
using this command with the default Docker network.

To build and run the local source instead, use `docker compose up -d --build`.
Compose uses the certificate and log mounts in `docker-compose.yml`.

## Step 2: Run an app container or open a devcontainer

Any container with a reasonable name is usually enough:

```bash
docker run -d --name myapp \
  -p 3000:3000 \
  your-app:latest
```

Supported patterns include:

- containers with a single exposed or published port
- dev containers with forwarded ports or `portsAttributes`
- any container that the built-in discovery heuristics recognize as a dev target

## Step 3: Open the homepage

Visit:

```text
https://chekov.local.dev-gutools.co.uk/
```

This page shows:

- discovered containers
- generated routes
- active tunnel entries
- direct links to the generated app URLs
- exported config endpoints

## Step 4: Open your app

After discovery, the app is usually available at:

```text
https://myapp.local.dev-gutools.co.uk
```

If you prefer, you can also use the HTTP form:

```text
http://myapp.local.dev-gutools.co.uk
```

## Using dev containers

A devcontainer with a forwarded port is discovered automatically. For example:

```json
{
  "name": "My Project",
  "image": "node:18",
  "forwardPorts": [3000]
}
```

This can become:

```text
https://my-project.local.dev-gutools.co.uk
```

## Using multiple ports

If a container exposes multiple ports, the project can route multiple hostnames. For instance, a devcontainer with `portsAttributes` may produce:

- `myapp-backend.local.dev-gutools.co.uk`
- `myapp-frontend.local.dev-gutools.co.uk`

## Using Docker Compose

```yaml
services:
  app:
    image: node:18
    expose:
      - "3000"
```

This is automatically discovered and exposed as:

```text
https://app.local.dev-gutools.co.uk
```

## Troubleshooting

The homepage checks for route and status changes every two seconds while visible,
and immediately when you return to its tab or navigate back to it. It reloads only
if the data has changed, including alias and tunnel changes.

### The app is not appearing

1. Check the proxy logs:

```bash
docker logs --tail 100 chekov
docker exec chekov tail -n 100 /var/log/chekov/chekov.log
```

2. Inspect the container metadata:

```bash
docker inspect myapp | jq '.Config.Labels, .Config.ExposedPorts, .NetworkSettings.Networks'
```

3. Verify the generated config page:

```bash
curl -k https://chekov.local.dev-gutools.co.uk/generated.conf
```

### TLS or certificate errors

```bash
docker exec chekov ls -la /etc/nginx/certs/
curl -vk --resolve myapp.local.dev-gutools.co.uk:443:127.0.0.1 https://myapp.local.dev-gutools.co.uk
```

### Connectivity issues

```bash
docker exec chekov curl -v http://myapp:3000
docker inspect chekov | jq '.NetworkSettings.Networks'
```

## Tunnel workflow

The built-in tunnels page is available at:

```text
https://chekov.local.dev-gutools.co.uk/tunnels
```

It supports creating local tunnel entries that reverse-proxy to an ALB or other upstream target. For HTTPS backends, you can toggle whether nginx verifies the upstream certificate before proxying.

---

That's it: start the proxy, run your app, and visit the generated hostname.
