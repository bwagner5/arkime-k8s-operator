# Kubernetes pod enrichment

The Go `pod-enricher` sidecar, enabled by default with WISE, maps live Pod IPs to cluster, namespace,
pod name, UID and node name. Capture remaps matches into `k8s.src.*` and
`k8s.dst.*`. Source/destination follow session orientation, not permanent traffic
sender/receiver roles. Generic matches (including XFF) remain generic and are
excluded from the endpoint detail view.

This is experimental until the runtime gates below are completed. No pinned
Arkime image has been newly qualified by this implementation. A current-IP map
cannot provide timestamp-correct attribution for long sessions, IP reuse, linked
saves or delayed PCAP import. NAT, encryption and capture placement can remove
the pod address entirely. Never infer a pod from a Service IP or shared node IP.
Use one Kubernetes cluster per WISE enrichment domain; a cluster field does not
resolve overlapping IP ranges.

## Configuration

WISE automatically deploys its Kubernetes pod enricher. No separate image,
cluster-name or ServiceAccount configuration is needed:

```yaml
spec:
  wise:
    enabled: true
```

The equivalent explicit configuration is:

```yaml
spec:
  wise:
    enabled: true
    kubernetesEnrichment:
      enabled: true  # optional; defaults to true
      # image: registry.example/pod-enricher:custom  # optional override
```

Set `spec.wise.kubernetesEnrichment.enabled: false` to turn off Kubernetes
enrichment while keeping WISE and its other sources. Setting `spec.wise.enabled:
false` turns both off. WISE itself retains its existing enabled-by-default
behavior when its `enabled` field is omitted.

The default watcher image is `ghcr.io/bwagner5/arkime-pod-enricher:<operator-version>`.
Both the release binary and container carry that version. The Arkime capture
version is independent. The `image` field above overrides the default for one
ArkimeCluster; chart value `podEnricherImage` or manager flag
`--pod-enricher-image` overrides the installation default. Development builds use
`0.0.0-dev`; use an explicit locally built image when running from source.

The exported `k8s.cluster` identity is the ArkimeCluster's `namespace/name`.
The Operator creates a dedicated ServiceAccount, a Pod-only ClusterRole and a
ClusterRoleBinding for each instance. Tokens are projected only into the watcher.
RBAC names include the CR UID; a finalizer removes cluster-scoped grants when
enrichment is disabled or the ArkimeCluster is deleted. Keep the Operator running
until cleanup completes. The Operator's own chart grants RBAC management but no
`bind` or `escalate` bypass: it already holds the Pod get/list/watch permissions
that it delegates. No manual RBAC step is needed for an Operator installation.
The standalone WISE example still uses its own administrator-provisioned RBAC.

If `spec.networkPolicy` is enabled, supply `apiEgress` and `captureIngress` under
`spec.wise.kubernetesEnrichment`. These standard NetworkPolicy rule arrays are
scoped to WISE. Use actual API endpoint CIDRs/ports accounting for CNI Service
translation, and capture-node source CIDRs on TCP 8081 for host-network capture.
The Operator cannot infer these rules from namespace pod selectors. Existing
internal traffic and DNS rules remain.

Upgrading from the initial experimental API requires moving overrides from
`spec.enrichment.kubernetes` into `spec.wise.kubernetesEnrichment`, removing
`clusterName` and `serviceAccountName`. Previously provisioned manual RBAC is no
longer used by the Operator and can be removed after its WISE rollout completes.
Enrichment is now on by default: set the new `enabled: false` explicitly if you
want to retain the old unenriched behavior. Enablement changes roll workloads;
pod inventory changes do not.

The native restartable init sidecar waits for synchronized publication before
WISE starts. Only it mounts the projected API token. Both containers mount the
inventory directory (never `subPath`), with WISE read-only. The sidecar inherits
no database credentials. Pod updates change only the file, never ConfigMaps or
capture templates. Enabling/disabling changes generated configuration and rolls
capture/viewer normally. Generated `k8s.*` fields, Kubernetes view/source, and
`wiseCacheSecs` reject conflicting config or secret overrides.

`EnrichmentReady` follows WISE deployment availability including the sidecar
probe, independently of `CaptureReady`. The WISE Service publishes unready
addresses so a running WISE can serve the withdrawn, empty inventory during an
API outage. This also exposes starting/restarting WISE endpoints; capture must
tolerate connection failures. Loss of enrichment is not a capture startup gate.

## Freshness and diagnostics

Defaults: debounce 250ms, maximum debounce 1s, capture-wide WISE cache 5s,
maximum staleness 60s. The cache affects all WISE sources. Repeated complete
synchronizations at one third of the staleness interval verify API freshness and
rebuild the inventory, including on quiet clusters. This intentionally incurs
periodic full-list/watch establishment load; measure it at your pod count.
Snapshots are sorted and unchanged snapshots are not rewritten. Host-network,
terminal and ambiguous owners are omitted; terminating pods remain until deletion
or terminal phase. Dual-stack exact IPs are supported, without Ready filtering.

At the freshness deadline, publish field definitions without identities and mark
readiness false. Preserve `pods.tagger.last-good` for diagnostics. Recover through
a new fully synchronized informer. Failed atomic writes retain the previous
file; failure to withdraw stale data terminates the process for restart. A
process crash, WISE reload delay, and capture caches can extend the stale window:
this is bounded best effort, not a hard consistency guarantee. Initial list
failure never produces a purportedly healthy empty snapshot.

The watcher serves `/livez`, `/readyz`, and Prometheus `/metrics` on port 8082.
Metrics include initial sync, latest successful synchronization, API failures,
address count, ambiguous IPs, writes and write failures. Secure metrics with
NetworkPolicy if needed. Namespace and label CLI filters reduce attribution
coverage; the Operator uses the full cluster by default.

For local debugging, select a context explicitly:

```sh
go run ./cmd/pod-enricher --kube-context=your-context --once \
  --cluster-name=home --output=/tmp/pods.tagger
```

In-cluster mode uses only the projected account token. `--kubeconfig` requires
`--kube-context`. `--probe-address`, `--debounce`, `--max-delay`, `--refresh` and
`--max-stale` are available; maximum staleness must be at least twice refresh.
Manual WISE installations can use `examples/wise-pod-enrichment.yaml` and the
same RBAC example. Configure capture `wise.so`, viewer `wise.js`, `wiseURL`, and
copy the Operator's generated custom-field/remap/view sections into their INI
files. No second watcher implementation is required.

## Runtime acceptance still required

The accompanying Arkime source change registers VXLAN UDP 8472 alongside 4789.
Its regression fixture is derived from the existing VXLAN fixture, **not real
Cilium traffic**. It does not qualify a deployed image. Before claiming support:

- Use real Cilium 8472 PCAPs and confirm bidirectional inner pod IPs, application
  decoding and VNI/VLAN session behavior. Physical interfaces miss some same-node
  traffic; `cilium_vxlan` also needs coverage verification. Do not assume `any`
  works with tpacketv3. Encrypted traffic needs a plaintext capture point.
- Exercise cross-node and same-node TCP/UDP, supported IP families, Service DNAT,
  pre/post SNAT egress, and duplicate encapsulated/decapsulated capture.
- On the exact image, verify repeated atomic WISE file replacement, all 15 custom
  fields and source/destination remaps, including unrelated XFF matches. Assert
  saved endpoint names/namespaces/UIDs, never the capture pod identity.
- Exercise recreation, IP reuse, API outage, watcher/WISE restarts, cache expiry,
  long sessions and offline PCAP. Record unknown and incorrect attribution windows.
- Measure API list/watch load, render time/memory, WISE QPS/memory and session-save
  latency at realistic pod count/churn. The proposed <10s visibility target is
  unverified and is not a guarantee about already-enriched sessions.

Existing kindnet e2e coverage cannot satisfy the Cilium gate. No cluster changes
or deployment are performed by the local implementation tests.

Local benchmark (2026-10-05, Apple M5 Pro, Go 1.27.1): rendering a prebuilt
10,000-pod index took approximately 5.3 ms/op and 10.6 MB allocated/op.
Run `go test ./internal/podenrichment -run '^$' -bench BenchmarkRender10000 -benchmem`
to repeat. This excludes index construction, API transfer, fsync and WISE reload;
it is not an end-to-end performance qualification.
