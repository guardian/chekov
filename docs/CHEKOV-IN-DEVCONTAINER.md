# Access Chekov inside a Dev Container

This setup gives you two URLs on your laptop:

| URL | What it opens |
| --- | --- |
| `https://inner-chekov.local.dev-gutools.co.uk/` | The Chekov UI inside the Dev Container |
| `https://inner-test.local.dev-gutools.co.uk/` | A demo API container running inside the Dev Container |

Chekov uses the short request hostname in the page heading and browser tab
title. The inner homepage says **Inner-Chekov: Home** and the outer homepage
says **Chekov: Home**.

Outer Chekov runs on your host's Docker daemon. Both outer aliases point to
the **same Dev Container route on port 8080**. Inner Chekov runs on the
Docker-in-Docker daemon and uses the original hostname to choose between its
own UI (`inner-chekov`) and the demo API (`inner-test`).

```text
Browser → outer Chekov (HTTPS :443) → Dev Container :8080 (HTTP) → inner Chekov :80
```

The examples use the default base domain, `local.dev-gutools.co.uk` and it is assumed that
`*.local.dev-gutools.co.uk` resolves to 127.0.0.1 everywhere.

## 1. Start outer Chekov

If it isn't already running, follow the [quick start](QUICK-START.md). Confirm
that you can open `https://chekov.local.dev-gutools.co.uk/` from your host and
that your browser trusts its certificate. Keep the UI local to your machine:
when publishing the outer ports, use `127.0.0.1:80:80` and `127.0.0.1:443:443`
rather than binding to every interface.

## 2. Start the Dev Container

Start a Docker-in-Docker Dev Container listening on port `8080`. Inner Chekov
provides that listener in step 3.

## 2a. Check network

Under normal practices, you will not need this step, but it is included here 
for completeness.

On the **host**, use `docker ps` to find the Dev Container's actual Docker
name or ID (`xxx` below). If it and outer Chekov do not already share a Docker
network, put them on a dedicated one:

```bash
docker network create chekov-dev
docker network connect chekov-dev xxx
docker network connect chekov-dev chekov
docker restart chekov
```

Replace `chekov` if your outer container has a different name. The restart
lets outer Chekov pick up its new network; do it before adding aliases, since
restarting Chekov clears its in-memory aliases. You don't need to publish the
Dev Container's port `8080` to the host or enable IDE port forwarding.

## 3. Start inner Chekov

Run this **inside the Dev Container**, where `docker ps` should show the inner
daemon's containers, not the host's. Use an image that includes the fix for
aliases of Chekov's own UI surviving route refreshes; substitute a newer tag or
build from this checkout inside the Dev Container if your image lacks it.

```bash
docker run -d --name inner-chekov \
  -p 8080:80 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  ghcr.io/guardian/chekov/nginx:latest
```

This publishes port `8080` **on the Dev Container**, not on the host. A log
message about a self-signed fallback certificate is expected without an inner
certificate mount: outer Chekov talks to inner Chekov over HTTP. The inner
certificate is only used by clients that connect directly to inner Chekov
over HTTPS. For HTTPS **upstreams**, inner Chekov instead needs to trust the
public CA that signed the upstream service's certificate.

## 4. Add the alias on inner Chekov

Still **inside the Dev Container**, run:

```bash
curl -fsS -o /dev/null -w '%{http_code}\n' \
  -H 'Host: chekov.local.dev-gutools.co.uk' \
  --data-urlencode 'sourceHostname=chekov' \
  --data-urlencode 'alias=inner-chekov' \
  http://127.0.0.1:8080/api/aliases
```

Expect `303`. Use the API because Chekov's own UI route isn't listed in the
Aliases source dropdown. To check the alias before involving outer Chekov:

```bash
curl -fsS -o /dev/null -w '%{http_code}\n' \
  -H 'Host: inner-chekov.local.dev-gutools.co.uk' \
  http://127.0.0.1:8080/
```

Expect `200`.

## 5. Add the alias on outer Chekov and open it

Open the **outer** Chekov UI. In **Aliases**, select the discovered Dev
Container route that targets port `8080` over HTTP. Use the hostname shown in
the UI; it may differ from the Docker container name. Add `inner-chekov` as
the alias. Outer Chekov preserves the request hostname, so inner Chekov needs
the same alias to select its UI route.

Open **`https://inner-chekov.local.dev-gutools.co.uk/`** without `:8080`. If
the Dev Container route is missing, check the shared network and that outer
Chekov discovers port `8080`. If the route exists but fails, repeat the curl check in
step 4 to isolate the inner hop. Recreate an alias after restarting the
Chekov instance that holds it; a Docker route refresh alone does not clear it.

## 6. Expose a demo API from the same Dev Container

1. Start your `inner-test` API container on the **inner** Docker daemon. Open
   the inner Chekov UI and check that it discovered
   `inner-test.local.dev-gutools.co.uk`. If it discovered the API under a
   different hostname, add `inner-test` as an alias for that route **on inner
   Chekov**.
2. On **outer Chekov**, add an `inner-test` alias pointing to the **same Dev
   Container port-8080 route** you used for `inner-chekov`. No additional
   port mapping on the Dev Container or laptop is needed.
3. Open `https://inner-test.local.dev-gutools.co.uk/` or call your API's
   endpoint at that hostname.
