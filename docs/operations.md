# Operations

## Identity and data

`ak-<12 hex SHA256(namespace/name)>-<Kubernetes node name>` is the stable node identity; external capture uses the same cluster prefix plus `-external`. Node PCAP resides in `<hostPath>/ak-<hash>`, external PCAP in one RWO claim. These identities and locations are persistence contracts. The receiver uses Recreate and one writer, with a 90-second termination grace period. Expect an ingestion gap during replacement; UDP has no delivery guarantee.

Set a capture block's `enabled: false` to retain a viewer serving its old PCAP. Keep the storage specification. Removing the block is rejected after provisioning. Deleting the entire CR removes the access path but preserves host files, generated claims, auth/admin Secrets, and database indices. Back up all four together. To restore, explicitly reference retained claims/secrets, preserve namespace/name and prefixes, and use schema mode `Adopt` after verifying image/schema compatibility. Node/disk loss destroys local data unless independently backed up.

Generated Secrets have no ownerReference. A missing recovery Secret after initialization blocks reconciliation; it is never silently replaced with a new password encryption key. Secret content changes update pod template checksums within the 30-second reconciliation interval. Secret values are never stored in ConfigMaps or status.

## Schema and retention

Schema/admin work runs in a deterministic finite Job with a deadline and zero automatic retries. Its guarded initializer checks the engine, existing template version, and prefix before invoking `db.pl init --ifneeded`. A database owner marker supports resuming an interrupted fresh initialization. Existing unowned schemas require explicit `database.schema.mode: Adopt`; higher schema versions and unapproved migrations fail closed. Admin creation uses `--createOnly` and a password over stdin.

On failure, inspect the Job named in status. Correct the underlying issue, then change `database.schema.retryToken` to request a new operation. Do not use initialization to recover a damaged schema. Completed operation IDs are persisted before application creation, so deleting a completed Job does not rerun bootstrap.

The pinned 6.6.0 → 6.7.0 transition is implemented. Approval is durably recorded before deleting managed compute, waiting for all application pods to stop, running the finite schema Job, and recreating applications. Other transitions and downgrades are blocked. The transition remains subject to the release qualification matrix. Before any future supported schema upgrade, take a database snapshot and retain PCAP and auth Secrets. Image rollback is not schema rollback. Existing Helm installations need a manual migration preserving identity, storage and prefixes; a moving Helm image tag does not prove that the required Arkime 5.2+ prerequisite was met.

Managed retention uses one daily `db.pl expire daily <days>` CronJob scoped to the session prefix. `retention.mode: External` removes that Job. Do not independently enable ILM/ISM on the same installation. PCAP disk reserve (`10%` by default) is separate and enforced by local viewers.

## TLS

Use [TLS and private CAs](tls.md) for cert-manager setup, database trust, renewal, and the current limits of internal Arkime TLS. Browser HTTPS termination does not encrypt Gateway-to-viewer or viewer-to-local-viewer traffic.

## Troubleshooting

`InvalidSpec` identifies inconsistent inputs. `DependencyUnavailable` identifies missing Secret keys or claims. `OwnershipConflict` prevents adoption of unowned resources or known database/host-port overlaps. `BootstrapFailed` links to a finite Job. `Progressing` reports unavailable workloads or exposure. Zero desired DaemonSet pods is not ready. `Degraded` records unconfigured WISE enrichment.

Route readiness requires current Accepted and ResolvedRefs conditions; the referenced Gateway also needs current Programmed. Missing Gateway CRDs affect Gateway requests only. A LoadBalancer requires a reported address. These conditions do not prove end-to-end network delivery. Check CNI policy, host firewalls and controller-specific UDP behavior.

Cont3xt's fixed indices are not isolated by the session prefix. Use distinct backends, or set explicit sharing on all participating CRs. Endpoint aliases can evade overlap detection; this is not a security boundary. Multiple independent operators/external schema tools still require coordination.

Optional `networkPolicy` installs internal application/DNS rules plus the user's explicit ingress/egress rules. Supply database, web/Gateway, OIDC/provider, sender, and host-IP routing rules as needed; policy enforcement is CNI-dependent.

Requests start at 100m CPU/256Mi per application container for development; size capture memory, threads, packet buffers and storage against real traffic and retention. Monitor Arkime drops, queues, disk space and recent capture, plus operator metrics. Optional operator metrics are unauthenticated internal HTTP; restrict access with your cluster policy. Ordinary pod NetworkPolicy cannot be assumed to isolate host-network capture.

Node tpacket capture runs with NET_RAW, NET_ADMIN, SETUID, SETGID and IPC_LOCK, then drops to nobody. The default ring uses one thread and 64 KiB blocks; tune for measured throughput. External TZSP capture and local viewers run as UID/GID 65534 without added capabilities. An init container owns only the configured PCAP directory. Web processes receive SIGINT through a preStop hook; capture receives SIGTERM and has up to 90 seconds to flush.

WISE enables Kubernetes pod enrichment by default; set `spec.wise.kubernetesEnrichment.enabled: false` to opt out. The Operator cleans up its Pod-reader cluster-wide RBAC on disable or ArkimeCluster deletion. Keep the Operator running until the enrichment finalizer is removed.
