# First implementation stage

A new project was created in [filedeck](../../README.md), with an independent Go module and code written from scratch.

A prototype of storage, permissions and upload was built, following the direction of stage B of the plan. Decisions for this stage:

- Linux/Docker; opening through `openat2`, without symlinks and nested mounts.
- One space and one instance; no database at this stage.
- Upload of new files only, with no-replace publication. Overwriting requires further design with regard to external changes.
- Private staging next to the data directory, on the same mount.
- A restart removes abandoned staging instead of pretending that resumption is possible.
- A local CLI as a testing tool; HTTP, sessions and the UI remain the next stage.

This describes the first stage only; later stages (durable resumable uploads, multiple spaces, the web interface, public links) are in [PROGRESS.md](../PROGRESS.md). [The contract and limitations](../CONTRACT.md) define the semantics of handles, external changes and deployment conditions. [Progress and verification](../PROGRESS.md) contains the list of test scenarios and the mapping to GHSA classes.
