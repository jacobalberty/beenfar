# Running beenfar with the web UI

beenfar is two programs: `beenfard`, the controller and its API, and the web UI
([LookMoreLike](https://github.com/jacobalberty/LookMoreLike)), a separate
container. They have to be served from **one origin**: beenfar refuses
cookie-authenticated requests whose `Origin` is not its own `Host`. So something
in front, a small proxy or one you already run, sends beenfar's paths to
`beenfard` and everything else to the UI, **keeping the `Host` header**.

| Path | Goes to |
|---|---|
| `/beenfar.*` (Connect / gRPC-Web, what the UI uses), `/api/` (REST), `/auth/` (OpenID Connect), `/openapi.json`, `/healthz` | `beenfard` (port 8080) |
| everything else | the UI |
| `/inform` | UniFi devices talk to `beenfard` **directly**, never through the proxy |

| File | For |
|---|---|
| [`compose.yaml`](../compose.yaml) | **default**: host networking, a Caddy proxy included |
| [`compose.ipvlan.yaml`](../compose.ipvlan.yaml) | each container on its own LAN address (ipvlan) |
| [`compose.traefik.yaml`](../compose.traefik.yaml) | behind an existing Traefik |
| [`compose.pangolin.yaml`](../compose.pangolin.yaml) | behind Pangolin, with a Newt connector |

## Quick start (host networking)

```sh
git clone https://github.com/jacobalberty/beenfar && cd beenfar   # or copy compose.yaml, deploy/Caddyfile and .env.example
cp .env.example .env                                              # set BEENFAR_ADMIN_PASSWORD
docker compose up -d
```

Open `http://<this host>:8000` and sign in as `admin` with that password. The
password only creates the first admin, when the database is empty; you can
remove it from `.env` afterwards. Without it, create one with:

```sh
docker compose run --rm -T beenfar /beenfard admin create -username admin   # reads the password from stdin
```

Point a UniFi device at the controller with `set-inform http://<this host>:8080/inform`
(or a DNS name that resolves to this host). Host networking is the default because
devices inform the controller on the machine's own address, with no port mapping
or NAT in between.

| Listens on | What |
|---|---|
| `:8080` | `beenfard`: the API, and `/inform` for devices |
| `:8000` | the proxy: **the address to open in a browser** |
| `127.0.0.1:8081` | the UI container, for the proxy only (`beenfard` already owns the host's port 8080) |

Things to know:

- **HTTPS.** Port 8000 is plain http, fine on a trusted LAN. For HTTPS set
  `BEENFAR_PUBLIC_ADDRESS=beenfar.example.com` in `.env`: Caddy then gets a
  certificate itself (ports 80 and 443 must be free, and the name must resolve to
  this host), and tells beenfar the request was HTTPS so the session cookie is
  marked `Secure`.
- **`:8080` is open to the LAN** (devices need it). The API still requires a
  token or a session, but firewall it from networks you do not trust.
- **Data** is in the `beenfar-data` volume (a SQLite database). Back it up with
  the controller stopped. Upgrade with `docker compose pull && docker compose up -d`;
  pin `BEENFAR_VERSION` to stay put.
- **API tokens** for Terraform:
  `docker compose run --rm -T beenfar /beenfard admin token -username admin -name terraform`.

## ipvlan: a controller on its own LAN address

Add your network to `.env`:

```sh
LAN_INTERFACE=eth0
LAN_SUBNET=192.168.1.0/24
LAN_GATEWAY=192.168.1.1
BEENFAR_IP=192.168.1.50
BEENFAR_UI_IP=192.168.1.51
BEENFAR_PROXY_IP=192.168.1.52
```

Choose three addresses your DHCP server will not hand out, then:

```sh
docker compose -f compose.ipvlan.yaml up -d
```

Devices inform `http://192.168.1.50:8080/inform`; browse to `http://192.168.1.52`.
ipvlan (unlike macvlan) also works over wifi interfaces.

**The Docker host itself cannot reach these addresses**, a known limit of ipvlan
and macvlan; other machines on the LAN can. Browse from another machine, or give
the host a shim interface in the same subnet:

```sh
sudo ip link add ipvlan-shim link eth0 type ipvlan mode l2
sudo ip addr add 192.168.1.53/32 dev ipvlan-shim
sudo ip link set ipvlan-shim up
sudo ip route add 192.168.1.50/31 dev ipvlan-shim   # beenfard and the UI
sudo ip route add 192.168.1.52/32 dev ipvlan-shim   # the proxy
```

(lost at reboot; make it persistent with your network manager). The compose file
validates, but it has **not been run on a real LAN**.

## Traefik

`compose.traefik.yaml` expects a Traefik that already runs with the docker
provider, an entrypoint `websecure`, a certificate resolver `letsencrypt` and a
docker network `proxy`; rename those in the file if yours differ.

```sh
echo 'BEENFAR_DOMAIN=beenfar.example.com' >> .env
docker compose -f compose.traefik.yaml up -d
```

It defines two routers on the domain: beenfar's paths (priority 100) go to
`beenfard`, everything else (priority 1) to the UI. Traefik keeps the `Host`
header by default. Devices do not go through Traefik: the file publishes port
8080 for them (bind it to one address if you like), or use `compose.ipvlan.yaml`
for a dedicated address. These rules were checked against a real Traefik,
including cookie sign-in.

To serve the API under a path prefix instead (`/beenfar-api`, stripped by
Traefik), see the second example in
[LookMoreLike's `examples/`](https://github.com/jacobalberty/LookMoreLike/tree/main/examples);
OpenID Connect then also needs `/auth/` routed without the prefix.

## Pangolin

[Pangolin](https://docs.pangolin.net/) publishes services from a private network
through a connector (Newt) running next to them. **These directions follow
Pangolin's documentation and have not been tried against a Pangolin server**;
check the Pangolin pages linked below if a screen differs.

1. In Pangolin, create a **site** and copy its ID and secret
   ([Install Newt](https://docs.pangolin.net/manage/sites/install-newt)). Put
   them in `.env`:

   ```sh
   PANGOLIN_ENDPOINT=https://pangolin.example.com
   NEWT_ID=...
   NEWT_SECRET=...
   ```

2. Start beenfar and Newt (this leaves out the local Caddy proxy; Pangolin
   routes instead):

   ```sh
   docker compose -f compose.yaml -f compose.pangolin.yaml up -d
   ```

   Newt runs on the host network, so it reaches `beenfard` at `127.0.0.1:8080`
   and the UI at `127.0.0.1:8081`.

3. In Pangolin, add one **HTTP(S) resource** for your domain with these
   **targets**, all on that site
   ([Targets](https://docs.pangolin.net/manage/resources/public/targets); a
   target can carry a path and a match type):

   | Path | Match | Target |
   |---|---|---|
   | `/beenfar.` | prefix | `127.0.0.1:8080` |
   | `/api` | prefix | `127.0.0.1:8080` |
   | `/auth` | prefix | `127.0.0.1:8080` |
   | `/openapi.json` | exact | `127.0.0.1:8080` |
   | `/healthz` | exact | `127.0.0.1:8080` |
   | (no path) | | `127.0.0.1:8081` (the UI) |

   Do not enable path rewriting: `beenfard` expects the paths as they are.

4. Open the domain and sign in to beenfar. Things to check, because the
   documentation does not spell them out:

   - **Pangolin's own login.** Public resources have Pangolin's authentication
     on by default. Browsers then sign in to Pangolin first and to beenfar
     second, which works, but API clients using bearer tokens (Terraform) cannot
     pass Pangolin's login. Let those reach `http://<host>:8080` directly, or see
     [Pangolin's authentication options](https://docs.pangolin.net/manage/resources/public/authentication)
     if you want the resource public. I could not confirm a "no authentication"
     setting.
   - **The `Host` header.** beenfar must see the public host name. If sign-in
     fails with *"cross-origin request with browser credentials"*, the `Host`
     reaching `beenfard` differs from the browser's `Origin`; leave the
     resource's custom host header empty and retry.
   - **`X-Forwarded-Proto`.** Pangolin's documentation does not say it is set.
     If the session cookie is not marked `Secure` over HTTPS (look in the
     browser's storage panel), add the custom header `X-Forwarded-Proto: https`
     to the resource.
   - **Devices** still inform `http://<host>:8080/inform` on the LAN; they do not
     go through Pangolin.

## Troubleshooting

| Symptom | Cause |
|---|---|
| *cross-origin request with browser credentials* | the proxy changed the `Host` header, or the UI was opened on a different address than the API (see above). |
| The UI loads but every call fails, or a blank screen | the proxy sends beenfar's paths to the UI (or the reverse); check the path table at the top. |
| The UI container exits with `LOOKMORELIKE_API_BASE ...` | the value must be a path on the same origin (`/` or `/prefix`), never a URL. |
| A device never appears | it must reach `http://<controller>:8080/inform`; check `docker compose logs beenfar`. |
| `docker compose up` fails: *required variable ... is missing* | set it in `.env` (the ipvlan and Traefik files need addresses or a domain). |
