SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help
BIN := $(CURDIR)/bin
CONTROLLER_GEN := $(BIN)/controller-gen
VERSION ?= 0.0.0-dev
HELM ?= helm
GO ?= go
KUBE_CONTEXT ?=
OCI_REPOSITORY ?= oci://ghcr.io/bwagner5/arkime-k8s-operator/helm-charts

.PHONY: help generate fmt test verify run test-e2e release-snapshot clean build tools package-charts verify-generated ci-verify release
help: ## Show contributor commands (overrides: VERSION, KUBE_CONTEXT, OCI_REPOSITORY).
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
generate: tools ## Generate deepcopy, CRDs, RBAC and reference docs.
	$(CONTROLLER_GEN) object crd rbac:roleName=arkime-k8s-operator paths=./... output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac
	python3 hack/generate.py
fmt: ## Format Go and tidy both modules.
	gofmt -w api cmd internal
	$(GO) mod tidy
	cd tools && $(GO) mod tidy
test: ## Run unit and available envtest tests without generation.
	$(GO) test -race ./...
verify: tools ## Check formatting, vet, generated drift, charts and release configuration.
	@test -z "$$(gofmt -l api cmd internal)"
	$(GO) vet ./...
	$(MAKE) verify-generated
	$(HELM) lint charts/arkime-k8s-operator charts/arkime-k8s-operator-crds
	$(HELM) template arkime charts/arkime-k8s-operator >/dev/null
	$(HELM) template arkime-crds charts/arkime-k8s-operator-crds >/dev/null
	goreleaser check
run: ## Run against an explicitly selected kubeconfig context.
	@test -n "$(KUBE_CONTEXT)" || { echo 'Set KUBE_CONTEXT'; exit 1; }
	$(GO) run ./cmd/manager --kube-context="$(KUBE_CONTEXT)" --leader-elect=false
build: ## Build the manager locally.
	mkdir -p _artifacts
	CGO_ENABLED=0 $(GO) build -trimpath -o _artifacts/manager ./cmd/manager
test-e2e: ## Create a disposable kind cluster and run integration tests; keep failure artifacts.
	bash hack/e2e.sh
release-snapshot: ## Build a local release without publication (requires Docker).
	goreleaser release --snapshot --clean
clean: ## Remove repository-owned build and test artifacts.
	rm -rf -- _artifacts dist bin
tools: $(CONTROLLER_GEN)
$(CONTROLLER_GEN): tools/go.mod
	mkdir -p "$(BIN)"
	GOBIN="$(BIN)" $(GO) install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.21.0
verify-generated:
	@bash hack/verify-generated.sh
package-charts:
	mkdir -p _artifacts/helm
	$(HELM) package charts/arkime-k8s-operator-crds --version "$(VERSION)" --app-version "$(VERSION)" -d _artifacts/helm
	$(HELM) package charts/arkime-k8s-operator --version "$(VERSION)" --app-version "$(VERSION)" -d _artifacts/helm
	$(HELM) template arkime-crds "_artifacts/helm/arkime-k8s-operator-crds-$(VERSION).tgz" >_artifacts/crds.yaml
	python3 -c 'from pathlib import Path; Path("_artifacts/operator.yaml").write_text("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: arkime-system\n---\n")'
	$(HELM) template arkime "_artifacts/helm/arkime-k8s-operator-$(VERSION).tgz" --namespace arkime-system >>_artifacts/operator.yaml
ci-verify:
	$(MAKE) verify
	$(MAKE) test
release:
	goreleaser release --clean
	for chart in _artifacts/helm/*.tgz; do $(HELM) push "$$chart" "$(OCI_REPOSITORY)"; done
