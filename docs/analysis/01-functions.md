# Features: keep the outcome, redesign the mechanism

The map is based on `reference/http/http.go`, the backend modules and the views in `reference/frontend/src/views`. Stages indicate the proposed order, not permanent removal of a feature.

| User feature | Current locations | Filedeck proposal | Stage |
|---|---|---|---|
| Listing, sorting, views, hidden files | `files/listing.go`, `files/sorting.go`, `FileListing.vue` | Listing with limits; the hiding preference separate from permissions | 1 |
| File download, audio/video | `http/raw.go`, `Preview.vue` | Streaming and Range; a common permission for reading content | 1 / preview 3 |
| Uploading multiple files and folders | `resource.go`, frontend upload | A queue in the UI; private staging and explicit commit | 1 |
| Resumable upload | `tus_handlers.go`, memory/Redis cache | One upload model, identifier and owner; a TUS adapter if needed | 2 |
| Creating directories | `resource.go` | An explicit operation, validated before any effect on disk | 1 |
| Rename, move, copy, delete | `resource.go`, `fileutils` | Domain operations, source and target checks, clear conflicts | 2 |
| Text editing and Markdown preview | `files/file.go`, `Editor.vue` | A limit on actually read bytes, protection against losing others' changes | 2 |
| Name search, recursive listing | `http/search.go`, `search/`, `resource.go` | Limits on time, results, depth and parallelism | 2 |
| Downloading a directory as an archive | `raw.go` | ZIP first, without special files, symlinks and unsafe names | 2 |
| Public links, password, expiry | `share/`, `http/public.go` | A separate recipient permission; revocation and unambiguous resource semantics | 3 |
| Thumbnails, images, subtitles, PDF, EPUB, CSV | `img/`, `http/subtitle.go`, frontend previews | A matrix of supported formats; parser limits, isolation of active formats | 3 |
| Accounts, passwords, profile, administrator | `users/`, `http/users.go` | Accounts separated from spaces; server-side sessions, roles as permission sets | 1 |
| Registration and automatic accounts | `auth.go`, `auth/proxy.go`, `settings/dir.go` | Disabled by default; a new user has no access until granted | later |
| Login through proxy/hook/no-auth | `auth/` | Local accounts first; OIDC as the preferred integration. Proxy requires an explicit trust boundary | later |
| Permissions and path rules | `Permissions`, `rules/`, `data.go` | Permissions for whole, explicit spaces; no regexes and overlapping exceptions in v1 | 1 |
| Terminal and command hooks | `runner/`, `http/commands.go` | Recommendation: none in the server process. If essential — a separate project for an isolated executor | separate decision |
| Branding, languages, theme, preferences | `branding/`, frontend, settings | Data and allowed appearance options; no executable user templates | 3 |
| CLI, configuration, Docker, database backup | `cmd/`, `storage/`, Dockerfile | A small administrative CLI, one configuration, explicit migrations and backup | 1 |
| Space statistics and checksums | `diskUsage`, `resourceGetHandler` | Space/quota information; a checksum requires read permission and a work limit | 2 |

## The most important simplification of the access model

A space (`Space`) represents an explicitly designated directory. An account gets a set of permissions for a space, e.g. listing, reading content, creating, overwriting, moving, deleting and sharing. Reader/editor roles are presets of these permissions. Administering accounts does not have to grant access to content automatically.

In v1, spaces with different policies should not overlap or contain each other. Separating a confidential subdirectory requires splitting storage boundaries; hiding it in the interface is not protection. If compatibility with the current per-file exceptions is needed, that requires a separate ACL design and tests — they must not be automatically flattened into broad access.

Preview and download hand the content to the user. We do not promise "can view but cannot copy" protection. The ZIP export feature can be controlled separately, but the absence of a download button must not be treated as confidentiality protection.
