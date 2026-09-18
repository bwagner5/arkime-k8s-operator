# Getting started

Prerequisites: Kubernetes 1.34+, Helm, an existing reachable OpenSearch or Elasticsearch database, and storage for at least one capture mode. No database, Gateway controller, CNI, certificates, or storage provisioner is installed implicitly. Only the development OpenSearch fixture is currently exercised; do not infer compatibility with every backend major accepted by the guard.

Until a release is qualified, build an operator image with `ko`, set the operator chart image values, and install the local charts:

```sh
helm upgrade --install arkime-crds ./charts/arkime-k8s-operator-crds
helm upgrade --install arkime ./charts/arkime-k8s-operator \
  --namespace arkime-system --create-namespace \
  --set image.repository=YOUR_OPERATOR_REPOSITORY --set image.digest=sha256:YOUR_DIGEST
kubectl create namespace arkime
```

Supply a database Secret with a `basicAuth` key containing `username:password`, using your secret manager or a file rather than shell history. Adapt `examples/node.yaml`: specify the real interfaces, dedicated host storage directory, database endpoints, and namespace. Then apply it:

```sh
kubectl apply -f examples/node.yaml
kubectl -n arkime get arkimecluster home -o yaml
kubectl -n arkime get services
```

Find the viewer Service in that output and port-forward port 8005. Status gives `adminSecret` and `sharedSecret` names; retrieve the initial username/password from the admin Secret using your approved secret tooling. Do not log or commit their values. Initial login uses digest authentication. Use HTTPS for public exposure.

`examples/external.yaml` demonstrates in-cluster TZSP reception using a retained PVC. The endpoint accepts TZSP, not arbitrary packet broker UDP. `examples/gateway.yaml` requires Gateway API 1.6.1 standard CRDs and a controller implementing UDPRoute v1. WISE is internal. Cont3xt is enabled by default; configuring external providers is separate.

For a disposable development backend, `test/e2e/database.yaml` pins OpenSearch 3.3.2 with security disabled and ephemeral storage. **Use only inside the disposable test cluster.** `make test-e2e` installs this fixture automatically; it is not a production database recipe.

Node capture requires a namespace admission policy that permits host networking, hostPath and capture capabilities. Choose interfaces for the actual CNI: host networking does not guarantee visibility into overlay traffic or encrypted payloads. No control-plane tolerations are added automatically. The default local viewer port is 8005; do not expose it to untrusted networks.

Advanced source files can be mounted from same-namespace `configMapRefs` under `/etc/arkime-extra/<ConfigMap-name>/`. OIDC uses `auth.oidc` (HTTPS discovery URL, client ID, client Secret key reference, user ID claim) and requires public HTTPS web exposure. The generated redirect paths are `/auth/login/callback` and `/cont3xt/auth/login/callback`; register both with the identity provider. Database CA Secret references are combined into a mounted trust bundle for capture, Node and Perl. Primary mTLS uses a Secret containing `tls.crt` and unencrypted `tls.key`; distinct per-backend client certificates are rejected.
