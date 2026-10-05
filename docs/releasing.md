# Releasing

`make release-snapshot` builds both Linux architectures, ko images, versioned chart packages, rendered manifests, compatibility metadata and checksums without publishing. It works before the first Git tag and uses `0.0.0-dev`; a local Docker runtime is required. Outputs live in `_artifacts/` and `dist/`.

Run `make ci-verify`, the real packet tests, and the outstanding qualification matrix before tagging a release. An operator tag `v0.1.0` maps to image/chart version `0.1.0`; Arkime's version remains independently pinned. The tag workflow authenticates to GHCR, invokes GoReleaser and then pushes the exact packaged charts. Registry/chart versions are immutable. There is no moving latest release alias.

GoReleaser drafts the release; the workflow publishes it only after image architectures and both OCI charts are verified. A draft keeps the GitHub release unpublished; container images and any successfully pushed OCI charts can already be public when a later step fails. Review the compatibility report after publication and yank the release if the qualification matrix fails. For a partial failure, inspect which immutable artifacts exist and resume only missing steps; do not overwrite published content under the same tag. Rebuild a new version if content must change.

`make generate` uses `go tool -modfile=tools/go.mod controller-gen` to generate the CRD directly from `api/v1alpha1` into `charts/arkime-k8s-operator-crds/templates`. Edit the Go types and Kubebuilder markers, then regenerate; do not edit the chart CRD by hand. `make verify-generated` runs the same generation target in a temporary copy and checks for drift. Tool versions are pinned with Go tool directives in `tools/go.mod`; update controller-gen with `go get -modfile=tools/go.mod -tool sigs.k8s.io/controller-tools/cmd/controller-gen@<version>`.

The operator chart renders the same generated CRD by default (`crds.enabled`), so a single chart is a complete installation. `make generate` copies the generated CRD into `charts/arkime-k8s-operator/files`, where the chart emits it untemplated; do not edit either copy. Set `crds.enabled=false` to own CRDs with the separate CRD chart instead, and never install both owners at once. In that split mode, upgrade the CRD chart first, then the operator chart. CRDs are rendered from Go-generated sources with `helm.sh/resource-policy: keep`. Uninstalling the CRD chart retains CRs/CRDs. To reinstall a retained CRD, use the same Helm release identity or deliberately restore its ownership metadata after backup; never delete the CRD merely to fix Helm ownership.

Controller RBAC permits creating host-access workloads and reading Secrets. `watchNamespaces` limits cache scope but is not a reduction of the published cluster-wide RBAC. Use only a trusted operator service account. Separate installations must not overlap watched namespaces or managed database schemas without explicit coordination.

The release also builds the `pod-enricher` Linux amd64/arm64 binary and `ghcr.io/bwagner5/arkime-pod-enricher` image at the same release version. The release workflow inspects both published image manifests before publishing the draft release. The manager defaults enrichment to the pod-enricher image at its own release version; `spec.wise.kubernetesEnrichment.image` optionally overrides it. Both manager build paths inject the release version.

## Recover a chart-push failure

CI pins Helm 4.2.4, which includes the [token-auth push-scope fix](https://github.com/helm/helm/pull/31211) missing in 4.2.0. A `failed to perform "Tag" ... not found` error happens in registry publication, after the binaries/images may already have succeeded. The message alone does not distinguish registry/authentication causes; do not delete images or retag a release to retry.

After merging the recovery workflow, run **Recover release charts** from the branch containing the fix, with the existing draft tag (for example `v0.1.2`). It downloads the original chart archives and checksums from that draft, verifies the existing image references, publishes only missing charts, pulls them back and compares bytes, then publishes the GitHub draft. A different existing chart, checksum mismatch, permission error or network failure stops recovery. It never rebuilds binaries/images or repackages charts. Do not rerun the old tag workflow: it still uses its old Helm version and reruns GoReleaser.

For local chart-only recovery, install Helm 4.2.4 and authenticate to GHCR with package read/write access, then:

```sh
gh release download v0.1.2 --repo bwagner5/arkime-k8s-operator \
  --pattern 'arkime-k8s-operator*-0.1.2.tgz' --pattern checksums.txt \
  --dir _artifacts/recovery
make publish-charts VERSION=0.1.2 \
  CHART_DIR=_artifacts/recovery CHECKSUMS=_artifacts/recovery/checksums.txt
```

This target does not publish the GitHub draft. Verify both image references and charts before publishing it. Use a new release version when artifact contents need to change.
