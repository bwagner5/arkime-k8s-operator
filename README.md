# Arkime Kubernetes operator

A Go controller for namespaced `ArkimeCluster` installations. Installs with separate operator and CRD Helm charts. The controller manages an external database schema, viewer, Cont3xt, WISE, host-network node capture, and a single persistent TZSP receiver.

**Development implementation, not a qualified production release.** The pinned application is Arkime 6.7.0. See [qualification](docs/qualification.md) for the acceptance gates and remaining limitations; a running pod is not proof of packet capture.

Start with [getting started](docs/getting-started.md), [API reference](docs/api.md), [operations](docs/operations.md), and [release process](docs/releasing.md). Application settings belong in `ArkimeCluster`; Helm values configure only the controller.

```sh
make help
make generate
make test
make verify
make test-e2e
```

The module preserves Go 1.27.1. Unit tests use the controller-runtime fake client; envtest admission tests run when `KUBEBUILDER_ASSETS` is set. Integration tests own a named disposable kind cluster and leave diagnostics in `_artifacts/e2e/`.
