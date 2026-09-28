# Filedeck — analysis before the new implementation

Analysis status: 2026-09-28. This analysis precedes the implementation and is kept as a historical record; the current state is in [PROGRESS.md](../PROGRESS.md) and [SECURITY.md](../SECURITY.md). The [first core prototype](06-implementation.md) and its tests and limitations are described separately.

## Conclusion

It is worth recreating File Browser's features on a new core. The biggest benefit comes from simplifying the access model, the upload lifecycle, sessions and sharing. Changing the language or framework alone does not remove the causes of the bugs found.

The new implementation should keep the user's needs: working with directories, upload, download, editing, search, preview, accounts and sharing. We do not assume compatibility with the old API, database or all configuration options. Removing or deferring features in the plan below is a recommendation, not a decision accepted by the owner.

## Material and limitations

- Reference repository: `../../reference`, origin `git@github.com:pbuzdygan/filebrowser.git`.
- Examined HEAD: `833d908884d5c801f30f5c098d7977177eb3a36b`, commit date 2026-07-28, `docs: update post link`.
- The reference tree was clean. `git describe --tags --always` returns only the hash; we do not automatically assign a release number. HEAD was not compared with the current upstream remote master.
- Sources: Go backend, HTTP routes, auth, filesystem, upload, share, rules, selected tests, frontend auth and editor, dependency manifests, Dockerfile and CI.
- All 62 public advisories returned by the upstream API were downloaded: 5 critical, 27 high, 24 medium, 6 low. 19 have no fixed version in the `patched_versions` field. **This does not mean 19 confirmed vulnerabilities in this checkout**: metadata, configuration and code need separate interpretation.
- No application, PoC, tests or dependency scanners were run. Confirming a mechanism in the code is distinguished from demonstrating an attack. This is not a completed security audit or a guarantee of completeness.
- Public advisories do not cover private reports or undisclosed vulnerabilities.

## Documents

1. [Features and proposed scope](01-functions.md).
2. [Findings from the code](02-findings.md).
3. [Filedeck architecture and threat model](03-design.md).
4. [Implementation order and acceptance criteria](04-roadmap.md).
5. [Register of all 62 advisories](05-advisories.md).
6. [Raw API record, with descriptions and metadata](sources/upstream-advisories.json).

## Decisions affecting further implementation

The user confirmed: **Linux and Docker**, **existing directories also changed by other applications or SMB/NFS**. These are project requirements. We still need to distinguish a local filesystem exported over SMB/NFS from a network mount on Filedeck's side, and to decide which configurations are tested; we do not automatically declare write support on every SMB/NFS.

Still to decide: the number of users and their mutual trust; internet access; local accounts or OIDC; the need for anonymous links; maximum file and directory sizes; migration requirements. This does not block the analysis of security mechanisms, but it affects the details of contracts and tests.
