# Releasing

`make release-snapshot` builds both Linux architectures, ko images, versioned chart packages, rendered manifests, compatibility metadata and checksums without publishing. It works before the first Git tag and uses `0.0.0-dev`; a local Docker runtime is required. Outputs live in `_artifacts/` and `dist/`.

Run `make ci-verify`, the real packet tests, and the outstanding qualification matrix before tagging a release. An operator tag `v0.1.0` maps to image/chart version `0.1.0`; Arkime's version remains independently pinned. The tag workflow authenticates to GHCR, invokes GoReleaser and then pushes the exact packaged charts. Registry/chart versions are immutable. There is no moving latest release alias.

The release remains a draft until image architectures, both OCI charts and installation of those exact artifacts are verified. Review the compatibility report before publishing the draft. For a partial failure, inspect which immutable artifacts exist and resume only missing steps; do not overwrite published content under the same tag. Rebuild a new version if content must change.

Upgrade the CRD chart first, then the operator chart. CRDs are owned exclusively by the CRD chart, rendered from Go-generated sources with `helm.sh/resource-policy: keep`. Uninstalling the CRD chart retains CRs/CRDs. To reinstall a retained CRD, use the same Helm release identity or deliberately restore its ownership metadata after backup; never delete the CRD merely to fix Helm ownership.

Controller RBAC permits creating host-access workloads and reading Secrets. `watchNamespaces` limits cache scope but is not a reduction of the published cluster-wide RBAC. Use only a trusted operator service account. Separate installations must not overlap watched namespaces or managed database schemas without explicit coordination.
