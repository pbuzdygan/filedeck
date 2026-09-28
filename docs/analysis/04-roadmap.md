# Work order and acceptance criteria

This roadmap was written before the implementation; actual progress is tracked in [PROGRESS.md](../PROGRESS.md).

## A. Complete the analysis and contracts

Before writing application code, decide the platforms, the model of external changes, roles, supported operations and share semantics. Give every advisory in the register a final status: mechanism present / fixed in the examined code / configuration-dependent / not applicable / needs evidence. The current register deliberately does not pretend to be a completed verification.

Expected artifacts: an accepted feature map, a threat model, a permission matrix, error contracts and limits, decisions about supported filesystems. Include a review of surfaces not mentioned in advisories: admin, import, frontend, dependencies, CI and releases.

## B. Security core prototype

A small prototype of filesystem operations and upload, without an elaborate UI. This is the right next step after the decisions, not rewriting visual components.

| Contract test | Required result |
|---|---|
| `..`, double encoding, slash/backslash, absolute path | Rejection or an unambiguous interpretation consistent with the contract; zero escapes from the space |
| Internal/external/dangling symlink, concurrent directory swap | No read/write outside the allowed boundary and no bypass of the symlink policy |
| FIFO/socket/device in read, preview and ZIP | Fast rejection without hanging a worker |
| An invalid upload onto an existing file/directory | Original data unchanged; cleanup affects only staging |
| Two PATCHes with the same offset, two commits to the same target | Serialization or a conflict; no duplicated data or finalisation events |
| Network loss, no space, restart in every upload state | Recoverable state; no deletion of others' data |
| Permissions revoked during an upload | Finalisation refused according to the agreed semantics |
| External change of a file during editing | A conflict or an explicit contract of limitations; no CAS promise based only on stat |

Tests must check the state of the data and side effects, not just the HTTP status. Controlled races with barriers are more meaningful than just running a test many times. `go test -race` detects memory races; it does not prove the absence of filesystem TOCTOU.

## C. A working foundation

Local accounts, sessions, spaces, listing, download and upload. Before acceptance:

- An old token/cookie is rejected after logout, password reset and account disable.
- CSRF requests and foreign identifiers do not change data.
- All read paths respect the same permission matrix.
- Limits hold with many users, also after an interrupted transfer.
- Backup and restore rehearsed on test data.

## D. Full file work

Copy, move, delete, edit, search, ZIP and resumption. Criteria: explicit behaviour for conflicts and partial operations, cancellation tests, correct ZIP entry names (also for a Windows recipient), bounded cost for large directories. Pagination does not guarantee cheap sorting of a huge directory — a measurement or an index is needed.

## E. Shares and previews

Add after the resource semantics are approved. Mandatory scenarios: a share by user A, delete/rename/replace by B; recreating the old path; a change outside the application; expiry and revocation; revoking the owner's permissions; an attempt to escape the shared directory. Check XSS in a real browser; test parsers with large and corrupted files.

## F. Migration and release

Import in dry-run mode on a copy of the old database, a report of all rules and conflicts that were not carried over. No automatic broadening of permissions for "compatibility". Old sessions and links expire by default; password migration needs a separate compatibility assessment. Rolling back a deployment must take file changes into account, not just restoring the database.

CI: contract and integration tests on supported platforms, path and parser fuzzing, the race detector, dependency and secret checks, a reproducible build with a lockfile. Tests of old vulnerabilities are a source of scenarios; new tests are written against Filedeck's contracts. Before a public release, an independent security review of the critical core and documentation of the limitations.

## Rule for closing an advisory in Filedeck

Every GHSA must have an identified mechanism, a design decision and a test with a result, or the justification "the feature does not exist". A "not applicable" status is reopened when the feature is added. A library upgrade, a UI switch or a project rename alone does not close a threat.
