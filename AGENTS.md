# Filedeck — instructions for AI agents (Codex, Claude Code and others)

This file is the single source of project context for agents. Read it at the start of every session. Work status: `docs/PROGRESS.md` (section "Next stage").

## Project

- **Filedeck** (this repository, Go, Linux + Docker only) is a from-scratch, security-first successor to File Browser. Goal: close the bug classes behind the original's 62 GHSA advisories with a simpler design, not by porting code.
- `reference/filebrowser/` is an archived fork of the original — **read-only, never modify it**; it is excluded from git (`.gitignore`), so it may not exist in another clone.
- `docs/analysis/` — analysis of the original (functions, findings, design, advisory register).
- Filedeck directories are used at the same time by other applications and SMB/NFS shares: never overwrite, publish atomically, assume files change underneath.

## Language

- **All documentation is written in English only**: `README.md`, `CHANGELOG.md`, `AGENTS.md`, everything in `docs/`, code comments, commit messages, pull request descriptions and release notes. When you touch a document that still contains another language, translate it to English.
- Talk to the user **in Polish** (conversation only — never in files).
- The interface is **English by default**, with a switch to Polish. Every new or changed UI text must go into both files: `internal/web/static/lang-en.json` and `lang-pl.json` (the Polish translation is the only Polish text in the repository, apart from test data). Never hardcode text in `app.js`/`index.html`/`share.html`/`share.js` — use `t('key')` and `data-i18n*`. `TestTranslations` enforces completeness.

## Code rules

- **Icons:** Tabler Icons only, **offline**. Copy `icons/outline/<name>.svg` from `@tabler/icons` 3.48.0 (e.g. `https://cdn.jsdelivr.net/npm/@tabler/icons@3.48.0/icons/outline/<name>.svg`; check that the file contains only `<path>` elements) to `internal/web/static/ti-<name>.svg`, add an `.ic-<name>` rule in `app.css`, list the icon in `third_party/tabler-icons/README.md`, render it with `icon('<name>')`. No CDNs or internet resources at runtime.
- **Security:**
  - no `os/exec`, plugins or HTML templates (enforced by `internal/audit`);
  - CSP without `unsafe-*`, with Trusted Types;
  - file operations only through `internal/storage` (`openat2` with BENEATH, NO_SYMLINKS and NO_XDEV, and `RENAME_NOREPLACE`);
  - deletion goes to the trash;
  - the `.filedeck` directory is unreachable.
- **Changes:**
  - every feature gets Go tests;
  - when a contract changes, update `docs/CONTRACT.md`;
  - after a stage, add a section to `docs/PROGRESS.md`;
  - describe the effect on advisories in `docs/SECURITY.md`;
  - add a `CHANGELOG.md` entry under New Features, Improvements or Bug Fixes. Write it non-technically: what the user will see, use or notice.

## Environment (important — limited resources)

The server is an LXC with 6 GB RAM, 3 CPUs and **no swap**. `/tmp` lives in RAM and is wiped on reboot. Heavy tasks run in parallel (image build together with Playwright) have already caused a hard reset of the machine.

- The host has no `go`, `gcc` or `make`. Run tests in a container, from the repository root:
  ```sh
  docker run --rm --memory 2g --cpus 2 -u $(id -u):$(id -g) -v "$PWD":/src \
    -v ~/.cache/filedeck-go/mod:/go/pkg/mod -v ~/.cache/filedeck-go/build:/cache \
    -e GOCACHE=/cache -e GOFLAGS=-buildvcs=false -e HOME=/tmp -w /src \
    golang:1.27.1-bookworm sh -c 'test -z "$(gofmt -l cmd internal)" && go vet ./... && go test ./...'
  ```
  Add `-race` when you change concurrency.
- Image: `docker build --memory 2g -t filedeck:local .`
- Browser test:
  - script: `test/ui/smoke.mjs`, run with the `docker run` command from the `ui-test` target in the `Makefile` (the host has no `make`; it uses the prebuilt Playwright image with a 2 GB limit);
  - run it against a **separate, throwaway instance** (test directories in `~/.cache/filedeck-test`, never in `/tmp`);
  - proven recipe (without the host spaces of `compose.override.yaml`), from the repository root:
    ```sh
    docker build --memory 2g -t filedeck:test .
    T=~/.cache/filedeck-test/s1; mkdir -p $T/data $T/files
    printf '%s\n' 'correct horse battery' | docker run --rm -i -u $(id -u):$(id -g) --read-only -v $T/data:/data -v $T/files:/files filedeck:test bootstrap admin
    docker run -d --name filedeck-s1 --read-only --cap-drop ALL -u $(id -u):$(id -g) -v $T/data:/data -v $T/files:/files \
      -p 127.0.0.1:18443:8443 -e FILEDECK_ORIGIN=https://localhost:18443 -e FILEDECK_TLS_SELF_SIGNED=true filedeck:test
    docker run --rm --network host --ipc=host --memory 2g --cpus 2 -u $(id -u):$(id -g) -e HOME=/tmp -e PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 \
      -e FILEDECK_URL=https://localhost:18443 -e FILEDECK_ADMIN=admin -e 'FILEDECK_PASSWORD=correct horse battery' \
      -v "$PWD/test/ui:/w" -w /w mcr.microsoft.com/playwright:v1.56.0-noble sh -c 'npm init -y >/dev/null && npm i -s playwright@1.56.0 && node smoke.mjs'
    ```
    A full run ends with `NO CONSOLE/CSP PROBLEMS`;
  - afterwards remove the instance (`docker rm -f filedeck-s1`, directory `$T`, image `filedeck:test`) and `test/ui/node_modules`, `test/ui/out`, `package*.json`.
- **Never run an image build and Playwright at the same time.**

## Branches and releases

- Work happens on the `dev` branch; `main` is production. Commits and releases only when the user asks.
- Images are built only from a published GitHub release (`.github/workflows/release.yml`); the release tag equals the image tag: `X.Y.Z` (must be on `main`, not a pre-release) → `X.Y.Z` + `latest`; `devX.Y.Z` (must be on `dev`) → `devX.Y.Z` + `dev_latest`; images for `linux/amd64` and `linux/arm64` (cross-compiled in the Dockerfile, no emulation). The channels never mix; versions are never overwritten.
- `CHANGELOG.md`: new entries go under the topmost version heading (currently `0.1.0`) until that version is released; after a release, start a new heading for the next version above it.
- Never commit `.env`, `compose.override.yaml` (local server paths), `reference/`, test artifacts (`test/ui/node_modules`, `out`, `package*.json`) — `.gitignore` enforces this.

## The user's instance (production — do not break it)

- Runs from the repository root with `docker compose` (project `name: filedeck`, volumes `filedeck_*`), address `https://192.168.68.6:8443`.
- Configuration in `.env`: `FILEDECK_BIND`, `FILEDECK_ORIGIN`, `FILEDECK_USER=1001:1001`.
- Extra spaces are in `compose.override.yaml` (git-ignored) as `/spaces/<name>`, e.g. `docker_dev`. The own space is `/files`.
- Deploy after changes: `docker compose up -d --build`, then check `docker compose ps` (must be `healthy`).
- Never test on the user's volumes. Do not change `.env` without asking.
- The user has their own reverse proxy — do not add a Caddy profile.

## Status on 2026-09-28 (evening)

Stages 1–10 are done and deployed:

- core, accounts and sessions, resumable uploads;
- UI and Compose;
- spaces, permissions and trash;
- preview, editor, copy and move, theme;
- EN/PL, notifications, selection, folder upload;
- reauth fix, `selftest`, sorting and search;
- user deletion, "My files" tab first, file table with cut long names (stage 10);
- public links (stage 9);
- release workflow (`dev`/`main` channels, amd64 + arm64).

All Go tests pass, and the browser test passes in 25 steps (without a `nas` space; with it, the host-space steps are added).

Next steps (only after the user confirms):

1. The user will run `docker compose run --rm filedeck selftest /spaces/<name>` on a real SMB share. After the result, add a support statement. Mounting CIFS is impossible inside this LXC.
2. Deployment behind the user's reverse proxy (`FILEDECK_ORIGIN` also determines the address of public links).
