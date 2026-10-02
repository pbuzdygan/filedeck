# Filedeck architecture

How Filedeck works inside: the security design, the container, the API, running without Docker, tests and releases. For installation and everyday use see the [README](../README.md). Related documents: [CONTRACT.md](CONTRACT.md) (security contracts and limitations in detail), [SECURITY.md](SECURITY.md) (File Browser's 62 advisories and how each is handled), [PROGRESS.md](PROGRESS.md) (status and next steps), [analysis/](analysis/) (analysis of the original File Browser).

## Overview

Filedeck is a single Go binary (`cmd/filedeck`) with the web interface embedded in it. Docker is the primary environment; it runs on Linux only, because it depends on Linux-specific system calls (`openat2`, `renameat2`, `statx`). There is no `os/exec`, no plugins and no HTML templates (enforced by `internal/audit`).

- `internal/storage` — every file operation, through directory handles.
- Accounts, sessions, upload records and links — a transactional bbolt database in the private state directory.
- `internal/web/static` — the interface (plain JavaScript, no build step), texts in `lang-en.json` / `lang-pl.json`.

## Container

- Runs as UID 65532 by default (`FILEDECK_USER`), with a read-only root filesystem, no capabilities, `no-new-privileges` and a PID limit.
- Volume `filedeck-data` → `/data/state` (accounts, sessions, certificate, upload and trash registry); volume `filedeck-files` → `/files/own` (the "My files" space). Every directory mounted under `/spaces/<name>` becomes a space.
- On the first start Filedeck creates `/data/state` (`0700`) and `/files/own` (`0750`) as `FILEDECK_USER`; the image contains only empty mount points with the sticky bit (like `/tmp`). The image has no shell.
- State (sessions, uploads in progress, certificate) survives `docker compose down`/`up`.
- First start: without an active administrator the service starts in setup mode and prints a random one-time setup code, which changes on every start and stops working once an administrator exists. Alternatively, with the service stopped: `printf '%s\n' 'password' | docker compose run --rm -T filedeck bootstrap admin`.
- Account commands (`bootstrap`, `reset-password`, `reset-2fa`) require a stopped service — a running instance locks the state.
- Every CLI flag has a `FILEDECK_<NAME>` equivalent (e.g. `FILEDECK_TLS_CERT`, `FILEDECK_TLS_KEY` for your own certificate); a flag takes precedence over the variable.

## TLS and origin

- `FILEDECK_ORIGIN` / `-origin` must match the browser address exactly; a request with a different `Host` gets 421 `unexpected_host`, which returns and logs the expected origin.
- Without a proxy Filedeck serves HTTPS itself: its own certificate (`-tls-cert`/`-tls-key`) or a self-signed one generated in the state directory for the origin's host name (`-tls-self-signed`; the SHA-256 fingerprint is logged).
- `FILEDECK_TLS_SELF_SIGNED` is automatic when empty: on without a proxy, off when `FILEDECK_PROXY_CIDR` is set.
- HSTS: Filedeck sends `Strict-Transport-Security: max-age=31536000` itself (without `includeSubDomains`).

### Reverse proxy

With `-proxy-cidr` / `FILEDECK_PROXY_CIDR` Filedeck serves plain HTTP and accepts connections only from that CIDR; others are rejected and logged as `untrusted_proxy` with the exact value to set. At start the log says `Behind a reverse proxy: plain HTTP on :8443, accepted only from …`.

Identity comes only from the session. `X-Forwarded-For` is read only from the trusted proxy and only to find the visitor's address for rate limits and the security log. It is read from the right, skipping hops inside the CIDR, so an address a visitor adds themselves is never used. Trusting only the proxy's own address (a `/32`) instead of a whole network means a published port cannot be used to bypass the proxy.

## Storage and spaces

- Access to spaces through handles (`openat2` with `RESOLVE_BENEATH`, `RESOLVE_NO_SYMLINKS`, `RESOLVE_NO_XDEV`), rejecting traversal, symlinks and crossing into nested mounts.
- Only regular files are read; the type is checked through `O_PATH` before opening the data. Listing skips symlinks and special files and limits the number of processed entries.
- Space names: lowercase letters, digits, `. _ -`. A read-only space is detected automatically. The state directory must be outside all spaces.
- The hidden `.filedeck` directory (mode `0700`) of each writable space holds uploads in progress and the trash, so that publication and moving to the trash stay on one filesystem and are atomic. It is unreachable also through name aliases (letter case, trailing dots/spaces, another name for the same directory — device and inode are compared).
- Rename, trash and restore use `renameat2(RENAME_NOREPLACE)` — they never overwrite. Permanent deletion happens only from the trash, without following symlinks and without crossing mounts. Trash items are deleted after 30 days or manually by an administrator.
- New files and folders get `FILEDECK_FILE_MODE` / `FILEDECK_DIR_MODE` (default `0640`/`0750`); on SMB mounts the mount options decide.

### Filesystem requirements

`renameat2(RENAME_NOREPLACE)`, `flock` and directory sync: local disks and CIFS/SMB. NFS does not support `RENAME_NOREPLACE` — publication returns an error instead of risking an overwrite. Support for specific SMB/NFS servers has not been tested yet. If a required mechanism is missing, Filedeck fails instead of falling back to something less safe.

`filedeck selftest <dir>` performs every operation Filedeck relies on (publication without overwriting, rename, trash, editor save with version check, copy, search, hiding `.filedeck` including case variants) inside a temporary `filedeck-selftest-*` folder that it removes afterwards. It prints PASS/FAIL and the modes of new files; a non-zero exit code means the directory must not be used for writing. It does not need the state directory, so it runs next to a running service.

## Uploads

- Private staging, limits on file size, chunk size, reserved bytes and the number of uploads globally and per user. The owner of an upload is checked on every operation on it.
- The offset check and the chunk write are serialized; rollback after an error, a limit breach or cancellation.
- Permissions, completeness and size are checked again before publication. A new name is created atomically without overwriting an existing file, directory or symlink.
- Cleanup removes only its own staging file. A failure to confirm durability after publication is distinguished from a failed publication.
- Durable upload records: the offset is written after the data is fsynced, two-phase publication is resolved after a restart by the presence of the staging file, the commit is idempotent with its result kept for 24 h. Expiry and cancellation remove the staging file and the record; staging files without a record are removed at startup.
- An upload survives a restart and even a server crash: after a lost response or connection the client asks `GET /api/uploads/{id}` for the confirmed `offset` and continues from there. If the commit response was lost, the client repeats the commit or checks the status (`state: "published"`) — for 24 h it gets the same result, with no risk of a second publication.
- Upload publication is serialized with logout and account changes: after a successful logout or disable, an old request cannot publish a file. Downloads started earlier may finish.

Defaults: file 1 GiB, chunk 8 MiB, staging 4 GiB, 32 active uploads, 4 per user, validity 1 hour. These are prototype limits, to be tuned for the product. The internal model allows many chunks; the CLI splits a local file automatically.

## Accounts, sessions and permissions

- The first administrator is created locally only (setup code or `bootstrap`); the API has no open registration and no header-based login. Passwords: at least 12 characters, Argon2id.
- Random sessions stored as hashes, with idle (30 min) and absolute (12 h) expiry, max. 8 per account; sessions survive a restart. A password change, reset, account disable or permission change revokes all sessions of the account; the last active administrator cannot be disabled or demoted.
- `__Host-` cookie, `HttpOnly`, `Secure`, `SameSite=Strict`. CSRF protection through a session-bound token, an exact `Origin` and `Sec-Fetch-Site`.
- Login limits per address and globally, a limit on concurrent requests, a 60 s deadline (downloads keep going while data flows), JSON and header limits, strict JSON and query parsing.
- Permissions are a per-space bit mask (`"spaces": {"files": 15, "*": 3}`; `*` = all spaces, also those added later): 1 list, 2 read, 4 create (upload, folders), 8 modify (rename, trash, restore). Denied by default.
- Two-factor authentication (TOTP) is managed by each user. `FILEDECK_SECRET_KEY` (or `FILEDECK_SECRET_KEY_FILE`, e.g. a Docker secret) encrypts the TOTP secrets (AES-256-GCM) and recovery codes (HMAC) in the account database. Existing secrets are encrypted on the next start and the database file is rewritten so no unencrypted copy stays in it. A different or missing key later stops the start with a clear message. Without a key everything works, and the log says the secrets are not encrypted.

## Web interface

Embedded in the binary: only same-origin resources, CSP without `unsafe-*`, no inline scripts or styles, Trusted Types (no `innerHTML`), file names inserted as text, the CSRF token kept only in memory, files always downloaded as attachments. Previews are served inline only for a fixed list of types, with `nosniff` and CSP `sandbox`.

## Security log

Sign-ins and security-relevant changes are written to the container log, one line each, with the visitor's address — passwords, codes and tokens never are:

```
time=… level=WARN msg=login_failed client=203.0.113.5 user=anna reason=password
```

Events: `login`, `login_failed` (`reason=password|code|code_locked`), `logout`, `rate_limited`, `reauth_failed`, `password_changed`, `password_reset`, `user_created`, `user_updated`, `user_deleted`, `setup_failed`, `setup_completed`, `totp_enabled`, `totp_disabled`, `totp_recovery_codes_renewed`, `totp_reset`, `link_created`, `link_revoked`, `link_unlock_failed`, `untrusted_proxy`. A sign-in with a recovery code is logged as a warning (`recovery_code=true`). For fail2ban or CrowdSec, match `msg=(login_failed|link_unlock_failed|setup_failed) client=<HOST>`.

## API

Every request other than GET/HEAD requires an `Origin` header equal to the origin and the `X-CSRF-Token` from the login response.

| Method and path | Description |
|---|---|
| `POST /api/auth/login` | `{"username","password","code"}` → session cookie and `csrf` token; an account with two-factor authentication gets 401 `totp_required` after a correct password without `code` (a 6-digit code or a recovery code) |
| `GET /api/auth/me`, `POST /api/auth/logout` | current session with the `csrf` token and upload limits, logout |
| `POST /api/auth/password` | change password, revokes all sessions of the account |
| `GET /api/auth/totp`, `POST /api/auth/totp/setup`, `POST /api/auth/totp/enable`, `POST /api/auth/totp/recovery`, `POST /api/auth/totp/disable` | own two-factor authentication: state and recovery codes left; a new secret (`{"password"}` → `key`, `uri`, `qr`); turning on with the first code (`{"code"}` → `recovery_codes`, other sessions end); new recovery codes and turning off (`{"password"}`) |
| `GET /api/spaces` | the user's spaces with their permissions (an administrator sees all) |
| `GET /api/files?space=&path=` | listing (`.` or missing = root of the space) |
| `POST /api/folders` | `{"space","path"}` — new folder in an existing directory, never overwrites |
| `POST /api/rename` | `{"space","from","to"}` — rename/move within a space, never overwrites |
| `GET /api/search?space=&path=&q=` | search by name below a folder (case-insensitive), max. 500 results, 200,000 scanned entries, 10 s — `truncated: true` when a limit is reached |
| `GET /api/preview?space=&path=` | inline preview for a fixed list of types only (images, video, audio, PDF), `nosniff`, CSP `sandbox` |
| `GET`/`PUT /api/text` | editor: `{"content","version"}`; saving with `version` replaces the file only in that version (409 `changed`), an empty `version` creates a new file; limit 2 MiB UTF-8 |
| `POST /api/transfers`, `GET /api/transfers[/{id}]`, `DELETE /api/transfers/{id}` | background copy/move: `{"kind":"copy"\|"move","from":{space,path},"to":{space,path}}` or `"items":[{from,to},…]` (up to 1000, one after another, the first error stops), progress, cancellation |
| `POST /api/trash`, `GET /api/trash?space=` | move to trash, trash contents |
| `POST /api/trash/{id}/restore`, `DELETE /api/trash/{id}` | restore (`{"path"}`, empty = original), permanent deletion — administrators only |
| `GET /api/content?space=&path=` | download as an attachment, single `Range` |
| `POST /api/uploads` | `{"space","path","size"}` → upload ID |
| `PATCH /api/uploads/{id}` | `application/octet-stream` chunk, `Upload-Offset` header |
| `GET`/`DELETE /api/uploads/{id}`, `POST /api/uploads/{id}/commit` | status (`state`, confirmed `offset`), cancellation, publication — a repeated commit returns the stored result |
| `GET`/`POST /api/users`, `PUT`/`DELETE /api/users/{id}`, `POST /api/users/{id}/password`, `DELETE /api/users/{id}/totp` | administration; changes require `reauth_password`; deleting ends the account's sessions and removes its public links (not allowed for your own account or the last administrator); `…/totp` turns off someone's two-factor authentication and ends their sessions |
| `POST /api/links` | `{"space","path","expires_in_hours","password"}` → `url` of the public link (shown only once) |
| `GET /api/links[?all=1]`, `DELETE /api/links/{id}` | own links with their `available` state (administrator: all), revocation |
| `GET /s/{token}`, `GET /api/public/{token}[/files?path=\|/content?path=]`, `POST /api/public/{token}/unlock` | link page and API without signing in: information, folder listing, download, unlocking with a password |

## Running without Docker (development)

Requirements: Linux with `openat2` and `statx(STATX_MNT_ID)` (kernel 5.8 or newer), an accessible `/proc/self/fd`, Go 1.27.1, and a filesystem meeting the [requirements](#filesystem-requirements).

From the repository root:

```sh
go build -o bin/filedeck ./cmd/filedeck
mkdir -p demo/files demo/state
chmod 700 demo/state
printf 'First Filedeck file\n' > demo/source.txt
./bin/filedeck -root demo/files -state demo/state put demo/source.txt hello.txt
./bin/filedeck -root demo/files -state demo/state list
./bin/filedeck -root demo/files -state demo/state read hello.txt
```

The `list`, `read` and `put` commands run with the operating-system operator's privileges and are not an authentication boundary for network users — only `serve` plays that role. Additional spaces: `-spaces-dir DIR` (every subdirectory is a space), and `-space NAME` selects the space for `list`/`read`/`put`. Uploading to `hello.txt` again returns a conflict and keeps the previous content. Nested targets work if the parent directory already exists. Paths relative to a space use `/`; `.` means the root, for listing only.

Server (the password is read from the terminal or stdin):

```sh
./bin/filedeck -root demo/files -state demo/state bootstrap admin
./bin/filedeck -root demo/files -state demo/state \
  -listen 127.0.0.1:8080 -origin http://127.0.0.1:8080 -insecure-local serve
```

The web interface is at `-origin`. `-insecure-local` allows HTTP on a loopback address only, for development. Otherwise `-origin https://…` is required, plus TLS (`-tls-cert`/`-tls-key` or `-tls-self-signed`) or `-proxy-cidr` of a reverse proxy terminating TLS.

## Translations

Interface texts live in `internal/web/static/lang-en.json` (default, source) and `lang-pl.json`. A new text is added to **both** files; `TestTranslations` (part of `go test ./...`) checks that the languages have the same keys and placeholders (`{name}`), and that every key used in HTML/JS and every API error code has a translation. A new language: another `lang-<code>.json` file plus an entry in `LANGS` in `app.js`/`share.js` and in the test.

## Tests

```sh
go test -count=1 -timeout=60s ./...
go vet ./...
go test -race -count=1 -timeout=120s ./...
go test ./internal/storage -run='^$' -fuzz=FuzzValidPath -fuzztime=10s -parallel=2
```

The race detector needs a C compiler; without one, use the `golang:1.27.1-bookworm` image. The `Makefile` also has `build`, `test`, `race`, `vet`, `fuzz` and `ui-test` targets.

The browser test (Chromium through Playwright, in the prebuilt Playwright image) runs against a **throwaway** instance with empty volumes — it creates folders, files, links and the user `jan`. With an instance at `https://localhost:18443` and an administrator `admin`:

```sh
FILEDECK_URL=https://localhost:18443 FILEDECK_ADMIN=admin FILEDECK_PASSWORD='admin-password' make ui-test
```

A complete recipe for the throwaway instance is in [AGENTS.md](../AGENTS.md).

## Releases and images

The image is built and published to `ghcr.io/pbuzdygan/filedeck` **only when a release is published on GitHub** (`.github/workflows/release.yml`). The branches have separate channels that never mix:

| Branch | Release tag = image tag | Moving tag |
|---|---|---|
| `main` (production) | `X.Y.Z`, e.g. `1.0.0` | `latest` |
| `dev` | `devX.Y.Z`, e.g. `dev0.9.0` | `dev_latest` |

Images are multi-platform: `linux/amd64` and `linux/arm64` (cross-compiled in the Dockerfile, no emulation).

The workflow rejects a release if the tag has any other format, if the tagged commit is not on the channel's branch (e.g. `1.0.0` on a commit that exists only on `dev`), or if a `main` release is marked as a pre-release. It runs the tests before building. A published version is never overwritten, and `latest`/`dev_latest` move only for the highest version of the channel (a fix to an older version gets only its own number). The image carries a provenance attestation and an SBOM.

Releasing: GitHub → Releases → *Draft a new release* → a new tag (`1.0.0` targeting `main`, or `dev0.9.0` targeting `dev`, preferably as a pre-release) → *Publish release*. A private package needs `docker login ghcr.io` before `docker compose pull`.
