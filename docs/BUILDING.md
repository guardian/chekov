# Building chekov

## Local Go build

```bash
go mod download
go build -o chekov ./cmd
```

## Docker build

```bash
docker build -t chekov:local .
```

## Run locally with Docker Compose

```bash
docker compose up -d --build
```

This is the recommended workflow for local development because it mirrors the runtime environment, TLS mount layout, and generated config flow used in the container.

## Local testing without Docker Compose

This walkthrough starts Chekov first, then uses a separate demo app container
to check that Chekov discovers and maps it automatically. Both containers use
Docker's default bridge network.

From the repository root, ensure a trusted mkcert CA exists in the default macOS
location, `~/Library/Application Support/mkcert`. Ports 443 and 8000 and the
names `chekov-test` and `chekov-test-app` must be available. Stop any existing
service using port 443 before starting the test proxy.

These commands assume macOS. On another operating system, change the mkcert
CA directory in the Docker mount and curl commands to that system's location.

### 1. Start Chekov

```bash
docker build -t chekov:local-test .

docker run -d --name chekov-test \
  --network bridge \
  -l com.docker.compose.service=chekov \
  -p 127.0.0.1:443:443 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v "$HOME/Library/Application Support/mkcert:/etc/nginx/mkcert:ro" \
  chekov:local-test
```

The label on the test proxy identifies it as Chekov so that it does not
discover itself as an app. The socket gives the container powerful access to
Docker, even with a read-only mount; the mkcert mount exposes the CA private
key for certificate generation.

### 2. Check the homepage

Open https://chekov.local.dev-gutools.co.uk/ and check the endpoints table.
If no other discoverable apps or tunnels are running, it should say
“No published endpoints are currently configured.” Otherwise, note the
existing routes; they need not be stopped.

If the hostname does not resolve locally, use curl instead. `--resolve` directs
the name to the local proxy without changing DNS, and `--cacert` uses the
mkcert root:

```bash
curl --noproxy '*' --cacert "$HOME/Library/Application Support/mkcert/rootCA.pem" \
  --resolve chekov.local.dev-gutools.co.uk:443:127.0.0.1 \
  https://chekov.local.dev-gutools.co.uk/
```

### 3. Start the demo app

The script starts a separate Python web server named `chekov-test-app` and
publishes port 8000. It uses Docker's default bridge unless Docker's defaults
have been changed.

**Use a unique container name:** the script searches running container names
for the supplied text and attempts to kill and remove the named container if
it finds a match.

```bash
./scripts/demo-local-container chekov-test-app 8000
```

Within a few seconds, the homepage should show a new
`chekov-test-app.local.dev-gutools.co.uk` endpoint. Open that URL or check it
without changing DNS:

```bash
curl --noproxy '*' --cacert "$HOME/Library/Application Support/mkcert/rootCA.pem" \
  --resolve chekov-test-app.local.dev-gutools.co.uk:443:127.0.0.1 \
  https://chekov-test-app.local.dev-gutools.co.uk/
```

The response should be `chekov-test-app`. The script publishes port 8000, but
when the containers share the default bridge Chekov routes to the app's bridge
IP. If the route is not ready yet, retry; inspect controller errors with
`docker exec chekov-test tail -n 100 /var/log/chekov/chekov.log`.

### 4. Clean up

Remove the two test containers when finished:

```bash
docker rm -f chekov-test chekov-test-app
```

## Dependencies

The project uses the Docker SDK and supporting Go packages, including:

- `github.com/docker/docker`
- `github.com/docker/go-connections`
- standard library packages for HTTP, JSON, and process management

## Helpful notes

- The project is intended to be built and run in a containerized dev environment.
- If dependency resolution is difficult in a raw Go environment, prefer the Docker build path.
- The entrypoint handles certificate generation and startup sequencing automatically when mounted files are absent.

## Building with the project scripts

There are helper scripts in the repository root that are useful for local shell access and host tunnel operations. See the repository root for the scripts directory and its purpose.
