# Qualification record

This is a development implementation of the design, not the v0.1 release acceptance sign-off.

Executed locally:

- Inspected the pinned Arkime 6.7.0 image manifest (amd64 and arm64), capture version, runtime UID and required WISE/TCP health plugins.
- Unit tests: validation/injection rejection, deterministic config/identity, retained local-viewer behavior, bootstrap gating, completed-Job deletion, generated Secret retention, and ownership conflicts.
- Generated structural CRD installed into envtest, including valid creation, invalid authentication rejection, stable reconcile generation, and removal of the writer when capture is disabled.
- Helm lint/render, generated-file drift, Go vet, and GoReleaser configuration checks.
- A no-tag GoReleaser snapshot built both Linux architectures, local ko artifacts, both Helm packages, rendered installation manifests and checksums without publication.
- On 2026-09-18, a Kubernetes 1.34.0 kind cluster on arm64 with kindnet and two worker sensors passed exact TZSP packet download through the authenticated central viewer before and after receiver replacement; node-specific packet downloads before and after replacing each sensor; WISE fixture enrichment; rejection of unauthenticated sessions requests; and historical retrieval after disabling external capture, with the writer container confirmed absent. The node viewer used the nondefault port 8105. These observations do not qualify other CNIs or load levels.
- The pinned 6.6.0 → 6.7.0 upgrade passed approval gating, quiescing, finite migration completion, operator restart after Job deletion, retained Secret/PVC checks, and CRD Helm uninstall/reinstall with the original CR UID preserved. Both images use schema version 86: this verifies the application upgrade lifecycle, not a migration between different database schemas.

Integration results and remaining release gates are updated as testing progresses. Features without qualification must not be advertised as supported.

Implemented but still requiring release qualification: OIDC, scoped bootstrap credentials, private CA bundles, mTLS (Cont3xt integration is explicitly rejected because this image does not support it), source ConfigMap mounts, NetworkPolicy rules, plugin merging. Downgrades and other upgrade paths are rejected.

The release matrix still needs real Gateway-controller UDP forwarding, private-CA/auth failures, OIDC identity-provider login, distinct users/Cont3xt backend tests, and destructive-failure migration recovery. Unit/envtest results do not replace these gates. Native ILM/ISM is deliberately not selected; the implementation uses the planned expiry CronJob alternative. Only local PCAP and a single external writer are modeled.

Arkime image observations: the image starts as root; the operator runs web applications as UID/GID 65534 with writable `/tmp`. Cont3xt `/api/health` requires authentication; readiness uses `/_ns_/nstest.html`. A compressed PCAP writer can retain less than one page indefinitely during idle input; the integration fixture sends enough entropy to flush pages. An early failed read of an empty file can remain cached in the viewer. This is an upstream behavior and is not presented as immediate low-rate packet availability.
