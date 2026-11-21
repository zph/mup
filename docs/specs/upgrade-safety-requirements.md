# Upgrade Safety Requirements

Safety gates for `mup cluster upgrade` on local clusters. These complement
`docs/UPGRADE_SPEC.md` (UPG-*), which describes the overall workflow.

## Health

- **UPGSAFE-001**: WHEN evaluating replica set health, the system SHALL require a primary and SHALL treat a member as healthy only IF it is reachable and in the PRIMARY, SECONDARY or ARBITER state.

## Prompts

- **UPGSAFE-002**: WHILE the prompt level is `none`, the system SHALL NOT read confirmations from standard input, including the failover and resume confirmations.
- **UPGSAFE-003**: WHEN prompting before a critical operation, the system SHALL proceed only on an explicit "continue"; IF the answer is "pause" THEN the system SHALL pause the upgrade, and any other answer SHALL abort.

## Completion

- **UPGSAFE-004**: IF any node was skipped, THEN the system SHALL NOT run post-upgrade tasks (metadata update, FCV change, old supervisor shutdown) and SHALL report the skipped nodes.

## Ordering and verification

- **UPGSAFE-005**: WHILE upgrading a replica set, the system SHALL, before taking each node down, wait until every member is healthy and re-read member roles; it SHALL upgrade non-primary members first and the primary last; and IF replica set state cannot be read or a node is not a member, THEN the system SHALL stop rather than assume a role.
- **UPGSAFE-006**: WHEN a primary is stepped down, the system SHALL verify that a different member became primary, and IF not THEN the system SHALL stop without stopping the node.
- **UPGSAFE-007**: WHEN a node has been restarted on the new binaries, the system SHALL ask the server for its version and SHALL fail the node IF the version does not match the target.
- **UPGSAFE-008**: WHEN upgrading a sharded cluster, the system SHALL stop the balancer before upgrading config servers IF it is enabled, SHALL start it again after mongos is upgraded only IF the upgrade stopped it, and SHALL record that decision in the upgrade state.

## Resume

- **UPGSAFE-009**: WHEN an upgrade stops with an error, the system SHALL record it as failed; the system SHALL allow paused, failed and interrupted upgrades to be resumed; a resume SHALL take its target version and FCV choice from the saved state; and IF a different target is requested while an unfinished upgrade exists, THEN the system SHALL refuse.
- **UPGSAFE-010**: WHEN installing binaries into a version directory, the system SHALL leave an identical existing binary untouched and SHALL otherwise replace it by rename, never by truncating it in place.

## Failure handling

- **UPGSAFE-011**: WHEN an upgrade fails at any phase, the system SHALL run the `on-upgrade-failure` hook on a context that is not cancelled with the upgrade; a paused upgrade SHALL NOT be treated as a failure.
- **UPGSAFE-012**: WHEN preparing a new version directory, the system SHALL create each node's log directory under the name it is supervised as; config servers SHALL use `config-<port>`.

## Replication lag

- **UPGSAFE-013**: WHILE upgrading a replica set, the system SHALL NOT take a node down while any data-bearing secondary is more than the lag limit (default 30 seconds) behind the primary.

## Rollback

- **UPGSAFE-014**: WHEN `mup cluster upgrade --rollback` is run on an unfinished upgrade, the system SHALL move every node the upgrade touched back to the old binaries (mongos, then shards, then config servers; unhealthy members first, the primary last after a stepdown), SHALL verify each reports the old version, SHALL restore the balancer if the upgrade stopped it, and SHALL archive the upgrade state. IF post-upgrade tasks have started, THEN the system SHALL refuse to roll back.

