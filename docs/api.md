# API reference (generated)

API: `arkime.arkime.com/v1alpha1`, namespaced `ArkimeCluster`.

Generated from Go API types. See the structural CRD for defaults and validation.

## ArkimeCluster

| JSON field | Go type |
| --- | --- |
| `spec` | `ArkimeClusterSpec` |
| `status` | `ArkimeClusterStatus` |

## ArkimeClusterList

| JSON field | Go type |
| --- | --- |
| `items` | `[]ArkimeCluster` |

## ArkimeClusterSpec

| JSON field | Go type |
| --- | --- |
| `networkPolicy` | `*NetworkPolicySpec` |
| `version` | `string` |
| `image` | `ImageSpec` |
| `database` | `DatabaseSpec` |
| `auth` | `AuthSpec` |
| `capture` | `CaptureSpec` |
| `viewer` | `ComponentSpec` |
| `cont3xt` | `Cont3xtSpec` |
| `wise` | `ComponentSpec` |
| `web` | `WebSpec` |
| `retention` | `RetentionSpec` |

## ImageSpec

| JSON field | Go type |
| --- | --- |
| `reference` | `string` |
| `allowUnverified` | `bool` |
| `pullSecrets` | `[]corev1.LocalObjectReference` |

## DatabaseSpec

| JSON field | Go type |
| --- | --- |
| `bootstrapAuth` | `*DatabaseAuth` |
| `indexPrefix` | `string` |
| `users` | `*UsersDatabase` |
| `schema` | `SchemaSpec` |
| inline fields | [Backend](#backend) |

## Backend

| JSON field | Go type |
| --- | --- |
| `engine` | `string` |
| `endpoints` | `[]string` |
| `auth` | `DatabaseAuth` |
| `tls` | `DatabaseTLS` |

## DatabaseAuth

| JSON field | Go type |
| --- | --- |
| `basicAuthSecretRef` | `*corev1.SecretKeySelector` |
| `apiKeySecretRef` | `*corev1.SecretKeySelector` |
| `unauthenticated` | `bool` |

## DatabaseTLS

| JSON field | Go type |
| --- | --- |
| `caSecretRef` | `*corev1.SecretKeySelector` |
| `clientCertificateSecret` | `string` |

## UsersDatabase

| JSON field | Go type |
| --- | --- |
| `indexPrefix` | `string` |
| inline fields | [Backend](#backend) |

## SchemaSpec

| JSON field | Go type |
| --- | --- |
| `mode` | `string` |
| `approvedUpgradeVersion` | `string` |
| `retryToken` | `string` |

## OIDCSpec

| JSON field | Go type |
| --- | --- |
| `discoverURL` | `string` |
| `clientID` | `string` |
| `clientSecretRef` | `corev1.SecretKeySelector` |
| `userIDField` | `string` |

## AuthSpec

| JSON field | Go type |
| --- | --- |
| `oidc` | `*OIDCSpec` |
| `sharedSecret` | `string` |
| `adminSecret` | `string` |

## ComponentSpec

| JSON field | Go type |
| --- | --- |
| `configMapRefs` | `[]corev1.LocalObjectReference` |
| `plugins` | `[]string` |
| `viewerPlugins` | `[]string` |
| `enabled` | `*bool` |
| `resources` | `corev1.ResourceRequirements` |
| `nodeSelector` | `map[string]string` |
| `tolerations` | `[]corev1.Toleration` |
| `affinity` | `*corev1.Affinity` |
| `topologySpreadConstraints` | `[]corev1.TopologySpreadConstraint` |
| `config` | `map[string]map[string]string` |
| `configSecretRefs` | `[]ConfigSecretRef` |

## ConfigSecretRef

| JSON field | Go type |
| --- | --- |
| `section` | `string` |
| `key` | `string` |
| `secretKeyRef` | `corev1.SecretKeySelector` |

## Cont3xtSpec

| JSON field | Go type |
| --- | --- |
| `database` | `*Backend` |
| `allowSharedDatabase` | `bool` |
| inline fields | [ComponentSpec](#componentspec) |

## CaptureSpec

| JSON field | Go type |
| --- | --- |
| `node` | `*NodeCapture` |
| `external` | `*ExternalCapture` |

## NodeCapture

| JSON field | Go type |
| --- | --- |
| `interfaces` | `[]string` |
| `storage` | `HostStorage` |
| `bpf` | `string` |
| `viewerPort` | `int32` |
| `tuneInterfaces` | `bool` |
| inline fields | [ComponentSpec](#componentspec) |

## HostStorage

| JSON field | Go type |
| --- | --- |
| `hostPath` | `string` |

## ExternalCapture

| JSON field | Go type |
| --- | --- |
| `storage` | `ClaimStorage` |
| `exposure` | `UDPExposure` |
| inline fields | [ComponentSpec](#componentspec) |

## ClaimStorage

| JSON field | Go type |
| --- | --- |
| `existingClaim` | `string` |
| `volumeClaim` | `*VolumeClaim` |

## VolumeClaim

| JSON field | Go type |
| --- | --- |
| `size` | `string` |
| `storageClassName` | `*string` |

## UDPExposure

| JSON field | Go type |
| --- | --- |
| `mode` | `string` |
| `gateway` | `*GatewaySpec` |
| `loadBalancerClass` | `*string` |
| `annotations` | `map[string]string` |
| `sourceRanges` | `[]string` |
| `externalTrafficPolicy` | `corev1.ServiceExternalTrafficPolicy` |

## GatewaySpec

| JSON field | Go type |
| --- | --- |
| `gatewayClassName` | `string` |
| `parentRef` | `*ParentReference` |

## ParentReference

| JSON field | Go type |
| --- | --- |
| `name` | `string` |
| `sectionName` | `string` |

## WebSpec

| JSON field | Go type |
| --- | --- |
| `mode` | `string` |
| `host` | `string` |
| `ingressClassName` | `*string` |
| `tlsSecret` | `string` |
| `parentRef` | `*ParentReference` |

## RetentionSpec

| JSON field | Go type |
| --- | --- |
| `mode` | `string` |
| `sessionsDays` | `int32` |
| `pcapFreeSpace` | `string` |

## ArkimeClusterStatus

| JSON field | Go type |
| --- | --- |
| `upgradePhase` | `string` |
| `upgradeTarget` | `string` |
| `databaseIdentity` | `string` |
| `observedGeneration` | `int64` |
| `resolvedVersion` | `string` |
| `resolvedImage` | `string` |
| `indexPrefix` | `string` |
| `sharedSecret` | `string` |
| `adminSecret` | `string` |
| `schemaOperation` | `string` |
| `schemaJob` | `string` |
| `completedOperation` | `string` |
| `appliedVersion` | `string` |
| `nodeStorageRetained` | `bool` |
| `externalStorageRetained` | `bool` |
| `nodeDesired` | `int32` |
| `nodeReady` | `int32` |
| `endpoints` | `map[string]string` |
| `conditions` | `[]metav1.Condition` |

## NetworkPolicySpec

| JSON field | Go type |
| --- | --- |
| `ingress` | `[]networkingv1.NetworkPolicyIngressRule` |
| `egress` | `[]networkingv1.NetworkPolicyEgressRule` |
