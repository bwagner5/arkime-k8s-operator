# Kubernetes pod enrichment implementation plan

Status: implementation added, 2026-10-05; runtime capture/Cilium/performance qualification remains outstanding. See [implementation and deployment guide](kubernetes-pod-enrichment.md). Administrator-provisioned RBAC is used; no cluster-wide grants are managed by the Operator.

## Recommendation

Add a Go `pod-enricher` binary to this repository. Run it as a sidecar of the central WISE workload, watching Kubernetes Pods and writing a tagger-format file to a shared `emptyDir`. WISE reloads that file and capture uses its existing `wise.so` integration. Record the source and destination workload pods separately.

The pod identity comes from the session endpoint IPs, never from the capture pod's downward API metadata. One cluster-wide pod watch covers both local and remote endpoints. A node-local watch would miss the remote endpoint of cross-node sessions.

```text
kube-apiserver: Pod list/watch
              |
        Go pod-enricher
              |
     atomic pods.tagger replacement
              |
        WISE file source <---- wise.so on every capture node
                                      |
                           ip.src / ip.dst lookups
                                      |
                    k8s.src.* / k8s.dst.* session fields
```

This reuses the Operator's existing WISE service, capture plugin configuration, and viewer plugin integration. No database credentials or privileged host access are needed by the Go process. Package the watcher separately from the Operator manager so pod churn does not trigger ArkimeCluster reconciliation or bypass the manager's configured namespace scope.

## Capture prerequisites: qualify these first

Metadata cannot recover a pod address that is absent from the captured session.

* Cilium's default VXLAN port is UDP 8472; the checked-out Arkime `capture/parsers/vxlan.c` registers 4789 and VXLAN-GPE 4790. Geneve registers 6081. Verify the actual deployed image and Cilium settings, not just these checkouts. For physical-interface capture, add and test 8472 support in Arkime (or configurable VXLAN ports), then qualify that image with the Operator. This is a small change to the existing C parser; the new Kubernetes component remains Go.
* Alternatively qualify capture on the decapsulated tunnel interface, such as `cilium_vxlan`. Do not assume that interface covers same-node traffic or all pod egress. Interface selection depends on the actual Cilium datapath, encryption, NAT, and offload settings.
* Physical-interface capture alone generally misses same-node pod traffic. Qualify pod-facing capture points for same-node traffic and pre-SNAT egress. Dynamic pod interfaces may require a separate discovery/capture enhancement; a static interface list is not a complete solution. Do not assume `any` works with the Operator's current `tpacketv3` reader.
* Capturing before Service DNAT can show a Service IP rather than the selected backend pod; capturing after SNAT can show a node IP instead of the originating pod. Never select an arbitrary pod behind a Service or node IP. Test before/after translation explicitly.
* Avoid collecting both encapsulated and decapsulated copies without measuring duplication. Test both directions of a TCP conversation; Cilium security identities carried in the tunnel are not unique pod IDs. Verify Arkime's VLAN/VNI session-key settings do not split the conversation unexpectedly.
* An encrypted underlay requires a capture point where plaintext is visible.

Required initial proof: send traffic between two named pods on different nodes and confirm Arkime records their **pod IPs** in `ip.src` and `ip.dst`, with normal application decoding. Repeat on one node and for Internet egress before claiming those paths are supported.

## Go component

Proposed layout:

```text
cmd/pod-enricher/main.go
internal/podenrichment/informer.go
internal/podenrichment/index.go
internal/podenrichment/render.go
internal/podenrichment/writer.go
internal/podenrichment/health.go
```

Use this repository's existing client-go version, shared informer machinery, `net/netip`, and `os` file operations. No polling shell script and no Cilium API dependency are needed for ordinary pod-IP attribution.

1. List/watch Pods across all namespaces by default. Optional namespace/label restrictions must be described as reducing attribution coverage. Watch Pod objects rather than EndpointSlices, since direct pod traffic and pods outside Services also matter.
2. Wait for initial informer synchronization before publishing a complete inventory. Reconnect and relist through client-go. Never publish an empty inventory because a list failed.
3. Retain only required fields in the cache: UID, namespace/name, node name, host-network flag, phase/deletion state, and all `status.podIPs`, falling back to `status.podIP`. Avoid retaining pod specs and annotations unnecessarily. Watch updates to IP assignments, not just creation events; do not require Pod Ready, since unready pods can send traffic.
4. Maintain UID-to-address and address-to-owner indexes. Support IPv4 and IPv6, canonicalize addresses, and output exact addresses rather than node PodCIDRs. Handle delete tombstones and delayed deletes by UID so deletion of an old pod cannot erase a new owner.
5. Exclude `hostNetwork` pods from IP attribution. Multiple such pods share a node address, including the capture pod. If two eligible pod UIDs claim an address, omit that address and report ambiguity until it resolves. Never emit both owners as if both were certain.
6. Retain terminating pods while still present with usable IPs; remove on deletion or terminal phase. Do not keep deleted IP ownership for an arbitrary grace period. This policy favors avoiding old ownership on new traffic but cannot solve historical lookup timing.
7. Coalesce events with a bounded debounce: initial target 250 ms, maximum publication delay 1 second while healthy. Sort output deterministically and skip unchanged snapshots. Rebuild from the synchronized cache periodically as a consistency check.
8. Write a temporary file in the destination directory, flush/close, and rename it atomically over `pods.tagger`. Use directory mounts, not `subPath`. Retain the last valid file after write errors. Include fixed field definitions even when there are zero pods.
9. Expose liveness, readiness, and metrics for initial sync, watch/relist failures, successful API contact, inventory size, ambiguous IPs, writes, and write failures. Publication age alone is not API freshness: unchanged clusters do not need writes. Verify freshness using successful list/watch establishment/bookmarks or a bounded periodic API check.
10. On sustained inability to verify freshness (proposed configurable default 60 seconds), publish a valid header-only file to stop serving indefinitely stale identities, mark readiness degraded, and retain the last good snapshot separately for diagnostics. Recover only after a successful synchronization. Existing capture caches can remain stale for their TTL; process death also requires readiness failure/restart. Document this as bounded best effort, not a hard consistency guarantee.

Provide `--once` to render a snapshot and exit for debugging/manual deployment, plus `--output`, `--cluster-name`, debounce, freshness and probe options. Normal in-cluster execution uses a projected ServiceAccount token; local debug uses an explicitly selected kubeconfig context.

## File format and endpoint fields

Export cluster name, namespace, pod name, pod UID, and node name. Keep labels and owner traversal out of v1; ReplicaSet-to-Deployment resolution can be a later opt-in feature.

Illustrative file excerpt (the implementation emits definitions for every exported field):

```text
#field:k8s.pod.name;kind:termfield;db:k8s.pod.name;friendly:Matched Pod Name
#field:k8s.namespace;kind:termfield;db:k8s.namespace;friendly:Matched Namespace
10.42.1.23;k8s.pod.name=checkout-7c9f8b6d5-x2abc;k8s.namespace=shop
10.42.2.41;k8s.pod.name=payments-6d8c9f7b4-q8xyz;k8s.namespace=shop
```

The file associates an address with metadata. Capture determines which side matched. Define all generic and directional fields at startup through `[custom-fields]`, then generate remapping for every metadata field:

```ini
[custom-fields-remap]
k8s.pod.name=ip.src=k8s.src.pod.name;ip.dst=k8s.dst.pod.name
k8s.namespace=ip.src=k8s.src.namespace;ip.dst=k8s.dst.namespace
k8s.pod.uid=ip.src=k8s.src.pod.uid;ip.dst=k8s.dst.pod.uid
k8s.node.name=ip.src=k8s.src.node.name;ip.dst=k8s.dst.node.name
k8s.cluster=ip.src=k8s.src.cluster;ip.dst=k8s.dst.cluster
```

These mappings are supported by the checked-out `capture/field.c` and both WISE and tagger pass the matched field to that mechanism. Qualify against the Operator's pinned image. Generate a viewer session-detail section and make the directional fields searchable, for example `k8s.src.namespace == shop && k8s.dst.pod.name == payments-6d8c9f7b4-q8xyz`.

`src` and `dst` follow Arkime session orientation; each endpoint can send and receive packets. They do not mean permanent sender/receiver roles. Generic IP lookups may also come from X-Forwarded-For or other fields. Only matches on `ip.src` and `ip.dst` may populate directional pod fields; generic matches must not be displayed as endpoint attribution. Test this specifically.

Start without extra `tags` values: structured fields preserve endpoint identity better. Validate configurable string values against tagger delimiters (`;`, newlines, etc.); do not interpolate arbitrary labels. Use UID plus namespace/name to distinguish recreations.

## WISE and Operator integration

Proposed API (new fields, not valid in the current CRD):

```yaml
spec:
  wise:
    enabled: true
  enrichment:
    kubernetes:
      enabled: true
      clusterName: home
      captureCacheSeconds: 5
      maxStaleSeconds: 60
```

Require WISE when enabled, with a clear validation error if explicitly disabled. Reserve generated field names and source configuration keys to prevent conflicting overrides. Expose watcher resource requests/limits and an image override using existing Operator conventions.

Generate this WISE source:

```ini
[file:kubernetes-pods]
file=/var/run/arkime-kubernetes/pods.tagger
type=ip
format=tagger
```

* Use one WISE replica initially, matching the existing resource builder. Add a native restartable init sidecar for `pod-enricher` and a startup probe that waits for synchronized publication. This fits the repository's Kubernetes floor. WISE must not start before the file exists: the checked-out file source declines to load missing files. Include fixed definitions even for an empty inventory.
* Mount the shared directory read/write in the watcher and read-only in WISE. Verify repeated atomic renames on the pinned Arkime release: the checkout has file-watch rearming logic that must not be assumed present in every image.
* Give the WISE pod a dedicated ServiceAccount with a token projected only into the watcher. Keep automatic token mounting disabled for application containers. Grant only Pod get/list/watch across the selected scope. No Secrets, Cilium CRDs, or Nodes access is required for v1; the node name comes from the Pod.
* Manage cluster-wide RBAC explicitly. Namespaced ArkimeClusters cannot own ClusterRoleBindings through ordinary owner references. Use unique names and finalizer cleanup, and ensure the Operator has the required RBAC create/bind permissions without broad privilege-escalation grants. Alternatively support an administrator-provisioned binding for restricted installations.
* Permit watcher-to-apiserver egress in enabled NetworkPolicies, accounting for the actual API endpoint and service translation. Preserve capture-to-WISE reachability for host-network capture pods, which cannot simply be assumed to match pod selectors.
* Set capture `wiseCacheSecs=5` as the initial performance/accuracy tradeoff. Its checked-out default is 600 seconds, inappropriate for pod churn. This is a capture-wide WISE cache setting and affects other sources too; make it configurable and load-test API traffic to WISE. File-source data itself bypasses the WISE result cache in the checkout.
* Readiness should reflect usable watcher data as well as WISE availability. Surface enrichment degradation separately from capture health, and verify that loss of enrichment does not indefinitely block capture/session persistence.
* Predefine fields and roll Viewer/capture once when enabling the feature. Pod changes update only the data file, never ConfigMaps, pod templates, or workload hashes. Restarting capture for every pod event is unacceptable.
* Update API/deepcopy/CRD generation, resource/config builders, reconciliation and cleanup, chart RBAC, build/release packaging for the Go binary and image, docs, and examples. Preserve existing uncommitted work in the Operator repository.

For manually managed Arkime, publish an example WISE Deployment containing the same watcher sidecar, volume, ServiceAccount, and RBAC. A second implementation is unnecessary. If WISE is later replicated, each replica can build its own local snapshot; do not elect one writer across separate emptyDir volumes.

## Accuracy contract and alternatives

This is **live, best-effort endpoint enrichment**, not historical pod attribution. Both tagger and WISE perform lookups around session save in the checkout. A long session can therefore be looked up after its original pod is gone, and fields may accumulate across linked saves. A short cache reduces stale matches but does not eliminate IP reuse races. Never promise exact pod ownership for every session with only an IP-to-current-pod file.

For timestamp-correct attribution, a later design must retain pod-IP ownership intervals and query with session/packet timestamps and cluster identity; stock IP-only tagger/WISE lookups do not carry that context. Delayed offline PCAP import also requires that historical path. This would need a capture-side integration change or a timestamp-aware enrichment pipeline. Cilium/Hubble/connection-tracking correlation may be needed where NAT has removed pod IPs, but is a separate feature and cannot be replaced by an apiserver watch.

Limit v1 to one Kubernetes cluster per enrichment domain. A cluster field in results does not disambiguate overlapping IPs from multiple clusters sharing one IP-only WISE lookup service. Multi-cluster operation requires separate services/domains or cluster-scoped lookup keys.

The legacy tagger is a reasonable fallback for slowly changing inventories: the Go process could publish the existing `type/data/fields/md5` document format centrally and capture nodes would use `tagger.so`. It does not directly watch arbitrary files mounted on capture nodes. Its one-minute polling and save-time lookup make it a worse default for short-lived Kubernetes pods. Do not implement both backends in the first release.

## Delivery and acceptance

1. **Capture proof:** real Cilium VXLAN 8472 PCAP, confirmed inner IPs and bidirectional session behavior. Qualify the deployed Arkime image, remapping, and WISE file replacement behavior before selecting a supported version.
2. **Go watcher:** table-driven unit/race tests for add/update/delete/tombstones, dual stack, duplicate IP ownership, host networking, terminal pods, reuse, empty inventory, delimiter validation, deterministic snapshots, write failure, and informer restart/relist. Fault-test freshness handling without confusing a quiet watch with an outage.
3. **Operator wiring:** resource/config/envtest coverage for feature enable/disable, dedicated RBAC/token mounts, startup ordering, generated fields, NetworkPolicy, finalizer cleanup, and rejection of incompatible settings. Verify pod inventory churn causes no capture rollout. Run the repository's generation, tests, and verify targets.
4. **Cilium integration:** two workload pods across nodes and on one node, direct Pod IP and Service traffic, TCP/UDP and supported IP families. Assert both endpoint names/namespaces/UIDs in actual saved Arkime sessions and never the capture pod's identity. Exercise pod recreation, API outage, watcher/WISE restart, repeated file replacement, cache expiry, unrelated XFF matches, long sessions, and capture after SNAT. Record unknown/incorrect windows instead of hiding them.
5. **Performance:** measure list/watch load, render cost, WISE memory, lookup QPS and session-save latency at expected pod count/churn. Target new lookups seeing changed metadata within 10 seconds under healthy conditions, accounting for debounce, reload and a 5-second cache; validate before advertising this target. It is not a guarantee about already enriched sessions.

Existing Operator e2e qualification uses kindnet, so it cannot substitute for the Cilium overlay acceptance gate. Runtime validation remains outstanding; research for this plan inspected source and documentation only.

## References

* [Arkime tagger](https://arkime.com/tagger): database distribution and one-minute refresh.
* [Arkime tagger format](https://arkime.com/taggerformat): field definitions and row syntax.
* [Cilium routing](https://docs.cilium.io/en/stable/network/concepts/routing/): encapsulation and default ports.
* [Cilium masquerading](https://docs.cilium.io/en/stable/network/concepts/masquerading/): loss of pod source addresses at NAT.
* Local Arkime source inspected: `capture/plugins/{tagger,wise}.c`, `capture/field.c`, `capture/parsers/{vxlan,geneve}.c`, and `wiseService/{source.file,simpleSource,wiseSource}.js`.
* Local Operator source inspected: `api/v1alpha1/types.go`, `internal/{config/config,resources/resources,controller/controller}.go`, `cmd/manager/main.go`, and `compatibility.json`.
