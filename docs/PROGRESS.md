# Progress

2026-09-28 — stage 1: core prototype; stage 2: accounts, sessions and HTTP API; stage 3: durable, resumable uploads; stage 4: web interface and Docker Compose deployment; stage 5: spaces, per-space permissions, rename and trash; stage 6: preview, editor, copy/move, drag & drop, theme, security review; stage 7: EN/PL, notifications, selection, folder upload; stage 8: reauth fix, share selftest, sorting, search; stage 9: public links; release preparation.

## Stage 1 — core

A new Go module, Linux storage, access policy, in-memory upload lifecycle, a local CLI, a Docker image and a CI configuration for the future Filedeck repository. The `reference` checkout remains comparison material and was not modified.

| Contract | Main tests |
|---|---|
| Path boundary and no symlinks | `TestPathContract`, `TestReadRejectsSymlinksAndFIFO`, `TestConcurrentDirectorySymlinkSwapCannotReadOutside`, `FuzzValidPath` |
| Bounded listing | `TestListIsBoundedAndOmitsSpecialEntries` |
| Private state and one instance per state | `TestOpenRejectsUnsafeStateAndConcurrentInstance` |
| Safe recovery after a restart | `TestRecoveryOnlyDeletesOwnedStagingNames`, `TestRecoveryRefusesSymlinkInsteadOfFollowingIt` |
| No loss of existing data | `TestPublishNeverOverwritesAnyExistingEntry`, `TestFailedUploadCannotDeleteOrTruncateDestination` |
| Atomic publication and conflicts | `TestUploadPublishesOnlyAfterCompletion`, `TestConcurrentCommitsNeverOverwrite` |
| Chunk serialization | `TestConcurrentPatchAtSameOffset` |
| Limits, errors and cancellation | `TestFailedAndOversizedChunksRollBack`, `TestChunkCapIndependentOfFileSize`, `TestCancellationRollsBack`, `TestQuotasExpiryAndAbort` |
| Ownership and access revocation | `TestOwnershipAndRevocation` |
| Checks before publication | `TestCommitChecksActualStagingLength`, `TestExpiredCommitCleansOnlyStaging`, `TestCommitRejectsSymlinkParentAndAllowsEmptyFile` |
| CLI | `TestOperatorCLI` |

Run: Go tests, `go vet`, race-detector tests in the Go image, path fuzzing (181,880 executions in a 10 s session) and `govulncheck` v1.8.0 — no vulnerabilities found in the scanned code. The race detector covers memory races; a separate test swaps a directory with a symlink during reads. No single test proves complete resistance to all filesystem races.

Built the `filedeck:prototype` image and ran a CLI test in a container without network, capabilities or writes to the image layer: upload, read, listing, data preserved on conflict, staging emptied, and refusal of a configuration with separate root/state bind mounts. The CI workflow was prepared but not run in GitHub Actions.

## What these tests address in the GHSA register

- Upload cleanup never receives the target path: class GHSA-c4fr-5f24-4wrj and GHSA-fmm7-x4gx-8jhr.
- Chunks have a limit and are serialized: classes GHSA-ffv3-7h97-993q and GHSA-4r8p-gqj2-mwgm. This is not yet an implementation of the TUS protocol.
- Symlinks are not an allowed access path: classes GHSA-239w-m3h6-ch8v, GHSA-8wc8-hf36-mjh9 and GHSA-7w29-q235-57m9, within the explicitly described handle model.
- Reading rejects FIFOs before opening the data. Archiving, where GHSA-8q5j-8wcr-8v2v comes from, is not implemented yet and will need its own tests.

We do not mark all 62 advisories as closed. The absence of sessions, shares or renderers in the prototype is not proof of the security of their future implementations.

## Stage 2 — accounts, sessions and HTTP API

Packages `internal/identity` (accounts, Argon2id passwords, server-side sessions in bbolt) and `internal/api` (HTTP adapter), commands `bootstrap`, `reset-password` and `serve`. Details: section "Accounts, sessions and the HTTP API" in [CONTRACT.md](CONTRACT.md).

| Contract | Main tests |
|---|---|
| Bootstrap only once, password and session hashes instead of secrets in the database, persistence across restarts | `TestBootstrapPersistenceAndSecretStorage`, `TestHTTPSTransportAndRestartedIdentityStore` |
| Password change/reset, disable and permission change revoke sessions | `TestPasswordChangeRevokesAllSessions`, `TestPasswordChangeAndAdminDisableRevokeSessions`, `TestAdministrativeResetAndPermissionsInvalidateOldCookies` |
| Last administrator, role and reauth | `TestAccountDisablePermissionsAndLastAdministrator`, `TestAdminRequiresRoleAndReauthentication` |
| Session expiry and limits, bounded login work | `TestIdleAndAbsoluteExpiry`, `TestLoginHasBoundedSessionsAndWork` |
| Private database, symlink and public permissions rejected | `TestDatabaseSymlinkAndPublicPermissionsRejected` |
| Cookie, logout, replayed session rejected | `TestLoginCookieLogoutAndReplayedSession` |
| CSRF, `Origin`, `Host`, `Sec-Fetch-Site` | `TestCSRFOriginAndHostBoundaries` |
| JSON limits and strictness, single path decoding, ambiguous cookies | `TestJSONLimitsAndUnknownFields`, `TestPathDecodingAndCookieAmbiguity` |
| Upload/download/Range/conflict over HTTP | `TestUploadDownloadRangeAndConflict` |
| Proxy and ignored forwarded headers, login limit | `TestProxyBoundaryAndForwardedHeadersIgnored`, `TestLoginRateCannotUseForwardedIPToBypassLimit`, `TestRateLimitAndBoundedBookkeeping` |
| Logout during an upload blocks publication; upload IDs are private | `TestLogoutDuringUploadPreventsPublication`, `TestUploadIDsRemainPrivateBetweenWriters` |

Verification: `go test` and `go vet` locally; `go test -race` in the `golang:1.27.1-bookworm` image without network — all packages pass. `govulncheck` v1.8.0: the code calls no known vulnerability; at module level it reported GO-2026-5932 (`x/crypto/openpgp`, no fix), a package Filedeck does not import — only `argon2` is used from `x/crypto`. End-to-end test of the built binary in `-insecure-local` mode: bootstrap, wrong password → 401, missing CSRF → 403, missing `Origin` → 403, upload/commit/listing/download with `Content-Disposition: attachment` and CSP, traversal `../state/identity.db` → 400, logout, replayed cookie → 401, foreign `Host` → 421. The server in a container with TLS and behind a real reverse proxy was not tested yet.

### What stage 2 addresses in the GHSA register

The mechanisms and tests of this stage correspond to the classes in the "Accounts and sessions" section of the [advisory register](analysis/05-advisories.md):

- Server-side sessions with revocation: GHSA-7xwp-2cpp-p8r7 (replay after logout), GHSA-v7vv-5wj2-gfcj (password reset does not revoke sessions), GHSA-v3jv-rmh2-635j (expired JWT with proxy auth).
- No proxy header auth and no auto-provisioning: GHSA-xqp3-jq6g-x3qm, GHSA-j7jh-37pf-mf8h, GHSA-7526-j432-6ppp.
- No self-registration, explicit permissions when creating accounts, no command execution permission: GHSA-6759-996p-gpj6, GHSA-5gg9-5g7w-hm73, GHSA-x8jc-jvqm-pm3f, GHSA-576v-w77m-gr84 (lowercase names only, no home directories derived from names).
- A password change requires the current password and is versioned: GHSA-hxw8-4h9j-hq2r, GHSA-cm2r-rg7r-p7gg.
- A dummy hash for non-existent accounts: GHSA-43mm-m3h2-3prc (a mitigation, not formal proof of no timing channel).
- Login limit and the Argon2 computation gate: GHSA-w5fm-68j4-fpc4.

The final status of every advisory is in [SECURITY.md](SECURITY.md); some items (e.g. timing) need a separate measurement.

## Known trade-offs and open points

- Every authenticated request writes `LastSeen` with fsync — a simple and consistent revocation model, but a performance limit. To consider: writing every N seconds.
- The Argon2 computation for administrator reauth runs under the exclusive `security` lock (briefly pausing other users' commits).
- `Begin` writes the record (fsync) under the global service lock, and every chunk needs two fsyncs — simplicity and correctness at the cost of throughput with many parallel uploads.
- If updating the in-memory policy fails after an account change was saved, the API returns an error and the change stays in the database; the policy is rebuilt from the database after a restart.
- Behind a reverse proxy the login limit is shared by the proxy's address.

## Stage 3 — durable, resumable uploads

Upload records in `uploads.db` in the state directory, recovery after a restart and a crash, an idempotent commit and publication result status. `Close` keeps uploads; the CLI `put` cancels its own failed upload. Details and the recovery table: sections "Upload lifecycle" and "Process crash and resources" in [CONTRACT.md](CONTRACT.md).

| Contract | Main tests |
|---|---|
| Resume after a restart, keeping the owner and the reservation | `TestResumeAfterRestart`, `TestPatchIsDurableOnlyAfterRecord` |
| Bytes without a stored offset are cut off; missing confirmed bytes invalidate the upload | `TestRecoveryTruncatesUnacknowledgedBytes`, `TestRecoveryDropsUploadWithMissingAcknowledgedBytes` |
| Crash during publication: before and after `rename` | `TestCrashBeforeRenameRevertsToUploading`, `TestCrashAfterRenameIsReportedAsPublished` |
| Idempotent commit, privacy and expiry of results, bounded number of results | `TestPublicationResultSurvivesRestartAndExpires`, `TestResultsAreBounded`, `TestUploadPublishesOnlyAfterCompletion` |
| Corrupt records, records pointing outside the space or to foreign staging; orphaned and expired staging | `TestRecoveryDiscardsOrphansCorruptAndExpiredRecords` |
| The database cannot be a symlink | `TestDatabaseSymlinkIsRejected` |
| HTTP: resume after restarting the whole process (same cookie), double commit, no cancellation after publication | `TestUploadResumesAcrossRestartAndCommitIsIdempotent` |
| The CLI leaves no staging after a conflict | `TestOperatorCLI` |

Verification: `go test`, `go vet`, `gofmt`; `go test -race -count=3` in `golang:1.27.1-bookworm` without network; path fuzzing for 10 s. End-to-end test of the binary: upload 5/10 B, **`kill -9` of the server**, restart, status `GET` with the same cookie → offset 5, completion, commit twice → both times `published: true, durability_confirmed: true`, status `published`, only the databases remain in the state. Crashes in the middle of operations are tested by recreating the disk state (appended bytes, a `publishing` record with/without staging) — not by actually cutting the power; that would need tests on a virtual machine with disk cache loss.

### What stage 3 addresses

The GHSA register has no separate reports about data loss after a restart; this stage instead closes risks from the design analysis: repeating a request after a lost response causes no second publication and no conflict mistaken for an error, a crash leaves no partial file under the target name, and cleanup after a restart still touches nothing but recognised staging without a record (the GHSA-c4fr-5f24-4wrj / GHSA-fmm7-x4gx-8jhr class is preserved).

## Stage 4 — web interface and Docker Compose

Package `internal/web` (HTML/CSS/JS embedded in the binary, no framework and no build step), endpoint `POST /api/folders`, upload limits in `/api/auth/me`, configuration through `FILEDECK_*`, `-tls-self-signed` mode, `compose.yaml` + `.env.example`, an image with a ready `/data` layout. Details: sections "Web interface" and "Docker deployment" in [CONTRACT.md](CONTRACT.md).

Interface: login, folder navigation with breadcrumbs and the address in `#/path`, download, folder creation, multi-file upload (picker or drag) with progress, resume and retry, password change, administrator panel (creating accounts, permissions, disabling, password reset — every change with the password re-entered). Write controls are hidden for accounts without Create; the server rejects such requests anyway.

| Contract | Test |
|---|---|
| Page headers (CSP without `unsafe`, Trusted Types), no access to files outside the asset list, cross-site only for the UI, limits in `/me`, folders over HTTP with CSRF and permissions | `TestInterfaceHeadersAndFolders` |
| `mkdirat` without symlinks, without creating intermediate directories, without replacing, requires Create | `TestMkdirIsBoundedAndNeverReplaces` |
| Self-signed certificate: reuse, bound to host/IP, mode 0600, refused for `http` | `TestSelfSignedCertificateIsReusedAndBoundToHost` |
| End-to-end browser test (Chromium) against a compose container | `test/ui/smoke.mjs` |

Verification: Go tests, `go vet`, `gofmt`, `go test -race -count=2` and fuzzing in `golang:1.27.1-bookworm`; `node --check` for the JS. The `filedeck:local` image is 11.8 MB. Docker Compose scenarios on separate projects (removed after the test):

- named volume: starting without an administrator ends with instructions, `docker compose run --rm -T filedeck bootstrap admin`, start with a self-signed certificate, HTTPS, `__Host-filedeck` cookie, foreign `Host` → 421;
- bind mount of an existing host directory with `FILEDECK_USER` = host UID: existing files visible, new ones created with the host owner, state 0600;
- Chromium (Playwright): wrong password, login, folder creation, 20 MiB upload in 3 chunks, a file named `<img src=x onerror=alert(1)>.txt` shown as text (no injected HTML and no dialog), download of 20,971,520 B, conflict without overwrite, account creation requiring reauth, read-only account without write controls, reload keeping the session and folder, logout — **zero console errors and CSP violations**.

Not tested: Firefox/Safari, mobile devices, a real reverse proxy, resuming an upload after a page reload in the browser (the logic exists; the automated test covers resuming at the API level).

**Fix after the first deployment (2026-09-28):** the first start through `docker compose run … bootstrap` turned out to be fragile — a container without an administrator restarted in a loop and locked the state, and the port listened only on `127.0.0.1` by default. Added a setup mode with a one-time code in the log (`TestFirstRunSetupCodeCreatesAdministratorOnce`, a Chromium test of the setup screen: a wrong code rejected, the correct one creates the account and signs in, no setup screen after a reload) and clearer instructions for LAN access (`FILEDECK_BIND`, `FILEDECK_ORIGIN`, `https://`).

### What stage 4 addresses in the GHSA register

The XSS and active content class (section "Input/output limits… isolation of active content"): the interface has no preview or rendering of files, content always goes out as an attachment with `sandbox`, and file names cannot become HTML thanks to Trusted Types.

## Stage 5 — spaces, rename and trash

Decisions (2026-09-28): the server mounts shares itself and Filedeck receives directories through compose mappings; next to them, the container's own "My files" space; deletion goes to the trash. Details: sections "Spaces and the `.filedeck` directory" and "Rename and trash" in [CONTRACT.md](CONTRACT.md).

Architecture change: staging moved from the state into the hidden `.filedeck/` of each space (publication and trash must be a single `rename` on the same filesystem, and spaces live on different mounts). The "state and files on one mount" requirement is gone. The state holds only the databases and the certificate. Added: multiple spaces (`/files` + detected `/spaces/*`), read-only spaces, per-space permissions with `*`, the Modify permission, rename, trash with restore and retention, permanent deletion by administrators only, modes of new files/folders (`0640`/`0750`), `healthcheck` and `HEALTHCHECK` in the image, `compose.override.example.yaml`, a log of detected spaces, migration of accounts and records from the older format.

| Contract | Test |
|---|---|
| `.filedeck` hidden and unreachable — names, letter-case variants, trailing dots, Kelvin sign, alias by inode | `TestPathContract`, `TestMetadataDirectoryIsHiddenAndUnreachable` |
| Rejecting a world-writable `.filedeck`, a symlinked one, a root symlink, wrong modes | `TestUnsafeMetadataDirectoryIsRejected` |
| Read-only space | `TestReadOnlySpace`, `TestReadOnlySpaceAndStatePlacement` |
| State private, locked, disjoint from spaces, cleanup of old staging | `TestStateIsPrivateLockedAndDisjoint` |
| Modes of new folders, private `.filedeck` | `TestMkdirUsesConfiguredMode` |
| Rename without overwriting, without escaping through a symlink, without entering `.filedeck`, without symlinks as the source | `TestRenameNeverReplacesOrEscapes`, `TestRenameRequiresModify` |
| Trash, restore without overwriting, permanent deletion without following symlinks | `TestTrashRestoreAndPurge`, `TestTrashLifecycle` |
| Trash recovery after a crash (record without item, item without record), retention | `TestTrashRecoveryAndRetention` |
| Per-space permissions, `*`, path isolation, revoking Create blocks publication | `TestPermissionsArePerSpace` |
| Migration of the old permission format | `TestLegacyPermissionsBecomeAllSpacesGrant` |
| HTTP: spaces, rename, trash, purge by administrators only, grant validation | `TestSpacesRenameAndTrashOverHTTP` |
| `/healthz` from loopback only; space discovery | `TestHealthzIsLoopbackOnly`, `TestDiscoverSpaces` |

Verification: Go tests, `go vet`, `gofmt`; `go test -race -count=3` and fuzzing in `golang:1.27.1-bookworm` (the race detector found an unsynchronized read of the upload's space in `Patch`/`Status`, now fixed). Compose with two host spaces (`nas` writable — a directory owned by the container UID, `archiwum` as `:ro`) and Chromium: folders, 20 MiB upload, XSS trap name, download, conflict, rename, trash → restore, trash → permanent deletion, upload into `nas` (file on the host with mode `0640`, `.filedeck` `0700`, invisible in the UI), `archiwum` marked and without write controls, an account with grants only for "My files" and `nas` (does not see `archiwum`, no write or delete controls), healthcheck `healthy` — **zero console and CSP errors**. The test found a UX bug (the alphabetically first space opened by default, here a read-only one) — fixed.

Not tested: a real SMB/CIFS and NFS share, a Samba server with 8.3 names, very large trees in the trash.

**Fix after deployment (2026-09-28):** changing `FILEDECK_USER` with new volumes ended with `permission denied`, because Docker copied directories from the image owned by UID 65532 into the volumes. Now the image contains only empty `1777` mount points, and Filedeck itself creates the state and its own space as the actual UID (`TestFirstStartCreatesPrivateDirectories`; checked in compose for UID 1001 and 65532).

### What stage 5 addresses in the GHSA register

- Deletion without permission / through path bugs: rename and trash require a separate Modify, go through `RENAME_NOREPLACE` and the same path checks as everything else; permanent deletion only from the trash and only by administrators (class GHSA-c4fr-5f24-4wrj / GHSA-fmm7-x4gx-8jhr extended to the new operations).
- Symlinks in tree operations: symlink sources rejected, purge does not follow symlinks or cross mounts (classes GHSA-239w-m3h6-ch8v, GHSA-8wc8-hf36-mjh9, GHSA-7w29-q235-57m9).
- Name aliasing on case-insensitive systems (class GHSA-576v-w77m-gr84): the metadata directory is protected by name with case-folding and by inode.
- User scope: no "scope = server root"; every space requires an explicit grant, a new space is not shared with anyone except through `*` (class GHSA-6759-996p-gpj6, GHSA-j7jh-37pf-mf8h).

## Stage 6 — preview, editor, copy, theme and security review

Done: drag & drop onto the whole window and onto a folder row (the browser no longer opens dropped files); preview of photos, video, audio, PDF and text with ←/→ navigation; a text editor (Ctrl+S, unsaved-changes marker, version conflict → "save as", previous version in the trash); "New file"; background copy and move between spaces with progress and cancellation; an auto/light/dark theme switch with a new light palette; Tabler icons (offline). Details: section "Preview, editor and copy" in [CONTRACT.md](CONTRACT.md).

**Security review:** all 62 File Browser advisories assigned a status with evidence — [SECURITY.md](SECURITY.md): 38 addressed with tests, 23 not applicable (missing features: sharing, commands/hooks, archives), 1 partial (account enumeration through timing). Found and fixed: a memory limit for editor saves, a free-space reserve (upload/save/copy), early rejection of targets in `.filedeck`. New regression tests: `TestAdvisoryRegressions`, `TestUsernamesCannotCollideByCaseOrUnicode`, `TestFreeSpaceReserve`, `TestNoProcessExecutionOrPlugins` (no `os/exec`, plugins, templates).

| Contract | Test |
|---|---|
| Text save: version, conflict, previous version in the trash, mode, permissions | `TestTextEditorSavesSafely` |
| Rejecting binaries, invalid UTF-8, oversized files, symlinks, directories, `.filedeck` | `TestTextEditorRejectsUnsuitableFiles` |
| Preview: types, headers, sandboxed SVG, HTML not inline, Range | `TestPreviewAndTextEditorOverHTTP` |
| Tree copy: skipping symlinks/FIFOs, modes, limits, cancellation, cleanup | `TestCopyTreeBetweenSpaces` |
| Copy/move between spaces, permissions, job isolation | `TestCopyAndMoveBetweenSpaces`, `TestTransfersOverHTTP` |

Verification: Go tests, `go vet`, `staticcheck` (clean), `govulncheck`, race detector ×3, fuzzing; Chromium: theme (remembered after reload), drag & drop onto the window and a folder, preview of an SVG with a script and of text, edit + conflict → save as a copy, copy and move to a host space — no console or CSP errors. Deployed on the user's instance.

## Stage 7 — language, notifications, selection, folders

Based on the user's testing:
- **Folder upload** — an "Upload folder" button and dragging a folder; the subfolder structure is recreated level by level (existing folders are reused), a limit of 10,000 files at once.
- **Notifications instead of sections under the table** — short messages disappearing after 4–8 s (max. 3 at a time, so they do not cover the page) and a bell in the top bar with a history of the last 40 operations (upload, copy/move, rename, trash, restore, save, folders) with status and progress. Jobs finished before a page reload go into the history without a repeated message.
- **Language** — English by default, EN/PL switch (remembered); dictionaries `lang-en.json`/`lang-pl.json`, `TestTranslations` enforces completeness (keys, placeholders, keys used in HTML/JS, API error codes — the test immediately found 3 untranslated codes).
- **Row actions as icons with tooltips**; rename has a text-field icon (`forms`).
- **Selection** — a selection mode button, checkboxes, "select all", Ctrl/Cmd+click, Shift+click (range), Esc; a bulk action bar: copy/move (to a target folder) and trash. The server accepts one job with a list of up to 1000 source→target pairs (`TestMultiItemTransfer`).

Verification: Go tests with the race detector, `TestTranslations`; Chromium (22 steps, including English by default, the history under the bell, max. 3 messages, uploading a folder tree, selection with Ctrl, bulk move, bulk trash, switching to PL and remembering it) — no console or CSP errors. Deployed.

## Stage 8 — reauth fix, share test, sorting and search

- **Bug:** a wrong administrator password when creating an account (and a wrong current password when changing a password) returned 401, which the interface treated as an expired session — the user saw the login screen although the session was valid. Now 403 `wrong_password` with a "Wrong password" message (`TestWrongPasswordKeepsSession`, a step in the browser test).
- **SMB test:** an attempt with a Samba server in a container and the kernel CIFS client failed because of the environment — on this host (LXC) CIFS mounts are not allowed even for a privileged container. Instead, the **`filedeck selftest DIR`** subcommand checks all required properties on any mounted directory (`TestSelftestPassesOnLocalDirectory`). Conclusion for LXC deployments: the share must be mounted on the virtualization host and passed in as a directory.
- **Browsing convenience:** sorting by name, size and modification date (remembered, `aria-sort`), a date column, downloading selected files one after another (no ZIP archives — see SECURITY.md).
- **Search** by name below the current folder with limits; a result opens the file's folder and preview (`TestSearchIsBoundedAndStaysInside`, `TestSearchOverHTTP`).

Verification: Go tests (in the `golang` container with memory limits — a hard reset of the server interrupted the previous run), Chromium 24 steps without console or CSP errors (prebuilt Playwright image, 2 GB limit). Deployed.

## Stage 9 — public links

Sharing a file or folder with people without an account: a "Share link" icon in the row, a choice of validity (1 hour – 1 year) and an optional password, the link shown once with a copy button; a "Shared links" view with the list, state (active / not working) and revocation, and every user's links for administrators. Public page: a file with a download button or a folder with navigation and downloads, a password screen, a message for an unavailable link, EN/PL and theme. Details: section "Public links" in [CONTRACT.md](CONTRACT.md).

Model derived from the 12 sharing advisories: a link points to an **object** (device, inode, birth time), not a path; every request checks expiry, the owner's permissions and the object identity; revoking permissions deletes links; the token is stored only as a hash, the password as Argon2id, and it never returns in the API. All 12 entries in [SECURITY.md](SECURITY.md) changed status from "not applicable" to "addressed" (in total 50 ✅, 11 🚫, 1 ⚠️).

| Contract | Test |
|---|---|
| Object identity: rename, replacement, symlink under the old name, `.filedeck` | `TestObjectIdentityFollowsTheObjectNotThePath` |
| Folder: reads only below the handle, symlinks, `..`, a moved `.filedeck` | `TestDirectoryObjectStaysBeneathItself` |
| Validation, permissions, password, ownership, no secrets in the database | `TestPublicLinks` |
| Rename, trash, losing List, account disable, expiry | `TestPublicLinkStopsWorking` |
| HTTP: page, download, traversal, password and cookie, list without secrets, revocation, origin boundaries | `TestPublicLinksOverHTTP` |
| Password attempt limit | `TestLinkPasswordAttemptsAreLimited` |
| A download longer than the request limit | `TestSlowDownloadIsNotCutOff` |

**Bug found along the way:** every download (also for signed-in users) was interrupted after about 60 s by a fixed write deadline — a large file on a slower connection broke off. Now the deadline moves forward with every chunk sent. The test reproduces the bug (without the fix it cuts 4 MiB off at ~3.9 MiB).

Verification: Go tests with the race detector, `TestTranslations` (extended to the public page and all API files), Chromium 23 steps on an instance without host spaces (new: a password-protected folder and a file without a password opened without an account, download, revocation, rename ends the link, state in the list) — no console or CSP errors. Also fixed two flaky test steps (search racing with navigation, a message from a previous upload) and the row layout in the trash. Tabler icons: `share` (share), `link` (link list), `link-off` (revoke), `lock` (password).

## Release preparation

The local server configuration (a space from a host directory) moved from `compose.yaml` to the git-ignored `compose.override.yaml` — the effective configuration of the instance is unchanged (compared with `docker compose config`). `.gitignore` covers `.env`, the override, `reference/`, the binary, test artifacts and editor files; `.dockerignore` lets only the code into the build. Workflow `release.yml`: images only from a published release, channels `main` (tag `X.Y.Z` → image `X.Y.Z`, `latest`) and `dev` (tag `devX.Y.Z` → image `devX.Y.Z`, `dev_latest`), images for `linux/amd64` and `linux/arm64` through cross-compilation (checked locally: image and binary architecture), with checks of the branch, format, pre-release, no overwriting of versions and moving tags only moving forward; the planning logic was checked by a simulation on a local repository (correct releases, a tag from another branch, a wrong format, a pre-release on `main`, an older version). The workflow has not run in GitHub Actions yet. All documentation is now in English only (see `AGENTS.md`).

## Known limitations

- Public links are read-only (no uploads through a link); search by name only (not by content); the operation history lives in the browser tab (not on the server).
- `.filedeck` is visible to SMB users of the same space (`veto files` recommended).
- The trash takes space until the end of retention; there is no limit on its size.
- Every authenticated request writes `LastSeen` with fsync; `Begin` writes the record under a global lock.

## Next stage (proposal)

1. `selftest` on the user's real SMB share — then a support statement.
2. Deployment behind the target reverse proxy (no separate Caddy profile — the user's decision); `FILEDECK_ORIGIN` also determines the address of public links.

The prototype is a foundation for further implementation, not a ready replacement for a production File Browser.
