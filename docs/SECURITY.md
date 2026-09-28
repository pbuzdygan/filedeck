# Filedeck security — mapping of File Browser advisories

Status on 2026-09-29. Source of the list: [register of 62 advisories](analysis/05-advisories.md) (GitHub Security Advisories of the File Browser project). For each report: the problem class, how Filedeck eliminates it, and how that is verified.

Statuses:
- ✅ **addressed** — a mechanism in the code and an automated test (test names in the "Evidence" column);
- 🚫 **not applicable** — Filedeck does not have the feature; if it is added, the entry goes back to analysis;
- ⚠️ **partial** — the risk is mitigated without full evidence.

**Summary: 50 ✅, 11 🚫, 1 ⚠️.**

## Paths and authorization

| Advisory | Class | Status | Mechanism | Evidence |
|---|---|---|---|---|
| GHSA-7w29-q235-57m9 | symlink bypasses rules | ✅ | `openat2` with `RESOLVE_BENEATH\|NO_SYMLINKS\|NO_XDEV`; symlinks are never resolved; no deny rules to bypass | `TestReadRejectsSymlinksAndFIFO`, `TestConcurrentDirectorySymlinkSwapCannotReadOutside` |
| GHSA-8q5j-8wcr-8v2v | FIFO blocks downloads | ✅ | type checked through `O_PATH` before opening the data; copy skips special files; no archives | `TestReadRejectsSymlinksAndFIFO`, `TestCopyTreeBetweenSpaces` |
| GHSA-77x8-73f4-5485 | recursive operations ignore child rules | ✅ | permissions are per space, with no rules for sub-paths — nothing to bypass | `TestPermissionsArePerSpace`, `TestRenameRequiresModify` |
| GHSA-7whw-q6gh-xr59 | checksum disclosure without download permission | ✅ | no checksum endpoint; every content read (`content`, `text`, `preview`) requires Read | `TestAdvisoryRegressions` |
| GHSA-fgm5-pw99-w2p7 | letter-case variants and `\` | ✅ | `\` rejected; no path normalisation; `.filedeck` protected regardless of letter case and by inode | `TestPathContract`, `TestMetadataDirectoryIsHiddenAndUnreachable`, `TestAdvisoryRegressions` |
| GHSA-83xp-526h-j3ww | zip-slip in archives | 🚫 | no archive building; copy creates entries relative to descriptors from `readdir` names | — |
| GHSA-8wc8-hf36-mjh9 | write through a dangling symlink | ✅ | every creation: `O_EXCL\|O_NOFOLLOW` or `renameat2(RENAME_NOREPLACE)` — a symlink is an existing entry → conflict | `TestPublishNeverOverwritesAnyExistingEntry`, `TestRenameNeverReplacesOrEscapes` |
| GHSA-gxjx-7m74-hcq8 | traversal in zip/tar through `\` | 🚫 | no archives | — |
| GHSA-239w-m3h6-ch8v | symlinked directories outside the scope | ✅ | as GHSA-7w29; rename and trash do not go through symlinks; purge does not follow symlinks | `TestRenameNeverReplacesOrEscapes`, `TestTrashRestoreAndPurge` |
| GHSA-67cg-cpj7-qgc9 | text file content without download permission | ✅ | `/api/text` requires Read | `TestAdvisoryRegressions`, `TestPreviewAndTextEditorOverHTTP` |
| GHSA-5q48-q4fm-g3m6 | `HasPrefix` without a separator | ✅ | authorization does not compare path prefixes (a space is a separate handle); the only comparison (state vs space) uses a separator | `TestStateIsPrivateLockedAndDisjoint` |
| GHSA-9f3r-2vgw-m8xp | traversal in the copy/rename target | ✅ | `ValidUserPath` for source and target before doing any work | `TestAdvisoryRegressions`, `TestCopyAndMoveBetweenSpaces` |
| GHSA-4mh3-h929-w968 | multiple slashes in the URL | ✅ | the path only in the `path` parameter, decoded once; absolute paths and `//` rejected | `TestAdvisoryRegressions`, `TestPathDecodingAndCookieAmbiguity` |

## Upload and write consistency

| Advisory | Class | Status | Mechanism | Evidence |
|---|---|---|---|---|
| GHSA-c4fr-5f24-4wrj | cleanup deletes directories | ✅ | cleanup removes only its own recognised staging name; never the target | `TestFailedUploadCannotDeleteOrTruncateDestination`, `TestRecoveryOnlyDeletesOwnedStagingNames` |
| GHSA-4r8p-gqj2-mwgm | parallel chunks beyond the length | ✅ | upload lock, offset and limit checked under it | `TestConcurrentPatchAtSameOffset` |
| GHSA-m9f5-2232-frp6 | deletion through a symlink in the cache | ✅ | staging in the private `.filedeck/staging`; a symlink/hardlink stops recovery | `TestRecoveryRefusesSymlinkInsteadOfFollowingIt` |
| GHSA-ffv3-7h97-993q | declared length ignored | ✅ | file, chunk and reservation limits; free-space reserve | `TestFailedAndOversizedChunksRollBack`, `TestChunkCapIndependentOfFileSize`, `TestFreeSpaceReserve` |
| GHSA-fmm7-x4gx-8jhr | `RemoveAll` through a symlink | ✅ | as GHSA-c4fr; permanent deletion only in the trash, without following symlinks | `TestTrashRestoreAndPurge` |
| GHSA-ffx7-75gc-jg7c | negative length | ✅ | size < 0 → error; no hooks | `TestFailedUploadCannotDeleteOrTruncateDestination` |
| GHSA-79pf-vx4x-7jmm | deleting an upload without permission | ✅ | only one's own upload can be cancelled, and only its staging file | `TestOwnershipAndRevocation`, `TestUploadIDsRemainPrivateBetweenWriters` |

## Sharing (public links)

A link points to an object — device, inode and birth time — not to a path or its prefix. Every request checks again: expiry, the owner's current permissions (Read, and List for a folder) and the identity of the object at the stored path. The token (256 bits) is stored only as SHA-256, the link password as Argon2id.

| Advisory | Class | Status | Mechanism | Evidence |
|---|---|---|---|---|
| GHSA-r6pg-pg54-rcr5 | deleting a file leaves a working link | ✅ | another object or no object at the path → the link does not work; the trash moves the object | `TestPublicLinkStopsWorking` |
| GHSA-m8v4-4w34-rrvf | renaming leaves a working link | ✅ | a rename (also over SMB or by another application) invalidates the link; a new file under the old name is a different object | `TestObjectIdentityFollowsTheObjectNotThePath`, `TestPublicLinkStopsWorking`, browser test |
| GHSA-833g-cqhp-h72j | API returns the password hash and a bypass token | ✅ | the link list contains no token, salt or digest; the token is shown only once, at creation | `TestPublicLinksOverHTTP`, `TestPublicLinks` |
| GHSA-pp88-jhwj-5qh5 | deleting with a trailing `/` leaves a link | ✅ | no textual path matching; paths with a trailing `/` are rejected | `TestPathContract`, `TestPublicLinkStopsWorking` |
| GHSA-3q2p-72cj-682c | a link to a non-existent path takes over a future file | ✅ | a link can be created only for an existing object (identified at creation) | `TestPublicLinks`, `TestPublicLinksOverHTTP` |
| GHSA-5ww9-jg6q-38r7 | deleting others' links through a path prefix | ✅ | no prefix operations; revocation by link ID, only by the owner or an administrator | `TestPublicLinks`, `TestPublicLinksOverHTTP` |
| GHSA-j9jx-hp4c-ghhh | access rules "rebased" in a folder link | ✅ | no rules for sub-paths; reads through the folder handle with `RESOLVE_BENEATH\|NO_SYMLINKS\|NO_XDEV` | `TestDirectoryObjectStaysBeneathItself` |
| GHSA-v9w4-gm2x-6rvf | a link works after permissions are revoked | ✅ | the owner's permissions are checked on every request; taking Read/List away or disabling the account deletes their links for good | `TestPublicLinkStopsWorking`, `TestPublicLinksOverHTTP` |
| GHSA-68j5-4m99-w9w9 | download through a link bypasses the policy | ✅ | the same policy as for the owner, checked on every request; read-only | `TestPublicLinksOverHTTP` |
| GHSA-mr74-928f-rw69 | traversal outside the shared folder | ✅ | path relative to the folder handle; `..`, symlinks, mounts and `.filedeck` rejected (also by inode) | `TestDirectoryObjectStaysBeneathItself`, `TestPublicLinksOverHTTP` |
| GHSA-6cqf-cfhv-659g | IDOR in link deletion | ✅ | the owner is checked on revocation; the list shows only one's own links (administrator: all, explicitly) | `TestPublicLinks`, `TestPublicLinksOverHTTP` |
| GHSA-3v48-283x-f2w4 | link password bypass | ✅ | before unlocking only `needs_password` is available; unlocking = a `__Host-` cookie with an HMAC bound to the link ID and time (key only in memory), attempt limit per address | `TestPublicLinksOverHTTP`, `TestLinkPasswordAttemptsAreLimited` |

## Accounts and sessions

| Advisory | Class | Status | Mechanism | Evidence |
|---|---|---|---|---|
| GHSA-v3jv-rmh2-635j | expired JWT with proxy auth | ✅ | no JWT and no proxy auth; server-side sessions with idle and absolute expiry | `TestIdleAndAbsoluteExpiry`, `TestProxyBoundaryAndForwardedHeadersIgnored` |
| GHSA-576v-w77m-gr84 | name collision through letter case | ✅ | names only `[a-z0-9._-]`, no normalisation; no home directories derived from names | `TestUsernamesCannotCollideByCaseOrUnicode` |
| GHSA-j7jh-37pf-mf8h | auto-provisioning with root scope | ✅ | no auto-provisioning; a new account without grants sees no space | `TestPermissionsArePerSpace` |
| GHSA-7rc3-g7h6-22m7 | collision after name normalisation | ✅ | as GHSA-576v | `TestUsernamesCannotCollideByCaseOrUnicode` |
| GHSA-6759-996p-gpj6 | self-registration with root scope | ✅ | no self-registration; access only through explicit grants | `TestFirstRunSetupCodeCreatesAdministratorOnce`, `TestPermissionsArePerSpace` |
| GHSA-v7vv-5wj2-gfcj | password reset does not revoke sessions | ✅ | account version in the session; password/permission change or disable removes sessions | `TestPasswordChangeRevokesAllSessions`, `TestAdministrativeResetAndPermissionsInvalidateOldCookies` |
| GHSA-7526-j432-6ppp | proxy auth + Execute permission | 🚫 | no proxy auth and no command execution | `TestNoProcessExecutionOrPlugins` |
| GHSA-x8jc-jvqm-pm3f | signup grants Execute | 🚫 | no signup and no commands | `TestNoProcessExecutionOrPlugins` |
| GHSA-5gg9-5g7w-hm73 | signup grants admin | ✅ | administrator only through bootstrap/setup code or the panel with reauth | `TestAdminRequiresRoleAndReauthentication`, `TestFirstRunSetupCodeCreatesAdministratorOnce` |
| GHSA-xqp3-jq6g-x3qm | forged proxy auth header | ✅ | identity headers ignored; `X-Forwarded-For` is read only from the trusted proxy CIDR, from the right, and only for rate limits and the log — never for identity | `TestLoginCookieLogoutAndReplayedSession`, `TestProxyBoundaryAndForwardedHeadersIgnored`, `TestClientAddressBehindProxyForLimitsAndLogs` |
| GHSA-hxw8-4h9j-hq2r | password change without the current password | ✅ | requires the current password, versioned | `TestPasswordChangeRevokesAllSessions` |
| GHSA-43mm-m3h2-3prc | username enumeration through timing | ⚠️ | dummy hash for non-existent accounts; timing distribution not measured | — |
| GHSA-w5fm-68j4-fpc4 | login DoS | ✅ | attempt limits per visitor address (the real one behind the proxy, IPv6 per /64) and globally, a gate of 2 Argon2 computations | `TestRateLimitAndBoundedBookkeeping`, `TestLoginHasBoundedSessionsAndWork`, `TestClientAddressBehindProxyForLimitsAndLogs` |
| GHSA-7xwp-2cpp-p8r7 | replay after logout | ✅ | the session is removed on the server side | `TestLoginCookieLogoutAndReplayedSession` |
| GHSA-rmwh-g367-mj4x | sensitive data in the URL | ✅ | the session token only in an `HttpOnly` cookie, CSRF in a header, passwords in the JSON body; URLs contain only the space and path. The exception is the token of a public link, which by design is the link itself: its page and API send `Referrer-Policy: no-referrer` and `X-Robots-Tag: noindex`, and the token is stored only as a hash | code review (`internal/api`, `app.js`, `share.js`) |
| GHSA-cm2r-rg7r-p7gg | unsafe passwords | ✅ | salted Argon2id, constant-time comparison, min. 12 characters | `TestBootstrapPersistenceAndSecretStorage` |

## Commands and hooks

GHSA-39cx-23x9-5c8p, GHSA-8c9q-7855-wfxq, GHSA-jvpw-637p-h3pw, GHSA-m93h-4hw7-5qcm, GHSA-3q2w-42mv-cph4, GHSA-hc8f-m8g5-8362, GHSA-w7qc-6grj-w7r8 — **🚫 not applicable (7)**: Filedeck runs no programs and has no hooks or WebSockets. `TestNoProcessExecutionOrPlugins` ensures the code does not import `os/exec`, `plugin`, CGI/FastCGI or template engines and does not call `Exec`/`ForkExec`.

## Previews and resources

| Advisory | Class | Status | Mechanism | Evidence |
|---|---|---|---|---|
| GHSA-448h-jr2h-3vhp | subtitle conversion into memory | ✅ | no conversion; text max 2 MiB | `TestTextEditorRejectsUnsuitableFiles` |
| GHSA-xfqj-3vmx-63wv | XSS through a branding template | ✅ | no templates and no branding; the interface consists of static files | `TestNoProcessExecutionOrPlugins` |
| GHSA-5vpr-4fgw-f69h | XSS through EPUB | ✅ | no EPUB/HTML rendering; preview only for a fixed list of types, `nosniff`, CSP `sandbox` (an SVG does not run a script even when opened in a tab) | `TestPreviewAndTextEditorOverHTTP`, browser test (SVG with a script) |
| GHSA-7xqm-7738-642x | memory with large files | ✅ | JSON 16 KiB, text 2 MiB, max. 4 buffered saves, no image processing | `TestAdvisoryRegressions`, `TestJSONLimitsAndUnknownFields` |
| GHSA-4wx8-5gm2-2j97 | stored XSS | ✅ | CSP without `unsafe-inline`, Trusted Types (`trusted-types 'none'`), names only through `textContent` | `TestInterfaceHeadersAndFolders`, browser test (name `<img onerror>`) |

## Dependencies and deployment

| Advisory | Class | Status | Mechanism | Evidence |
|---|---|---|---|---|
| GHSA-6jqf-mv7m-3q7p | request smuggling in a dependency | ✅ | HTTP only from the Go standard library; `govulncheck`: the code calls no known vulnerability | `govulncheck` v1.8.0 |
| GHSA-jj2r-455p-5gvf | unsafe file permissions | ✅ | state `0700`, databases `0600`, `.filedeck` `0700`, staging `0600`, configurable modes for new files; image without a shell, `read_only`, `cap_drop: ALL` | `TestStateIsPrivateLockedAndDisjoint`, `TestMkdirUsesConfiguredMode`, `TestUnsafeMetadataDirectoryIsRejected` |

## Hardening before public exposure (stage 12)

Beyond the advisory register, the review before publishing Filedeck on the internet through a reverse proxy added:

1. **Rate limits per visitor behind the proxy.** Before, every connection came from the proxy's address, so one attacker exhausting 10 attempts per minute blocked sign-in and link passwords for everyone. The visitor's address now comes from `X-Forwarded-For`, trusted only from `FILEDECK_PROXY_CIDR` and read from the right (a client cannot choose it). Evidence: `TestClientAddressBehindProxyForLimitsAndLogs`.
2. **Security log.** Failed and successful sign-ins, rate limiting, failed re-entered passwords, account, two-factor and link changes, failed link passwords and connections from outside the proxy CIDR are logged with the visitor's address — for monitoring and fail2ban/CrowdSec. Secrets are never logged (checked by the tests). Evidence: `TestClientAddressBehindProxyForLimitsAndLogs`, `TestTwoFactorSetupLoginAndReset`.
3. **HSTS** (`max-age=31536000`, without `includeSubDomains`) on every HTTPS response. Evidence: `TestHSTSOnlyForHTTPS`.
4. **Two-factor authentication (TOTP)**, optional and managed by each account: RFC 6238 verified against the RFC test vectors, replay protection (an accepted step is never accepted again), single-use recovery codes stored as SHA-256, a lockout after 5 wrong codes in a row (5 min doubling to 1 h), `totp_required` returned only after a correct password, other sessions ended when it is turned on or off. Evidence: `TestTOTPMatchesRFC6238`, `TestTwoFactorLifecycle`, `TestTwoFactorStrikesAndAdminReset`, `TestTwoFactorSetupLoginAndReset`, browser test.

5. **Encrypting two-factor secrets** with an optional key kept outside the data volume (`FILEDECK_SECRET_KEY` / `FILEDECK_SECRET_KEY_FILE`): AES-256-GCM bound to the account, recovery codes as HMAC; migration of existing secrets with a rewrite of the database file so no plaintext copy remains in freed pages; a wrong or missing key stops the start. Evidence: `TestSecretKeyProtectsTwoFactorSecrets` (also reads the raw database file), `TestSecretKeyFromEnvironmentOrFile`.

New dependency: `rsc.io/qr` v0.2.0 (BSD, pure Go, no further dependencies) draws the setup QR code on the server as a matrix; the page renders it on a canvas, so the secret never reaches a third-party service.

## Additional findings from the review (stage 6)

The review of the new features (preview, editor, copy) found and fixed:

1. **Memory during editor saves** — a save buffers up to ~12 MiB before the lock; with 64 parallel requests that is ~770 MiB. Now there is a separate limit of 4 parallel saves (HTTP 429).
2. **Filling the disk** — a copy of up to 256 GiB, uploads and saves could take the whole disk (including under the state database). Now each of these operations leaves at least 512 MiB free (`MinFreeBytes`, HTTP 507 `no_space`).
3. **Targets in `.filedeck`** — an upload/copy to a reserved path was rejected only at publication (harmless, but wasted work). Now `ValidUserPath` rejects it at the start.

Tools: `go vet`, `staticcheck` (clean), `govulncheck` (0 called vulnerabilities; the `x/crypto/openpgp` module is reported but unused), race detector, path fuzzing, Chromium browser test without CSP violations. `gosec` does not work with Go 1.27 (an internal error in an older version, out of memory when compiling the latest).

## Open risks

- Account enumeration through response timing — only mitigated (GHSA-43mm).
- Two-factor authentication is optional; accounts without it are protected by the password and the rate limits only. There is no lockout after failed passwords (only wrong codes lock), so that a stranger cannot lock people out.
- Without `FILEDECK_SECRET_KEY`, TOTP secrets are stored in plaintext in `identity.db` and whoever reads the state directory or a backup of it can generate codes (the start log warns about it). With the key they are encrypted and recovery codes cannot be brute-forced offline; the key itself is in the environment of the running container, so someone with access to the Docker host or the process can still read both — the key protects copies of the data volume and backups.
- Backups of `/data/state` made before the key was set still contain the plaintext secrets: after setting the key, set two-factor authentication up again (new secrets) if such backups may leak.
- The global sign-in limit (20 attempts, then 1/s) lets a distributed attacker make signing in slower for everyone for as long as the attack lasts; limit connections in the proxy as well.
- Changes to the source during a copy/move between spaces are not a snapshot; when moving, the source goes to the trash, so nothing is lost.
- A copy job belongs to the account, not the session: logging out does not stop a copy (disabling the account and revoking permissions do).
- PDF preview works without `sandbox` (browsers do not render PDFs in a sandbox); `nosniff` and a forced `application/pdf` prevent treating it as HTML.
- Not tested on a real SMB/NFS share.
- Public links: on filesystems without a birth time (some NFS/CIFS setups) the object identity relies on device and inode — a file deleted and replaced by a new one with the same inode number could be taken for the same object. A download started before a link was revoked may finish.
