# Arkime Kubernetes operator

Run Arkime on Kubernetes to capture, search, and explore network traffic. Define an `ArkimeCluster`, and the operator takes care of deploying and configuring the pieces together.

Helm installs the operator; the operator handles Arkime's ongoing management. Compared with deploying Arkime through a chart alone, it watches for changes and coordinates work that needs to happen in order, like preparing the database before starting capture.

- **Capture and search:** Run capture on Kubernetes nodes or receive mirrored traffic over TZSP, with a viewer for searching sessions and retrieving packets. Add WISE for enrichment and Cont3xt for investigations.
- **Less manual upkeep:** Initialize the database schema, manage session retention, roll workloads when configuration or referenced Secrets change, and report readiness. Supported schema upgrades require approval and stop writers before migration.
- **Kubernetes context:** The pod enricher watches live pod IPs and feeds WISE pod, namespace, node, and cluster metadata, making traffic easier to identify. It is enabled by default with WISE. Enrichment is experimental and uses current IP mappings, so historical traffic and reused IPs can be misattributed.
- **Database CA trust:** Supply your CA certificate in a Secret and reference it in the database settings. The operator automatically configures trust for Arkime components and database jobs, and rolls application pods when it changes. It does not issue certificates or enable TLS between Arkime components; internal connections currently use HTTP.

Bring your own OpenSearch or Elasticsearch database and packet storage.

[Get started](docs/getting-started.md) · [Pod enrichment](docs/kubernetes-pod-enrichment.md) · [TLS and private CAs](docs/tls.md)
