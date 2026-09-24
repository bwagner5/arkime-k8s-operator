#!/usr/bin/env bash
set -euo pipefail
name=${E2E_CLUSTER_NAME:-arkime-operator-e2e}
[[ "$name" == arkime-operator-e2e* ]] || { echo 'E2E_CLUSTER_NAME must start with arkime-operator-e2e'; exit 1; }
mkdir -p _artifacts/e2e
if kind get clusters | grep -Fxq "$name"; then echo "Refusing to reuse existing cluster $name"; exit 1; fi
created=false
cleanup() {
  rc=$?
  if $created; then
    kind export logs --name "$name" _artifacts/e2e/logs || true
    kubectl --context "kind-$name" get arkimeclusters,pods,jobs -A -o yaml >_artifacts/e2e/resources.yaml || true
    if [[ ${KEEP_CLUSTER:-false} != true ]]; then kind delete cluster --name "$name"; fi
  fi
  exit "$rc"
}
trap cleanup EXIT
# Keep the version tag for kind's version detection, but verify its immutable content.
node_image=kindest/node:v1.34.0
node_digest=sha256:7416a61b42b1662ca6ca89f02028ac133a309a2a30ba309614e8ec94d976dc5a
docker pull "kindest/node@$node_digest"
docker tag "kindest/node@$node_digest" "$node_image"
created=true
kind create cluster --retain --name "$name" --image "$node_image" --config test/e2e/kind.yaml --kubeconfig "$PWD/_artifacts/e2e/kubeconfig"
export KUBECONFIG="$PWD/_artifacts/e2e/kubeconfig"
context="kind-$name"
make package-charts
KO_DOCKER_REPO=ko.local ko build --bare --platform "linux/$(go env GOARCH)" ./cmd/manager >_artifacts/e2e/image
image=$(tail -1 _artifacts/e2e/image)
kind load docker-image --name "$name" "$image"
helm upgrade --install arkime-crds _artifacts/helm/arkime-k8s-operator-crds-0.0.0-dev.tgz --kube-context "$context"
helm upgrade --install arkime _artifacts/helm/arkime-k8s-operator-0.0.0-dev.tgz --kube-context "$context" --namespace arkime-system --create-namespace --set crds.enabled=false --set image.repository="${image%:*}" --set image.tag="${image##*:}"
kubectl --context "$context" create namespace arkime
kubectl --context "$context" -n arkime apply -f test/e2e/database.yaml
kubectl --context "$context" -n arkime rollout status deployment/opensearch --timeout=300s
kubectl --context "$context" -n arkime apply -f test/e2e/wise.yaml
kubectl --context "$context" -n arkime apply -f test/e2e/cluster.yaml
kubectl --context "$context" -n arkime wait arkimecluster/demo --for=condition=Ready --timeout=600s
E2E_CONTEXT="$context" go test -tags=e2e -count=1 -timeout=20m -v ./test/e2e
