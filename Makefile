SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help
BIN := $(CURDIR)/bin
TOOLS_MOD := $(CURDIR)/tools/go.mod
GO_TOOL = $(GO) tool -modfile=$(TOOLS_MOD)
CONTROLLER_GEN = $(GO_TOOL) controller-gen
VERSION ?= 0.0.0-dev
HELM ?= helm
GO ?= go
KUBE_CONTEXT ?=
OCI_REPOSITORY ?= oci://ghcr.io/bwagner5/arkime-k8s-operator/helm-charts

.PHONY: help generate fmt test verify run test-e2e release-snapshot clean build tools package-charts verify-generated ci-verify release
help: ## Show contributor commands (overrides: VERSION, KUBE_CONTEXT, OCI_REPOSITORY).
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
generate: ## Generate deepcopy, CRDs, RBAC and reference docs.
	$(CONTROLLER_GEN) object crd paths=./api/v1alpha1 output:crd:artifacts:config=charts/arkime-k8s-operator-crds/templates
	$(CONTROLLER_GEN) rbac:roleName=arkime-k8s-operator paths=./... output:rbac:artifacts:config=config/rbac
	bash hack/generate.sh
fmt: ## Format Go and tidy both modules.
	gofmt -w api cmd internal
	$(GO) mod tidy
	cd tools && $(GO) mod tidy
test: ## Run unit and available envtest tests without generation.
	$(GO) test -race ./...
verify: ## Check formatting, vet, generated drift, charts and release configuration.
	@test -z "$$(gofmt -l api cmd internal)"
	$(GO) vet ./...
	$(MAKE) verify-generated
	$(HELM) lint charts/arkime-k8s-operator charts/arkime-k8s-operator-crds
	$(HELM) template arkime charts/arkime-k8s-operator >/dev/null
	$(HELM) template arkime charts/arkime-k8s-operator --set crds.enabled=false >/dev/null
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
tools: ## Install the Go tools pinned in tools/go.mod into bin/.
	mkdir -p "$(BIN)"
	GOBIN="$(BIN)" $(GO) install -modfile=$(TOOLS_MOD) tool
verify-generated:
	@GO="$(GO)" bash hack/verify-generated.sh
package-charts:
	mkdir -p _artifacts/helm
	$(HELM) package charts/arkime-k8s-operator-crds --version "$(VERSION)" --app-version "$(VERSION)" -d _artifacts/helm
	$(HELM) package charts/arkime-k8s-operator --version "$(VERSION)" --app-version "$(VERSION)" -d _artifacts/helm
	$(HELM) template arkime-crds "_artifacts/helm/arkime-k8s-operator-crds-$(VERSION).tgz" >_artifacts/crds.yaml
	printf 'apiVersion: v1\nkind: Namespace\nmetadata:\n  name: arkime-system\n---\n' >_artifacts/operator.yaml
	$(HELM) template arkime "_artifacts/helm/arkime-k8s-operator-$(VERSION).tgz" --namespace arkime-system --set crds.enabled=false >>_artifacts/operator.yaml
ci-verify:
	$(MAKE) verify
	$(MAKE) test
release:
	goreleaser release --clean
	for chart in _artifacts/helm/*.tgz; do $(HELM) push "$$chart" "$(OCI_REPOSITORY)"; done
