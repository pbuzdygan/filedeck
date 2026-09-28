# Filedeck — proposed security design

Status: a proposal to be refined (historical; the implemented contracts are in [CONTRACT.md](../CONTRACT.md)). Confirmed scope: Linux/Docker and existing directories also changed by other applications or SMB/NFS. A single server instance is an architectural recommendation. Other network users and file data are untrusted. The host operator and the mount configuration are trusted; a local process changing files may cause races. A compromise of the host's root is outside the application's guarantees.

## Structure

A modular monolith: HTTP/UI → sessions → operation services → access policy and storage. Separate modules for accounts/sessions, spaces, file operations, uploads and shares; expensive jobs have a bounded queue. A handler never gets an arbitrary host path or a general filesystem for direct use.

Go remains a sensible candidate, because the new model does not require changing the language. Proposed minimum infrastructure: one process, the frontend as static assets and one local metadata database; SQLite is a candidate to evaluate, not an approved choice. Do not add Redis or multiple replicas in v1. Process locks are not a guarantee in a future multi-instance model.

## Trust boundaries and invariants

| Boundary | Rule | Verification |
|---|---|---|
| Browser → API | The client does not determine the owner, the scope or its own permissions | Modified IDs, mass assignment of fields, foreign resources |
| Account → space | No grant means denial; every operation is checked | The same permission matrix for listing/raw/preview/search/ZIP/edit |
| API path → OS | The operation stays within the opened space boundary even during a race | Traversal, symlinks, directory swap, platform-specific names |
| User data → HTML/parser | A file is untrusted regardless of its extension | XSS, active SVG/HTML/EPUB, large and malformed formats |
| Upload → target file | An incomplete upload does not change an existing target | Error, cancellation, restart, conflict, concurrent write |
| Share → anonymous recipient | The permission is limited to the share and its current state | Foreign ID, revocation, expiry, resource replacement |
| Proxy → identity | A client header never becomes an identity | Direct connection and forged forwarded headers |

## Filesystem access

Use a directory handle and traversal-resistant APIs; evaluate `os.Root` for the chosen Go version and the full set of operations. The official documentation describes its protection and limitations: [os.Root](https://pkg.go.dev/os#Root), [description of the mechanism](https://go.dev/blog/osroot). `os.Root` allows some symlinks inside the boundary — on its own it does not implement our "no symlinks" policy or application permissions.

Do not traverse symlinks by default; if they are needed in the future, design their semantics separately. `Lstat` before `Open` is not enough, because that recreates the race. On Linux evaluate handles and the appropriate OS operations; other platforms need a separate set of guarantees and tests.

Support regular files and directories. FIFOs, sockets and devices must be rejected without a blocking read; checking the type only after a potentially blocking Open is not enough. Hardlinks and mounts are a separate problem: path isolation does not guarantee isolation of data shared through a hardlink. A trusted configuration of exported directories, OS permissions and mounts is part of the deployment model.

The API contract should define path decoding and validation unambiguously; reject ambiguous input instead of interpreting the same name differently in authorization, storage and ZIP. The filesystem, including case folding and Unicode, determines real name collisions.

## Sessions and identity

A random session secret; in the database its hash, the user, validity terms and revocation state. A server-set cookie with HttpOnly, Secure and SameSite. An explicit POST logout, session invalidation on password reset and account disable. Re-authentication for sensitive account changes. The idle limit and maximum lifetime are enforced by the server.

The cookie requires CSRF protection for state-changing operations: an Origin check and a CSRF token, no mutations in GET. Limit the cost of logging in, the input size, the number of attempts and parallel expensive hashing operations; do not rely only on an IP address from an untrusted header.

An account has a stable identifier independent of the login name. A private directory may be derived from the ID, never from a potentially colliding normalisation of the username. No default permissions for a shared root. Registration and automatic provisioning are separate features.

## One upload model

States: created → uploading → ready to commit → committed; separately cancelled/expired. The record has the owner, space, target, size, offset, expiry and a random identifier. Both simple and resumable uploads use the same model.

Validation and authorization first, then staging in a private directory on the same filesystem as the target, unreachable through the exported API. Limits per file, per user/space, on active transfers and on total staging. The declared size does not replace counting the actual bytes.

The upload lock covers reading the offset, the write and the state update; a target lock decides competing commits. Permissions are checked again at finalisation. "Do not overwrite" needs atomic no-replace semantics, not just Exists+Rename. A temporary write + rename can give atomic visibility of a single file on a supported filesystem; it does not automatically give a database+disk transaction or atomicity of a whole tree.

Cleanup removes only the staging file owned by the operation. A restart requires state recovery and idempotent cleanup. A cross-filesystem move is a separate copy+commit+delete operation that may partially complete. For SMB/NFS the semantics of the target environment must be verified first.

## Sharing and external changes

The hardest decision: what does a link identify? A path, a specific object or a frozen version of the data?

- For files managed only by the application: a resource ID + generation, invalidation of shares after delete/replace and an explicit rename policy. The database alone does not provide atomicity with the disk; an operation log and crash recovery are needed.
- For arbitrary directories changed over SMB/by other processes: a watcher, inode and mtime are not sufficient proof of immutability. The safe variant for the first public links is copies/snapshots in storage managed by Filedeck, with space limits. Copying a source that changes meanwhile does not guarantee a consistent snapshot — a separate condition to solve.
- A "live folder link" can be a deliberate feature that also reveals new files. It needs clear information in the UI and a separate access model; it must not pretend to be a link to an immutable object.

A link has a high-entropy random secret, expiry and revocation. A share password should not generate a second persistent URL that bypasses the password protection; after unlocking, a short recipient session. Remove share secrets from logs and referrers. Check the current state of the owner, the share and the permissions on every access. A content cache does not bypass authorization.

## Previews and limits

Treat HTML/SVG/EPUB as active content. The simplest first version may offer only a download; a later preview requires a sandbox or a separate origin without the application's session. Sanitize Markdown and restrict raw HTML. The list of supported formats should follow from tests and user needs.

Limits apply to the bytes actually read, the output size, pixels after decoding, tree depth, the number of results, time and parallelism. A Context alone will not stop every library or a blocking syscall; uninterruptible and risky conversions may need a separate process with OS limits.

## Configuration and operations

One explicit configuration model with validation before startup. Secrets outside the shared directories. Startup without a default public administrator account; a one-time bootstrap. A non-root process, minimal mounts, backup of metadata and data, an event log without secrets. Migrations do not import active sessions, old share secrets or rules that cannot be mapped without broadening access.
