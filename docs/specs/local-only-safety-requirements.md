# Local-Only Safety Requirements

mup manages MongoDB clusters on the local machine only. These requirements
cover destroy safety, deploy input validation, and simulation purity.

## Destroy

- **LOCAL-001**: WHEN a cluster is destroyed, the system SHALL remove only paths inside that cluster's directory and SHALL NOT use node paths recorded in metadata as deletion targets.
- **LOCAL-002**: WHEN a cluster is destroyed without `--keep-data`, the system SHALL remove the entire cluster directory.
- **LOCAL-003**: WHEN a cluster is destroyed with `--keep-data`, the system SHALL preserve the cluster's `data/` directory, SHALL remove every other entry in the cluster directory including `meta.yaml`, and SHALL print the preserved path.
- **LOCAL-004**: WHEN cluster metadata is saved after a deploy, the system SHALL record each node's resolved local data, log and config paths rather than the raw topology values.

## Deploy

- **LOCAL-005**: IF a topology contains a host other than `localhost`, `127.0.0.1` or `::1`, THEN the system SHALL reject it before generating a plan and SHALL name the offending hosts.
- **LOCAL-006**: WHEN generating a deploy plan, the system SHALL validate the topology after port allocation and SHALL reject an invalid topology before any operation is planned.
- **LOCAL-007**: The system SHALL NOT require `global.user` or `global.deploy_dir` in a topology.

## Simulation

- **LOCAL-008**: WHILE running with `--simulate`, the system SHALL NOT write cluster metadata, symlinks, supervisor configuration, plans, locks or apply state under the real storage directory, and SHALL NOT download MongoDB or supervisord binaries during apply.

## Deploy reliability

- **LOCAL-009**: WHEN selecting a configuration template for a MongoDB version with a release suffix (for example Percona `8.0.12-4`), the system SHALL select the template that matches the `X.Y.Z` core version.
- **LOCAL-010**: WHEN verifying cluster health, the system SHALL check that every node port in the topology is listening, and IF no ports are supplied THEN the system SHALL fail the check.
- **LOCAL-011**: The system SHALL interpret supervisor process states case-insensitively and independently of terminal colour codes.
- **LOCAL-012**: IF any node has not reached the RUNNING state after `cluster start`, THEN the system SHALL return an error naming each such node and SHALL NOT record the cluster as running.

## Resume

- **LOCAL-013**: WHEN an apply is resumed, the system SHALL load the plan from `<cluster-dir>/plans`, SHALL skip phases and operations that already completed, and SHALL continue from the first operation that did not.
- **LOCAL-014**: IF a resume cannot start or fails again, THEN the system SHALL leave the apply state resumable.
- **LOCAL-015**: WHEN an apply fails, the system SHALL return the apply state together with the error.
- **LOCAL-016**: WHEN an operation reads a topology from a plan loaded from disk, the system SHALL accept the JSON-decoded form as well as the in-memory form.

## Locking

- **LOCAL-017**: WHEN several processes try to lock the same cluster at once, the system SHALL grant the lock to exactly one of them.
- **LOCAL-018**: IF a lock has expired, or the process that took it on this host has exited, THEN the system SHALL treat the lock as stale and allow it to be taken over; `mup cluster unlock` SHALL remove a lock on request.
- **LOCAL-019**: IF a lock file exists but cannot be read, THEN the system SHALL refuse to acquire the lock and SHALL NOT overwrite the file.

## Storage location

- **LOCAL-020**: WHERE the `MUP_STORAGE_DIR` environment variable is set to an absolute path, the system SHALL keep all clusters, cached packages and helper binaries under that directory instead of `~/.mup/storage`.

## Lifecycle ordering

- **LOCAL-021**: WHEN starting a cluster, the system SHALL start config servers, then data-bearing members, then mongos, waiting for each group to be RUNNING before the next; WHEN stopping a cluster, the system SHALL use the reverse order.

## Replica set members

- **LOCAL-022**: WHEN initiating a replica set, the system SHALL apply each member's `priority`, `hidden` and `votes` settings from the topology, and SHALL plan replica sets in name order so the same topology yields the same plan.

## Binary cache

- **LOCAL-023**: WHEN extracting an archive, the system SHALL reject any entry that would be written outside the extraction directory.
- **LOCAL-024**: WHERE a checksum is published for a download, the system SHALL verify the downloaded archive against it and SHALL fail on a mismatch.
- **LOCAL-025**: WHEN caching a package, the system SHALL assemble it in a staging directory and publish it only when complete; the system SHALL replace an incomplete cache directory and SHALL NOT replace a complete one.

## Supervisor

- **LOCAL-026**: IF the supervisor control port derived for a cluster is already in use, THEN the system SHALL fail before starting supervisord and SHALL name the port and the cluster.

## Monitoring

- **LOCAL-027**: The system SHALL install Grafana dashboards from copies embedded in the binary, independent of the working directory.
- **LOCAL-028**: WHEN checking monitoring health, the system SHALL use the ports recorded in the cluster's monitoring URLs, falling back to the defaults only when a URL has no port.
