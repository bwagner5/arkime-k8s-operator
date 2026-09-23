# Arkime Kubernetes operator

A Go controller for namespaced `ArkimeCluster` installations. Installs with separate operator and CRD Helm charts. The controller manages an external database schema, viewer, Cont3xt, WISE, host-network node capture, and a single persistent TZSP receiver.

Start with [getting started](docs/getting-started.md), [API reference](docs/api.md), [operations](docs/operations.md), and [release process](docs/releasing.md). Application settings belong in `ArkimeCluster`; Helm values configure only the controller.

```sh
make help
make generate
make test
make verify
make test-e2e
```
