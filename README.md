# Chekov

Chekov acts like a transporter for local services: discover the container, pick the route, beam it onto a friendly HTTPS hostname.

It watches running containers, discovers their ports, and turns them into friendly hostnames like `https://myapp.local.dev-gutools.co.uk`.

It will also create aliases, tunnels, and shells for containers that are not directly exposed to the internet.

## Quick start example

Ensure your trusted mkcert CA exists in the default macOS location and
`chekov.local.dev-gutools.co.uk` and your application hostnames resolve to your
machine. With Docker running and ports 80 and 443 free, start a published image
(replace `<published-tag>` with a tag containing the default-bridge fix):

```bash
docker run -d --name chekov --restart unless-stopped \
  -p 80:80 -p 443:443 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v "$HOME/Library/Application Support/mkcert:/etc/nginx/mkcert:ro" \
  'ghcr.io/guardian/chekov/nginx:<published-tag>'
```

The mkcert mount gives Chekov access to the CA private key to generate
certificates. The Docker socket grants powerful access to the Docker daemon,
even when mounted read-only. See the [quick start guide](docs/QUICK-START.md)
for prerequisites, troubleshooting, and the development setup.

To build locally instead:

```bash
docker build -t chekov:local .
docker compose up -d --build
```

For a local test without Compose, see [local testing](docs/BUILDING.md#local-testing-without-docker-compose).

Then start a container:

```bash
docker run -d --name myapp -p 3000:3000 your-app:latest
```

Open:

```text
https://myapp.local.dev-gutools.co.uk
```

The homepage is here:

```text
https://chekov.local.dev-gutools.co.uk/
```

Its Connectivity column checks backends from inside Chekov in the background,
showing HTTP responses, connection/TLS failures, and inactive tunnels. Hover for
details and the last-check time. These checks indicate reachability, not application
health or browser-to-proxy connectivity. Routing configuration failures appear
separately above the endpoint table.

## How routes are created

This is the fall-through order Chekov uses when it decides how to map a container:

### 1) Port label route

If a container exposes one app port, Chekov can route it automatically from the port metadata or a label.

```bash
docker run -d --name api \
  --label devcontainer.application.port=3000 \
  -p 3000:3000 \
  your-api:latest
```

Result:

```text
https://api.local.dev-gutools.co.uk
```

For Dev Containers, a labeled port can be explicit:

```json
{
  "forwardPorts": [3000, 8080],
  "portsAttributes": {
    "3000": { "label": "backend" },
    "8080": { "label": "frontend" }
  }
}
```

This gives separate routes like:

```text
https://backend.local.dev-gutools.co.uk
https://frontend.local.dev-gutools.co.uk
```

A single labeled port also uses its label instead of the container name. Labels
may contain dots: port `3030` labeled `rendering.dotcom` creates
`https://rendering.dotcom.local.dev-gutools.co.uk`. Each dot-separated label must
be a valid DNS label of at most 63 characters.

### 2) Inferred from container name and port

When a container has multiple ports, the name plus chosen app port gives a stable route.

```bash
docker run -d --name myapp \
  -p 3000:3000 \
  -p 9000:9000 \
  your-app:latest
```

If the app port is known, Chekov can map:

```text
https://myapp.local.dev-gutools.co.uk -> :3000
```

### 3) Inferred from container name

If the container has a sensible name and a single app port, Chekov infers the hostname directly.

```bash
docker run -d --name payments -p 8080:8080 your-app:latest
```

Result:

```text
https://payments.local.dev-gutools.co.uk
```

### 4) Tunnelled via a bastion / ALB

If the app is behind an ALB or bastion-host tunnel, use the tunnel page or config entry and point the route at the tunnel endpoint.

```text
https://chekov.local.dev-gutools.co.uk/tunnels
```

This is useful for AWS-backed or remote targets that are not exposed to the internet.

## Aliases

Once a route exists, you can add secondary hostnames without changing the original target.

```text
source: https://api.local.dev-gutools.co.uk
alias:  https://api-public.local.dev-gutools.co.uk
```

This is handy for customer-facing names, temporary URLs, or legacy aliases.

The Alias field offers project-name suggestions for the selected container using
VS Code or IntelliJ Dev Container metadata. Choose a suggestion or type your own
alias; generated container hostnames stay unchanged.

## Access from inside the container

If a Dev Container needs to call the host by the generated route name, add Docker host aliases in `runArgs`:

```json
"runArgs": [
  "--add-host=host.docker.internal:host-gateway",
  "--add-host=myapp.local.dev-gutools.co.uk:host-gateway"
]
```

That makes the container resolve the host machine and the Chekov domain correctly when it needs to call a local HTTPS endpoint from inside the container.

## Shell and tunnel options

The homepage exposes:

- Tunnel UI for bastion/ALB-backed routes
- Bastion shell for AWS/remote access
- Local shell for container-level debugging

```text
https://chekov.local.dev-gutools.co.uk/tunnels
https://chekov.local.dev-gutools.co.uk/remote-shell-launch
https://chekov.local.dev-gutools.co.uk/shell-launch
```

The local shell is especially useful because it keeps credentials inside the container instead of spreading them onto the host machine.

## In one sentence

Chekov turns Docker and Dev Container metadata into a clean local HTTPS routing layer, with optional aliases, tunnels, and shell access for day-to-day development.

## Docs

- Quick start: `docs/QUICK-START.md`
- Shell access: `docs/SHELLS.md`
- Configuration: `docs/CONFIGURATION.md`
- Architecture: `docs/ARCHITECTURE.md`
