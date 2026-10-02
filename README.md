# Filedeck

![Filedeck](branding/filedeck_banner.png)

**Filedeck is a self-hosted web file browser.** You run it in Docker on your server, open it in a browser, and you and your users can browse, upload, preview, edit and share files from the server's disks and network shares.

## Where it comes from

Filedeck is a from-scratch successor to [File Browser](https://github.com/filebrowser/filebrowser). File Browser has had 62 published security advisories; Filedeck was written to close those whole classes of bugs with a simpler, stricter design instead of patching them one by one. Filedeck shares no code with File Browser. How each advisory is handled: [docs/SECURITY.md](docs/SECURITY.md).

It is built to work safely next to other programs: the same folders can be used at the same time over SMB/NFS or by other applications, and Filedeck never silently overwrites a file someone else has changed.

## What it does

- **Spaces** — each user gets "My files", and the administrator can add server folders (local disks, SMB/NFS shares) as extra spaces.
- **Accounts and permissions** — for each space the administrator decides who can browse, download, add and change files.
- **Upload** files and whole folders with a button or by dragging them onto the window. Large uploads resume after an interruption.
- **Preview** photos, videos, music, PDFs and text files; **edit** text files in the browser.
- **Copy, move, rename** and **search**, also across spaces and for many selected items at once.
- **Trash** — deleted items can be restored for 30 days.
- **Public links** to a file or folder, with an expiry date and an optional password.
- **Two-factor authentication** (an authenticator app) for every account.
- Light and dark theme, English and Polish interface, works on phones.

What changed in each version: [CHANGELOG.md](CHANGELOG.md).

## Installation

You need a Linux server (kernel 5.8 or newer) with Docker and Docker Compose v2.

**1. Get the files**

```sh
git clone https://github.com/pbuzdygan/filedeck.git
cd filedeck
cp .env.example .env
```

**2. Set the address** — edit `.env`. `FILEDECK_ORIGIN` must be exactly the address you will type in the browser.

| How you will open Filedeck | Put in `.env` |
|---|---|
| Only on the server itself | nothing — the default is `https://localhost:8443` |
| From other computers on your network | `FILEDECK_BIND=0.0.0.0`<br>`FILEDECK_ORIGIN=https://192.168.1.10:8443` (your server's IP or name) |
| Through your own reverse proxy with a domain | see [Behind a reverse proxy](#behind-a-reverse-proxy) |

To use the released image instead of building it on your server, also set `FILEDECK_IMAGE=ghcr.io/pbuzdygan/filedeck:latest`.

**3. Start it**

```sh
docker compose up -d          # add --build when building locally
docker compose logs filedeck
```

The log shows the address, the certificate fingerprint and a one-time **setup code**.

**4. Create the administrator** — open the address in the browser (`https://` is required). Filedeck uses its own self-signed certificate, so the browser shows a warning the first time; you can compare the fingerprint with the one in the log. Enter the setup code, choose the administrator's name and a password (at least 12 characters). Done.

Check that it is running: `docker compose ps` should show `healthy`.

## Configuration

All settings go in `.env` (every one is optional). Restart with `docker compose up -d` after changes.

| Setting | What it does | Default |
|---|---|---|
| `FILEDECK_ORIGIN` | The exact address people open. Also used to build public links. | `https://localhost:8443` |
| `FILEDECK_BIND` | Network address the port listens on. `0.0.0.0` = reachable from your network. | `127.0.0.1` (this server only) |
| `FILEDECK_PORT` | Port on the server. | `8443` |
| `FILEDECK_USER` | `UID:GID` Filedeck runs as. It must be allowed to write the folders you add. | `65532:65532` |
| `FILEDECK_SECRET_KEY` | Encrypts two-factor secrets in the database. Recommended, see below. | none |
| `FILEDECK_PROXY_CIDR` | Address of your reverse proxy, see below. | none |
| `FILEDECK_FILE_MODE` / `FILEDECK_DIR_MODE` | Permissions of new files and folders (ignored on SMB mounts). | `0640` / `0750` |
| `FILEDECK_IMAGE` | Released image to use instead of a local build. | local build |

**The two-factor key.** Generate it once with `openssl rand -base64 32`, put it in `FILEDECK_SECRET_KEY` and **keep a copy outside the server** (e.g. in a password manager). With it, a stolen copy of the data or a backup does not reveal anyone's two-factor secrets. Do not lose or change it later — without the right key Filedeck refuses to start, and two-factor authentication then has to be turned off for every user. You can also pass the key as a Docker secret, see `compose.override.example.yaml`.

### Adding server folders (spaces)

Copy `compose.override.example.yaml` to `compose.override.yaml` (Compose reads it automatically, and it is never overwritten by updates) and list the folders. Each folder mounted as `/spaces/<name>` becomes a space called `<name>`:

```yaml
services:
  filedeck:
    volumes:
      - /mnt/nas/shared:/spaces/nas          # space "nas"
      - /srv/archive:/spaces/archive:ro      # browsing and downloading only
```

Run `docker compose up -d`; the log shows e.g. `Space "nas": read-write`. Then give users access to the new space in **Users** (a new space is not shared with anyone automatically).

Things to know:

- **Write access** — the folder must be writable by `FILEDECK_USER`, otherwise the space is read-only. For an SMB share use the same UID/GID in the mount options (`uid=`, `gid=`), or set `FILEDECK_USER` to the folder's owner. If you change `FILEDECK_USER` after Filedeck already has data, change the owner of the data once (service stopped):
  `docker run --rm -v filedeck_filedeck-data:/data -v filedeck_filedeck-files:/files busybox chown -R 1001:1001 /data/state /files/own`
- **The hidden `.filedeck` folder** — Filedeck keeps uploads in progress and the trash in a `.filedeck` folder inside each writable space. It is invisible in Filedeck, but SMB users may see it; you can hide it in Samba with `veto files = /.filedeck/`.
- **Test a share before using it**: `docker compose run --rm filedeck selftest /spaces/<name>` tries every file operation Filedeck needs in a temporary folder and prints PASS or FAIL. NFS is not supported for writing (it cannot guarantee that a file is never overwritten).

### Behind a reverse proxy

For access from the internet, put Filedeck behind your reverse proxy (Nginx Proxy Manager, Caddy, Traefik, nginx). The proxy handles the real certificate; Filedeck then talks plain HTTP to it. Two settings in `.env`:

```sh
FILEDECK_ORIGIN=https://files.example.org   # the address people open
FILEDECK_PROXY_CIDR=172.18.0.1/32           # the address the proxy connects from
```

| Your proxy forwards to | `FILEDECK_PROXY_CIDR` | In Nginx Proxy Manager |
|---|---|---|
| the server's IP and Filedeck's published port | the gateway of Filedeck's Docker network, e.g. `172.18.0.1/32` | scheme `http`, host `192.168.1.10`, port `8443` |
| the Filedeck container, both in the same Docker network | the proxy container's address, e.g. `172.18.0.5/32` | scheme `http`, host `filedeck`, port `8443` |

**Not sure which address?** Put anything there, open the site through the proxy and run `docker compose logs filedeck`: a line like `untrusted_proxy client=172.18.0.1 … set FILEDECK_PROXY_CIDR=172.18.0.1/32` tells you the exact value.

Proxy checklist:

- pass the original `Host` header and set `X-Forwarded-For` (Nginx Proxy Manager, Caddy and Traefik do both by default);
- allow request bodies of at least 16 MB and long timeouts, so large uploads and downloads are not cut off;
- the proxy's access log contains public link addresses (`/s/…`, `/api/public/…`) — keep it private.

## Using Filedeck

- **Files** — the tabs at the top are your spaces. Click a folder to open it, a file to preview it. Upload with the button or by dragging files onto the window (or onto a folder). Select several items with checkboxes, Ctrl+click or Shift+click to copy, move, download or trash them together.
- **Share a file or folder** — choose *Share link*, set how long it lasts and, optionally, a password, then copy the link. All your links are under **Shared links** in the menu, where you can revoke them.
- **Header** — buttons for the language (EN/PL) and the theme (auto, light, dark), and the **Menu**: *Shared links*, *Change password*, *Two-factor authentication*, and for administrators *Users* (accounts and per-space permissions: list, read, create, modify).
- **Trash** — every space has its own; restore items from there. Only administrators can delete permanently.

## Maintenance

| Task | Command |
|---|---|
| Update (released image) | `docker compose pull && docker compose up -d` |
| Update (local build) | `git pull && docker compose up -d --build` |
| Logs | `docker compose logs -f filedeck` |
| Health | `docker compose ps` (should be `healthy`) |
| Reset a forgotten password | `docker compose stop` → `printf '%s\n' 'new-password' \| docker compose run --rm -T filedeck reset-password admin` → `docker compose start` |
| Turn off two-factor authentication for a user who lost their phone and recovery codes | `docker compose stop` → `docker compose run --rm filedeck reset-2fa admin` → `docker compose start` (or an administrator does it in **Users**) |

**Your data** lives in two Docker volumes: `filedeck-data` (accounts, sessions, certificate) and `filedeck-files` ("My files"). Back up both. `docker compose down` keeps them; `docker compose down -v` deletes them.

**Image versions.** `latest` is the current stable release (`1.0.0`, `1.1.0`, … for a fixed version); `dev_latest` follows the development branch. Images exist for `amd64` and `arm64`.

**Security log.** Sign-ins, failed attempts and account changes appear in the container log with the visitor's address, e.g. `msg=login_failed client=203.0.113.5 user=anna`. For fail2ban or CrowdSec, match `msg=(login_failed|link_unlock_failed|setup_failed) client=<HOST>`. The full list of events is in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#security-log).

## More

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — how Filedeck works inside: security design, API, running without Docker, tests, releases.
- [docs/SECURITY.md](docs/SECURITY.md) — File Browser's advisories and how Filedeck handles each.
- [docs/CONTRACT.md](docs/CONTRACT.md) — detailed security contracts and limitations.
- [CHANGELOG.md](CHANGELOG.md) — what changed in each version.
