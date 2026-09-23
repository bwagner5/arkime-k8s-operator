// Package v1alpha1 contains the Arkime operator API.
// +kubebuilder:object:generate=true
// +groupName=arkime.arkime.com
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var GroupVersion = schema.GroupVersion{Group: "arkime.arkime.com", Version: "v1alpha1"}
var SchemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &ArkimeCluster{}, &ArkimeClusterList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
})
var AddToScheme = SchemeBuilder.AddToScheme

// +kubebuilder:object:root=true
// +kubebuilder:metadata:annotations="helm.sh/resource-policy=keep"
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.resolvedVersion`
type ArkimeCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ArkimeClusterSpec   `json:"spec"`
	Status            ArkimeClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ArkimeClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ArkimeCluster `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="!has(oldSelf.capture.node) || has(self.capture.node)",message="retain the node block and disable capture with enabled:false"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.capture.external) || has(self.capture.external)",message="retain the external block and disable capture with enabled:false"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.database.indexPrefix) || (has(self.database.indexPrefix) && self.database.indexPrefix == oldSelf.database.indexPrefix)",message="index prefix is immutable"
type ArkimeClusterSpec struct {
	NetworkPolicy *NetworkPolicySpec `json:"networkPolicy,omitempty"`
	Version       string             `json:"version,omitempty"`
	Image         ImageSpec          `json:"image,omitempty"`
	Database      DatabaseSpec       `json:"database"`
	Auth          AuthSpec           `json:"auth,omitempty"`
	Capture       CaptureSpec        `json:"capture"`
	Viewer        ComponentSpec      `json:"viewer,omitempty"`
	Cont3xt       Cont3xtSpec        `json:"cont3xt,omitempty"`
	Wise          ComponentSpec      `json:"wise,omitempty"`
	Web           WebSpec            `json:"web,omitempty"`
	Retention     RetentionSpec      `json:"retention"`
}
type ImageSpec struct {
	Reference       string                        `json:"reference,omitempty"`
	AllowUnverified bool                          `json:"allowUnverified,omitempty"`
	PullSecrets     []corev1.LocalObjectReference `json:"pullSecrets,omitempty"`
}
type DatabaseSpec struct {
	Backend       `json:",inline"`
	BootstrapAuth *DatabaseAuth `json:"bootstrapAuth,omitempty"`
	// +kubebuilder:validation:MaxLength=48
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	IndexPrefix string         `json:"indexPrefix,omitempty"`
	Users       *UsersDatabase `json:"users,omitempty"`
	Schema      SchemaSpec     `json:"schema,omitempty"`
}
type Backend struct {
	// +kubebuilder:validation:Enum=OpenSearch;Elasticsearch
	Engine string `json:"engine"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Endpoints []string     `json:"endpoints"`
	Auth      DatabaseAuth `json:"auth"`
	TLS       DatabaseTLS  `json:"tls,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="(has(self.basicAuthSecretRef) ? 1 : 0) + (has(self.apiKeySecretRef) ? 1 : 0) + (has(self.unauthenticated) && self.unauthenticated ? 1 : 0) == 1",message="choose exactly one database authentication method"
type DatabaseAuth struct {
	BasicAuthSecretRef *corev1.SecretKeySelector `json:"basicAuthSecretRef,omitempty"`
	APIKeySecretRef    *corev1.SecretKeySelector `json:"apiKeySecretRef,omitempty"`
	Unauthenticated    bool                      `json:"unauthenticated,omitempty"`
}
type DatabaseTLS struct {
	CASecretRef             *corev1.SecretKeySelector `json:"caSecretRef,omitempty"`
	ClientCertificateSecret string                    `json:"clientCertificateSecret,omitempty"`
}
type UsersDatabase struct {
	Backend     `json:",inline"`
	IndexPrefix string `json:"indexPrefix"`
}
type SchemaSpec struct {
	// +kubebuilder:default=Initialize
	// +kubebuilder:validation:Enum=Initialize;Adopt
	Mode                   string `json:"mode,omitempty"`
	ApprovedUpgradeVersion string `json:"approvedUpgradeVersion,omitempty"`
	// RetryToken explicitly retries a failed finite operation after correcting its cause.
	RetryToken string `json:"retryToken,omitempty"`
}
type OIDCSpec struct {
	DiscoverURL     string                   `json:"discoverURL"`
	ClientID        string                   `json:"clientID"`
	ClientSecretRef corev1.SecretKeySelector `json:"clientSecretRef"`
	// +kubebuilder:default=sub
	UserIDField string `json:"userIDField,omitempty"`
}
type AuthSpec struct {
	OIDC         *OIDCSpec `json:"oidc,omitempty"`
	SharedSecret string    `json:"sharedSecret,omitempty"`
	AdminSecret  string    `json:"adminSecret,omitempty"`
}
type ComponentSpec struct {
	ConfigMapRefs             []corev1.LocalObjectReference     `json:"configMapRefs,omitempty"`
	Plugins                   []string                          `json:"plugins,omitempty"`
	ViewerPlugins             []string                          `json:"viewerPlugins,omitempty"`
	Enabled                   *bool                             `json:"enabled,omitempty"`
	Resources                 corev1.ResourceRequirements       `json:"resources,omitempty"`
	NodeSelector              map[string]string                 `json:"nodeSelector,omitempty"`
	Tolerations               []corev1.Toleration               `json:"tolerations,omitempty"`
	Affinity                  *corev1.Affinity                  `json:"affinity,omitempty"`
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
	// Config extends application INI sections; operator-owned settings are rejected.
	Config           map[string]map[string]string `json:"config,omitempty"`
	ConfigSecretRefs []ConfigSecretRef            `json:"configSecretRefs,omitempty"`
}
type ConfigSecretRef struct {
	Section      string                   `json:"section"`
	Key          string                   `json:"key"`
	SecretKeyRef corev1.SecretKeySelector `json:"secretKeyRef"`
}
type Cont3xtSpec struct {
	ComponentSpec       `json:",inline"`
	Database            *Backend `json:"database,omitempty"`
	AllowSharedDatabase bool     `json:"allowSharedDatabase,omitempty"`
}
type CaptureSpec struct {
	Node     *NodeCapture     `json:"node,omitempty"`
	External *ExternalCapture `json:"external,omitempty"`
}
type NodeCapture struct {
	ComponentSpec `json:",inline"`
	// +kubebuilder:validation:MaxItems=64
	Interfaces []string    `json:"interfaces,omitempty"`
	Storage    HostStorage `json:"storage"`
	BPF        string      `json:"bpf,omitempty"`
	// +kubebuilder:default=8005
	// +kubebuilder:validation:Minimum=1024
	// +kubebuilder:validation:Maximum=65534
	ViewerPort     int32 `json:"viewerPort,omitempty"`
	TuneInterfaces bool  `json:"tuneInterfaces,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="host PCAP storage is immutable"
type HostStorage struct {
	HostPath string `json:"hostPath"`
}
type ExternalCapture struct {
	ComponentSpec `json:",inline"`
	Storage       ClaimStorage `json:"storage"`
	Exposure      UDPExposure  `json:"exposure"`
}

// +kubebuilder:validation:XValidation:rule="has(self.existingClaim) != has(self.volumeClaim)",message="choose exactly one PVC source"
// +kubebuilder:validation:XValidation:rule="has(self.existingClaim) == has(oldSelf.existingClaim) && (!has(self.existingClaim) || self.existingClaim == oldSelf.existingClaim)",message="PVC source is immutable"
type ClaimStorage struct {
	ExistingClaim string       `json:"existingClaim,omitempty"`
	VolumeClaim   *VolumeClaim `json:"volumeClaim,omitempty"`
}
type VolumeClaim struct {
	Size             string  `json:"size"`
	StorageClassName *string `json:"storageClassName,omitempty"`
}
type UDPExposure struct {
	// +kubebuilder:validation:Enum=ClusterIP;LoadBalancer;Gateway
	Mode              string            `json:"mode"`
	Gateway           *GatewaySpec      `json:"gateway,omitempty"`
	LoadBalancerClass *string           `json:"loadBalancerClass,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	SourceRanges      []string          `json:"sourceRanges,omitempty"`
	// +kubebuilder:validation:Enum=Cluster;Local
	ExternalTrafficPolicy corev1.ServiceExternalTrafficPolicy `json:"externalTrafficPolicy,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.gatewayClassName) != has(self.parentRef)",message="choose dedicated Gateway class or existing parent"
type GatewaySpec struct {
	GatewayClassName string           `json:"gatewayClassName,omitempty"`
	ParentRef        *ParentReference `json:"parentRef,omitempty"`
}
type ParentReference struct {
	Name        string `json:"name"`
	SectionName string `json:"sectionName,omitempty"`
}
type WebSpec struct {
	// +kubebuilder:validation:Enum=ClusterIP;Ingress;Gateway
	Mode             string           `json:"mode,omitempty"`
	Host             string           `json:"host,omitempty"`
	IngressClassName *string          `json:"ingressClassName,omitempty"`
	TLSSecret        string           `json:"tlsSecret,omitempty"`
	ParentRef        *ParentReference `json:"parentRef,omitempty"`
}
type RetentionSpec struct {
	// +kubebuilder:default=Managed
	// +kubebuilder:validation:Enum=Managed;External
	Mode string `json:"mode,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=36500
	SessionsDays int32 `json:"sessionsDays,omitempty"`
	// +kubebuilder:default="10%"
	PcapFreeSpace string `json:"pcapFreeSpace,omitempty"`
}
type ArkimeClusterStatus struct {
	UpgradePhase            string            `json:"upgradePhase,omitempty"`
	UpgradeTarget           string            `json:"upgradeTarget,omitempty"`
	DatabaseIdentity        string            `json:"databaseIdentity,omitempty"`
	ObservedGeneration      int64             `json:"observedGeneration,omitempty"`
	ResolvedVersion         string            `json:"resolvedVersion,omitempty"`
	ResolvedImage           string            `json:"resolvedImage,omitempty"`
	IndexPrefix             string            `json:"indexPrefix,omitempty"`
	SharedSecret            string            `json:"sharedSecret,omitempty"`
	AdminSecret             string            `json:"adminSecret,omitempty"`
	SchemaOperation         string            `json:"schemaOperation,omitempty"`
	SchemaJob               string            `json:"schemaJob,omitempty"`
	CompletedOperation      string            `json:"completedOperation,omitempty"`
	AppliedVersion          string            `json:"appliedVersion,omitempty"`
	NodeStorageRetained     bool              `json:"nodeStorageRetained,omitempty"`
	ExternalStorageRetained bool              `json:"externalStorageRetained,omitempty"`
	NodeDesired             int32             `json:"nodeDesired,omitempty"`
	NodeReady               int32             `json:"nodeReady,omitempty"`
	Endpoints               map[string]string `json:"endpoints,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func Enabled(c ComponentSpec) bool { return c.Enabled == nil || *c.Enabled }

// NetworkPolicySpec adds explicit user rules to internal application traffic and DNS.
// Host-network enforcement is CNI-dependent.
type NetworkPolicySpec struct {
	Ingress []networkingv1.NetworkPolicyIngressRule `json:"ingress,omitempty"`
	Egress  []networkingv1.NetworkPolicyEgressRule  `json:"egress,omitempty"`
}
