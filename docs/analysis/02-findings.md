# Findings from the reference code

The status "mechanism present" means static analysis of the examined HEAD, not an executed PoC. GHSA numbers refer to the full [register](05-advisories.md). The risks below are grouped by cause; they should not be added up as independent vulnerabilities.

## Mechanisms that need a redesign

| Area | Evidence in the code | Assessment and conditions | Consequence for Filedeck |
|---|---|---|---|
| Cleanup after an upload | `http/resource.go:133`, a `writeFile` error leads to `RemoveAll` on line 183 | GHSA-c4fr-5f24-4wrj mechanism present. Upload onto an existing directory with override; needs Create and Modify, no Delete check in cleanup | We remove only the staging owned by the upload, never the requested target |
| Path policy and symlinks | `http/data.go` checks the path text; `files/scoped.go` allows links that stay in scope | GHSA-7w29-q235-57m9 mechanism present. An alias in scope can point to a target forbidden by a rule | A uniform space policy, symlink traversal forbidden by default |
| Parallel TUS PATCH | `http/tus_handlers.go`, `tusPatchUpload`: stat, offset check, open with append, write | GHSA-4r8p-gqj2-mwgm: the whole cycle is not serialized; a per-request limit is not an upload limit | An upload lock covering the offset check, the write and the state update |
| Command WebSocket | `http/commands.go`: Upgrade and ReadMessage before EnableExec and Execute, no SetReadLimit | GHSA-39cx-23x9-5c8p: mechanism present. Disabling exec does not remove the buffering cost for a signed-in client | Do not register inactive features; authorize before upgrade/read |
| Subtitle conversion | `http/subtitle.go`, `subtitleFileHandler`: ReadAll, parser, result in bytes.Buffer | GHSA-448h-jr2h-3vhp: mechanism present; an authorized read of a large file may cost many copies in RAM | Limit the input, output, time and number of conversions |
| Special files in ZIP | `http/raw.go`, `rawHandler` checks for named pipes, `getFiles` does not filter types before Open | GHSA-8q5j-8wcr-8v2v: mechanism present. A FIFO may come from local storage | Only regular files and directories; no blocking open of a FIFO |
| Shares after deletion | `http/resource.go:110`, `storage/bolt/share.go`, userID filter | GHSA-r6pg-pg54-rcr5: cleanup limited to the deleting user's shares, despite a shared file | Invalidating a resource's shares independently of the share owner |
| Shares after rename | `http/resource.go`, `patchAction` removes the cache, moves the file, does not revoke shares | GHSA-m8v4-4w34-rrvf: the old path may later reveal new content | Explicit semantics of identity and share generation |
| Sessions | `http/auth.go`, `frontend/src/utils/auth.ts:118`: logout clears the client state | GHSA-7xwp-2cpp-p8r7 and GHSA-v7vv-5wj2-gfcj: no server-side token revocation; reading current permissions from the database does not invalidate authentication | A session with a random secret, a server-side record, logout/reset revoke sessions |
| Token available to JS | `frontend/src/utils/auth.ts:13`: cookie from JS and localStorage | Increases the impact of XSS; not proof of XSS on its own | HttpOnly, Secure, SameSite cookie; separate CSRF protection |
| Runner and hooks | `runner/parser.go`, `runner/runner.go`, `http/commands.go` | Shell, variable substitution, subcommands and the process UID's access; depends on configuration. A name allowlist gives no isolation | Remove the executor from the core or separate it with real isolation |
| Proxy auth | `auth/proxy.go`, `ProxyAuth.Auth` trusts a header | GHSA-xqp3-jq6g-x3qm: direct client access to this mode crosses the trust boundary | Prefer OIDC; proxy only as an explicitly trusted peer, checked on the backend |
| Default scope of new accounts | `http/auth.go`, `settings/dir.go`, `settings/defaults.go` | GHSA-6759-996p-gpj6: with CreateUserDir disabled, defaults may still grant a shared root | No automatic inheritance of the administrator's scope |

## Fixes already visible — do not present them as missing

- `withUser` fetches the current user from the database, so it does not rely only on the copy of permissions stored in the JWT.
- `renewableErr` requires a proxy identity assertion for an expired JWT; `TestExpiredTokenNeedsProxyAssertion` exists. GHSA-v3jv-rmh2-635j has an upstream fix listed.
- `checkDescendants` and `TestRecursiveOperationsEnforceDescendantRules` address GHSA-77x8-73f4-5485 on the normal copy/rename/delete paths. This does not cover upload cleanup.
- TUS checks for a negative Upload-Length and limits the bytes of a single PATCH. This does not prove resistance to parallel PATCHes.
- Signup removes Admin and Execute; provisioning checks for scope collisions. This does not remove the shared-root problem with a different configuration.
- `ScopedFs` also checks dangling symlinks; public shares have a separate directory restriction and symlink tests.
- `shareResponse` does not serialize the password hash and bypass token. The token-in-URL mechanism in `public.go` remains a separate matter.
- `files/file.go:264` has a 10 MiB limit for classifying a file as text. The old report about arbitrarily large text is not automatically current; the question of a size change between stat and ReadFile and the number of parallel reads remains.
- Markdown uses DOMPurify. The presence of `v-html` is not proof of XSS on its own.
- The Dockerfile sets an unprivileged user; CI includes `go test --race`. These protections should be kept as requirements, not considered missing.

## Additional observations that need separate tests

1. **TOCTOU in ScopedFs.** `guard()` performs EvalSymlinks, after which a separate `base.Open/OpenFile/...` resolves the path again. With a concurrent change to the tree there is a race window. A controlled test with a process swapping a link/directory is needed; we do not report a new confirmed exploit here. The mechanism of this class is described in [Go: traversal-resistant file APIs](https://go.dev/blog/osroot).
2. **Effect before TUS POST validation.** `tusPostHandler` opens the target and may perform O_TRUNC before `getUploadLength`. Statically, an existing file could be changed before an invalid header is rejected. A test should check that data stays unchanged for an invalid and a negative size. We do not automatically attribute this to an existing GHSA.
3. **Aggregate limits.** Recursive listing collects results in an array; checking the Context helps with cancellation but does not limit the maximum result. Large trees and parallel requests should be measured.

## What still needs an audit

A full analysis of the frontend parsers, all administrative handlers, transitive dependencies and the lockfile, proxy configuration, migration/import, the Redis cache, logs, secrets and the release chain. The advisory register is a complete download of public metadata, not a complete confirmation of every scenario on the local code.
