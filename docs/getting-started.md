# Install Arkime and capture your first traffic

This walkthrough installs a node sensor, a viewer, and a TLS-protected OpenSearch database, then verifies that you can search a session and download its packets. You can use an existing Kubernetes cluster and database, or follow the optional installation steps. Traefik adds browser HTTPS and optional UDP/TZSP reception later in the guide.

**TLS coverage:** database connections and OpenSearch node transport use private certificates. Optional browser HTTPS terminates at Traefik. The current operator still uses HTTP between Arkime components, including the central viewer and capture-local viewers. Cert-manager alone does not encrypt those connections. See [TLS and private CAs](tls.md) for the exact coverage and remaining implementation work.

Run commands from the repository root in **Bash**, keeping the same shell for variables. The examples use namespace `arkime`, cluster name `home`, and Kubernetes DNS suffix `cluster.local`.

## 1. Prepare Kubernetes

You need `kubectl`, Helm 3+, Go matching `go.mod`, `ko`, Python 3, and access to a container registry your nodes can pull from. The optional local cluster also needs Docker and kind. Use Kubernetes 1.34 or later with Linux worker nodes and a working CNI/DNS. The OpenSearch example needs three 20 GiB PVCs and at least 6 GiB memory for database pods; allow additional capacity for bootstrap, Arkime, and Kubernetes. For the local walkthrough, give Docker about 12 GiB memory and 4 CPUs or more.

If you already have a cluster, select its context and skip cluster creation. Otherwise create a disposable cluster with two workers:

```sh
kind create cluster --name arkime-demo --image kindest/node:v1.34.0 \
  --config test/e2e/kind.yaml
kubectl config use-context kind-arkime-demo
```

Confirm the selected cluster and storage:

```sh
kubectl config current-context
kubectl get nodes -o wide
kubectl get storageclass
kubectl create namespace arkime
kubectl label namespace arkime pod-security.kubernetes.io/enforce=privileged --overwrite
mkdir -p _artifacts/getting-started
```

Node capture needs host networking, hostPath storage, and capture capabilities. The OpenSearch operator also uses an init container to set `vm.max_map_count`. Use this dedicated namespace for the walkthrough. On an existing cluster, provide a default dynamic StorageClass, or set `spec.nodePools[].persistence.pvc.storageClass` in the OpenSearch example. Kind includes a local provisioner; its disks disappear when the kind cluster is deleted.

## 2. Install cert-manager and create a private CA

Skip installing cert-manager if your cluster already has it. The [upstream Helm installation](https://cert-manager.io/docs/installation/helm/) uses:

```sh
helm upgrade --install cert-manager oci://quay.io/jetstack/charts/cert-manager \
  --version v1.21.2 --namespace cert-manager --create-namespace \
  --set crds.enabled=true --wait --timeout 5m
kubectl apply -f examples/getting-started/issuer.yaml
kubectl -n arkime wait certificate/arkime-root-ca --for=condition=Ready --timeout=120s
kubectl -n arkime wait issuer/arkime-ca --for=condition=Ready --timeout=120s
```

This creates a namespaced CA issuer. It can issue certificates for private Service DNS names without public DNS or an ACME challenge. Keep the `arkime-root-ca` Secret's private key restricted to certificate administration. Applications receive only its public certificate:

```sh
kubectl -n arkime get secret arkime-root-ca \
  -o go-template='{{index .data "tls.crt" | base64decode}}' \
  > _artifacts/getting-started/arkime-root-ca.crt
kubectl -n arkime create secret generic arkime-database-ca \
  --from-file=ca.crt=_artifacts/getting-started/arkime-root-ca.crt \
  --dry-run=client -o yaml | kubectl apply -f -
```

If using your organization's issuer, change the example `issuerRef` values and supply its CA chain instead. CA rollover and application certificate reloads are separate from certificate issuance; see [rotation](tls.md#renewal-and-ca-rotation).

## 3. Choose your database

### Option A: use an existing database

Use a dedicated OpenSearch/Elasticsearch installation or a deliberately allocated Arkime index prefix. Obtain its HTTPS endpoint, CA certificate chain, and credentials with permission to initialize Arkime's schema and write/query its indices. Do not use `Initialize` against an existing Arkime installation; migration/adoption needs the [operations procedure](operations.md).

Create `arkime-database` from a file containing exactly `username:password`, without a trailing newline. Create `arkime-database-ca` from the database's CA, replacing the walkthrough CA from step 2 if necessary:

```sh
kubectl -n arkime create secret generic arkime-database \
  --from-file=basicAuth=/path/to/database-basic-auth
kubectl -n arkime create secret generic arkime-database-ca \
  --from-file=ca.crt=/path/to/database-ca-chain.pem \
  --dry-run=client -o yaml | kubectl apply -f -
```

In step 5, change `database.engine` and `database.endpoints`. Keep hostname verification enabled: the URL hostname must appear in the database certificate's SANs. Skip the rest of step 3.

### Option B: install OpenSearch with its operator

This example pins operator/chart **2.8.0** and OpenSearch **3.3.2**. It intentionally uses that release's `opensearch.opster.io/v1` API. Do not mix it with the changed API/security defaults on upstream `main`. See the [versioned operator guide](https://github.com/opensearch-project/opensearch-k8s-operator/blob/v2.8.0/docs/userguide/main.md) and [compatibility matrix](https://github.com/opensearch-project/opensearch-k8s-operator/blob/v2.8.0/README.md#compatibility).

```sh
helm repo add opensearch-operator https://opensearch-project.github.io/opensearch-k8s-operator/
helm repo update
helm upgrade --install opensearch-operator opensearch-operator/opensearch-operator \
  --version 2.8.0 --namespace opensearch-system --create-namespace \
  --wait --timeout 5m
kubectl apply -f examples/getting-started/opensearch-certificates.yaml
kubectl -n arkime wait certificate/opensearch-node certificate/opensearch-admin \
  --for=condition=Ready --timeout=120s
```

The certificates cover the database's Service and node DNS names. The node certificate supports server/client authentication; the separate admin certificate is used by the OpenSearch operator to configure security. Arkime uses HTTPS with basic authentication, not the OpenSearch admin certificate.

Create a random database password and the matching security configuration. Operator 2.8.0 needs both the plaintext credential Secret and a bcrypt hash in `internal_users.yml`; setting only the credential Secret is insufficient. The [setup helper](../hack/setup-opensearch.sh) installs its dependencies in a local virtual environment, downloads the release's security template, and applies all three Secrets without printing credentials:

```sh
bash hack/setup-opensearch.sh
kubectl apply -f examples/getting-started/opensearch.yaml
kubectl -n arkime get opensearchcluster,pods,pvc
```

Run the setup helper once for a fresh installation. It grants Arkime database-admin permissions to make this first installation self-contained. Before a shared/production deployment, replace these with scoped runtime credentials and separate `database.bootstrapAuth`; do not reuse this admin account across installations.

Wait for the StatefulSet to appear, then for all three replicas:

```sh
until kubectl -n arkime get statefulset/opensearch-nodes >/dev/null 2>&1; do sleep 5; done
kubectl -n arkime rollout status statefulset/opensearch-nodes --timeout=15m
kubectl -n arkime get jobs
```

Verify TLS, authentication, and cluster health before installing Arkime. In a second terminal:

```sh
kubectl -n arkime port-forward svc/opensearch 9200:9200
```

In the original terminal, save credentials into a private curl configuration and query the Service hostname through that forward:

```sh
(umask 077
 kubectl -n arkime get secret arkime-database \
   -o go-template='{{printf "user = %q\n" (index .data "basicAuth" | base64decode)}}' \
   > _artifacts/getting-started/database.curl)
curl --fail --silent --show-error --noproxy '*' \
  --config _artifacts/getting-started/database.curl \
  --cacert _artifacts/getting-started/arkime-root-ca.crt \
  --resolve opensearch.arkime.svc:9200:127.0.0.1 \
  'https://opensearch.arkime.svc:9200/_cluster/health?wait_for_status=yellow&timeout=60s'
rm _artifacts/getting-started/database.curl
```

Expect three nodes, `timed_out: false`, and green or yellow status. A TLS error, HTTP 401, or red cluster is a stop point: inspect OpenSearch pods and its security-config Job before proceeding. No `curl -k` is needed. Stop the database port-forward when done.

## 4. Build and install the Arkime operator

Generate the CRDs and build from this checkout:

```sh
make generate
```

For an existing cluster, publish to a registry you control (authenticate using your registry tooling first):

```sh
export KO_DOCKER_REPO=ghcr.io/YOUR_ACCOUNT/arkime-k8s-operator
OPERATOR_IMAGE=$(ko build --bare --platform=linux/amd64,linux/arm64 ./cmd/manager)
helm upgrade --install arkime-crds ./charts/arkime-k8s-operator-crds
helm upgrade --install arkime ./charts/arkime-k8s-operator \
  --namespace arkime-system --create-namespace \
  --set image.repository="${OPERATOR_IMAGE%@*}" \
  --set image.digest="${OPERATOR_IMAGE#*@}" --wait --timeout 5m
```

For the optional kind cluster, use this local-image branch **instead** of the registry branch:

```sh
OPERATOR_IMAGE=$(KO_DOCKER_REPO=ko.local ko build --bare --platform="linux/$(go env GOARCH)" ./cmd/manager)
kind load docker-image --name arkime-demo "$OPERATOR_IMAGE"
helm upgrade --install arkime-crds ./charts/arkime-k8s-operator-crds
helm upgrade --install arkime ./charts/arkime-k8s-operator \
  --namespace arkime-system --create-namespace \
  --set image.repository="${OPERATOR_IMAGE%:*}" \
  --set image.tag="${OPERATOR_IMAGE##*:}" --wait --timeout 5m
```

## 5. Select capture nodes and apply Arkime

Start with two workers. For kind:

```sh
kubectl label nodes arkime-demo-worker arkime-demo-worker2 arkime-capture=true --overwrite
```

On an existing cluster, substitute the worker names you want to capture on. Inspect the actual host interfaces (on each host, run `ip -brief link`). Set `capture.node.interfaces` accordingly: kind workers use `eth0`, while a physical host might use `eno1`. The same interface list must work on every selected node. Host capture sees only traffic traversing those interfaces; it does not automatically see all overlay traffic or decrypt TLS.

Copy the example, adjust the interface and database if needed, and apply it:

```sh
cp examples/getting-started/arkime.yaml _artifacts/getting-started/arkime.yaml
# Edit _artifacts/getting-started/arkime.yaml for your interfaces/database.
kubectl apply -f _artifacts/getting-started/arkime.yaml
kubectl -n arkime wait arkimecluster/home --for=condition=Ready --timeout=15m
kubectl -n arkime get pods,daemonsets,deployments,jobs
```

Each selected worker gets capture and a local viewer sharing `/var/lib/arkime-pcap`. Keep TCP 8005 reachable from the central viewer to the capture nodes, and reserve 8005/8006 on those hosts. OpenSearch stores searchable metadata; packet files stay on the capture node. Cont3xt and WISE are disabled here to keep the first capture setup focused; enable them after configuring the providers/enrichment you need.

If readiness fails, inspect the reason and bootstrap Job:

```sh
kubectl -n arkime describe arkimecluster home
SCHEMA_JOB=$(kubectl -n arkime get arkimecluster home -o jsonpath='{.status.schemaJob}')
kubectl -n arkime logs "job/$SCHEMA_JOB" --all-containers=true
kubectl -n arkime get events --sort-by=.lastTimestamp
```

After fixing a failed bootstrap dependency, explicitly retry:

```sh
kubectl -n arkime patch arkimecluster home --type=merge \
  -p "{\"spec\":{\"database\":{\"schema\":{\"retryToken\":\"$(date +%s)\"}}}}"
```

## 6. Open the viewer

With only this Arkime installation in the namespace, discover the viewer Service:

```sh
VIEWER_SERVICE=$(kubectl -n arkime get svc -l app.kubernetes.io/component=viewer -o jsonpath='{.items[0].metadata.name}')
kubectl -n arkime port-forward "svc/$VIEWER_SERVICE" 8005:8005
```

Leave that terminal running. In another terminal retrieve the initial login, for display only on your own terminal:

```sh
ADMIN_SECRET=$(kubectl -n arkime get arkimecluster home -o jsonpath='{.status.adminSecret}')
kubectl -n arkime get secret "$ADMIN_SECRET" \
  -o go-template='Username: {{index .data "username" | base64decode}}{{"\n"}}Password: {{index .data "password" | base64decode}}{{"\n"}}'
```

Open [http://localhost:8005](http://localhost:8005) and sign in. This localhost HTTP connection travels through the authenticated Kubernetes port-forward. Step 8 adds browser HTTPS without port-forwarding.

## 7. Prove that capture and packet retrieval work

A Ready pod is not proof of capture. Generate traffic across the selected host interface and look for it in Arkime. The following sends 1,024 UDP packets between the first two capture workers, with a recognizable payload and enough data to flush a compressed PCAP page:

```sh
CAPTURE_POD=$(kubectl -n arkime get pods -l app.kubernetes.io/component=node -o jsonpath='{.items[0].metadata.name}')
DESTINATION_IP=$(kubectl -n arkime get pods -l app.kubernetes.io/component=node -o jsonpath='{.items[1].status.hostIP}')
test -n "$CAPTURE_POD" && test -n "$DESTINATION_IP"
kubectl -n arkime exec "$CAPTURE_POD" -c local-viewer -- /opt/arkime/bin/node -e '
const socket = require("dgram").createSocket("udp4");
const payload = Buffer.alloc(1200);
let count = 0;
socket.bind(42042, function send() {
  require("crypto").randomFillSync(payload);
  payload.write("arkime-first-capture");
  socket.send(payload, 45999, process.argv[1], err => {
    if (err) throw err;
    if (++count === 1024) return socket.close();
    setTimeout(send, 2);
  });
});' "$DESTINATION_IP"
```

The destination does not need a UDP listener; an ICMP port-unreachable reply is normal. With one capture node, set `DESTINATION_IP` to another reachable host whose route crosses the chosen capture interface instead.

In the viewer:

1. Select **Last hour** and search `port.src == 42042 && port.dst == 45999`.
2. Allow up to a few minutes for the session to close and index; refresh the search.
3. Open the session and confirm that packet payload contains `arkime-first-capture`.
4. Download its PCAP and open it in Wireshark. Verify the same addresses, ports, and payload.

You now have a working capture-to-search-to-PCAP path. If sessions appear but packet retrieval fails, check the central viewer's access to the node's local viewer on TCP 8005 and the PCAP directory. If no sessions appear, check the interface, BPF, capture logs, and database writes:

```sh
kubectl -n arkime logs "$CAPTURE_POD" -c capture --tail=100
kubectl -n arkime logs "$CAPTURE_POD" -c local-viewer --tail=100
```

## 8. Optional: expose HTTPS through Traefik

Traefik handles HTTPS with Gateway API and UDP with its own `IngressRouteUDP`. Its [Gateway API provider](https://doc.traefik.io/traefik/reference/routing-configuration/kubernetes/gateway-api/) documents HTTPRoute support, while [IngressRouteUDP](https://doc.traefik.io/traefik/reference/routing-configuration/kubernetes/crd/udp/ingressrouteudp/) is the UDP integration used below. Do not select Arkime's `capture.external.exposure.mode: Gateway` for this Traefik recipe: that makes a Gateway API `UDPRoute`, a different resource.

You need a LoadBalancer implementation that supplies reachable addresses. On bare metal, use your existing load-balancer controller; kind alone does not provide one. You can still test HTTPS on kind by port-forwarding Traefik's TCP Service, but receiving UDP from outside requires a real UDP address/port mapping (kubectl port-forward does not support UDP).

If you already run Traefik, configure that installation with the needed providers and entrypoints rather than installing a second conflicting instance. For a new installation:

```sh
kubectl apply --server-side -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.6.1/standard-install.yaml
helm repo add traefik https://traefik.github.io/charts
helm repo update
helm upgrade --install traefik traefik/traefik \
  --version 41.6.0 --namespace traefik --create-namespace \
  -f examples/getting-started/traefik-values.yaml --wait --timeout 5m
kubectl apply -f examples/getting-started/web.yaml
kubectl -n arkime wait certificate/arkime-web --for=condition=Ready --timeout=120s
kubectl -n arkime wait gateway/arkime-web --for=condition=Programmed --timeout=5m
kubectl -n arkime patch arkimecluster home --type=merge -p \
  '{"spec":{"web":{"mode":"Gateway","host":"arkime.home.arpa","parentRef":{"name":"arkime-web","sectionName":"https"}}}}'
kubectl -n arkime get httproute -o yaml
kubectl -n traefik get svc
```

Expect the HTTPRoute's parent status to report `Accepted=True` and `ResolvedRefs=True`. The Gateway listener uses **8443**, matching Traefik's container entrypoint; its TCP Service exposes **443**. The supplied values create separate TCP and UDP Services.

Point `arkime.home.arpa` at the TCP Service's load-balancer IP in your private DNS (or workstation hosts file). Install `_artifacts/getting-started/arkime-root-ca.crt` as a trusted CA in your workstation/browser, then open [https://arkime.home.arpa](https://arkime.home.arpa). For a command-line verification with an IP address:

```sh
TRAEFIK_IP=192.0.2.10 # Replace with the TCP Service's actual load-balancer IP.
curl --cacert _artifacts/getting-started/arkime-root-ca.crt \
  --resolve "arkime.home.arpa:443:$TRAEFIK_IP" -I https://arkime.home.arpa/
```

A 401 is an authentication challenge and still demonstrates a verified TLS handshake. For kind without a load balancer, use `kubectl -n traefik port-forward svc/traefik 8443:443` and test `https://arkime.home.arpa:8443` with `--resolve arkime.home.arpa:8443:127.0.0.1`. This is only a connectivity test; normal browser use needs the configured hostname and port 443.

## 9. Optional: receive mirrored packets over UDP/TZSP

Keep node capture running and add one external receiver:

```sh
kubectl -n arkime patch arkimecluster home --type=merge -p \
  '{"spec":{"capture":{"external":{"enabled":true,"storage":{"volumeClaim":{"size":"20Gi"}},"exposure":{"mode":"ClusterIP"}}}}}'
until kubectl -n arkime get deployment -l app.kubernetes.io/component=external -o name | grep -q .; do sleep 5; done
EXTERNAL_DEPLOYMENT=$(kubectl -n arkime get deployment -l app.kubernetes.io/component=external -o jsonpath='{.items[0].metadata.name}')
kubectl -n arkime rollout status "deployment/$EXTERNAL_DEPLOYMENT" --timeout=10m
UDP_SERVICE=$(kubectl -n arkime get svc -l app.kubernetes.io/component=udp -o jsonpath='{.items[0].metadata.name}')
test -n "$UDP_SERVICE"
cat <<YAML | kubectl apply -f -
apiVersion: traefik.io/v1alpha1
kind: IngressRouteUDP
metadata:
  name: arkime-tzsp
  namespace: arkime
spec:
  entryPoints: [tzsp]
  routes:
    - services:
        - name: $UDP_SERVICE
          port: 37008
YAML
kubectl -n traefik get svc
```

Configure your packet mirror/exporter to send **TZSP-encapsulated Ethernet packets** to the UDP Service's external address, port **37008**. This is not NetFlow, sFlow, raw UDP payload capture, or a generic PCAP-over-UDP format. Restrict that address/port to the sender network. TZSP itself has no TLS or sender authentication; carry it over a trusted network or an encrypted tunnel when necessary.

Generate identifiable traffic at the mirrored source. In Arkime search that traffic and `node == ak-<cluster-hash>-external` (copy the exact node identity from the Stats page). Download a session's PCAP, as in step 7. This checks the real sender → Traefik → receiver path, not just the existence of Kubernetes resources. The operator does not track readiness of a manually created IngressRouteUDP. If packets do not arrive, check the UDP load balancer, firewall, Traefik logs, receiver capture logs, and exporter encapsulation.

To omit Traefik for UDP, set external exposure to `LoadBalancer` and send TZSP directly to the generated Arkime UDP Service. In-cluster senders can use its ClusterIP/DNS name. Configure `sourceRanges` and load-balancer annotations as appropriate for your infrastructure.

## 10. Keep the installation usable

Persist changes from the optional patch commands into your saved Arkime manifest before reapplying it. Back up OpenSearch, PCAP storage, the CA, and Arkime's generated authentication Secrets. Removing a capture block is intentionally blocked once storage is provisioned; disable it with `enabled: false` to keep historical packet access. See [operations](operations.md).

This guide's optional OpenSearch/Traefik/private-CA integration has configuration validation, but is not yet part of the repository's executed end-to-end qualification matrix. The automated test fixture uses a different disposable backend. Record successful session/PCAP and certificate-renewal checks on your target infrastructure before treating the deployment as qualified; see [qualification](qualification.md).

For the disposable kind walkthrough only, `kind delete cluster --name arkime-demo` removes the cluster and its local disks, including all captured packets. On a persistent cluster, Helm uninstall is not a data-erasure procedure: CRDs, Secrets, PVCs and host PCAP may be retained.
