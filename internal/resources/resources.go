package resources

import (
	"fmt"
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	cfg "github.com/bwagner5/arkime-k8s-operator/internal/config"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"maps"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
	"strings"
)

func Ptr[T any](v T) *T { return &v }
func Labels(c *api.ArkimeCluster, k string) map[string]string {
	return map[string]string{"app.kubernetes.io/managed-by": "arkime-k8s-operator", "arkime.arkime.com/cluster": cfg.ID(c), "app.kubernetes.io/component": k}
}
func Meta(c *api.ArkimeCluster, k string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: cfg.Name(c, k), Namespace: c.Namespace, Labels: Labels(c, k)}
}

// Arkime reads ARKIME_<section>__<key>; the default section is unnamed, giving ARKIME__<key>.
func EnvName(section, key string) string {
	if section != "" {
		section = "_" + section
	}
	return "ARKIME" + section + "__" + key
}
func EnvSecret(name string, ref *corev1.SecretKeySelector) corev1.EnvVar {
	return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: ref}}
}
func secretRef(name, key string) *corev1.SecretKeySelector {
	return &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key}
}
func Credentials(c *api.ArkimeCluster, component string) []corev1.EnvVar {
	env := []corev1.EnvVar{}
	add := func(b api.Backend, section, prefix string) {
		if b.Auth.BasicAuthSecretRef != nil {
			env = append(env, EnvSecret(EnvName(section, prefix+"elasticsearchBasicAuth"), b.Auth.BasicAuthSecretRef))
		}
		if b.Auth.APIKeySecretRef != nil {
			env = append(env, EnvSecret(EnvName(section, prefix+"elasticsearchAPIKey"), b.Auth.APIKeySecretRef))
		}
	}
	add(c.Spec.Database.Backend, "", "")
	users := c.Spec.Database.Backend
	if c.Spec.Database.Users != nil {
		users = c.Spec.Database.Users.Backend
	}
	// usersElasticsearch has a capital E, unlike the primary setting.
	before := len(env)
	add(users, "", "users")
	for i := before; i < len(env); i++ {
		env[i].Name = strings.Replace(env[i].Name, "userselasticsearch", "usersElasticsearch", 1)
	}
	if component != "capture" {
		for _, key := range []string{"passwordSecret", "serverSecret"} {
			env = append(env, EnvSecret(EnvName("", key), secretRef(c.Status.SharedSecret, key)))
		}
	}
	if component == "cont3xt" {
		b := c.Spec.Database.Backend
		if c.Spec.Cont3xt.Database != nil {
			b = *c.Spec.Cont3xt.Database
		}
		add(b, "cont3xt", "")
		add(c.Spec.Database.Backend, "arkime:local", "")
		env = append(env, EnvSecret(EnvName("cont3xt", "passwordSecret"), secretRef(c.Status.SharedSecret, "passwordSecret")))
	}
	if o := c.Spec.Auth.OIDC; o != nil && (component == "viewer" || component == "cont3xt") {
		env = append(env, EnvSecret("ARKIME__authClientSecret", &o.ClientSecretRef))
	}
	for _, r := range cfg.Components(c)[component].ConfigSecretRefs {
		env = append(env, EnvSecret(EnvName(r.Section, r.Key), &r.SecretKeyRef))
	}
	return env
}
func pod(c *api.ArkimeCluster, k, digest string) corev1.PodTemplateSpec {
	comp := cfg.Components(c)[k]
	p := corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: Labels(c, k), Annotations: map[string]string{"arkime.arkime.com/config-hash": digest}}, Spec: corev1.PodSpec{ServiceAccountName: cfg.Name(c, "app"), AutomountServiceAccountToken: Ptr(false), ImagePullSecrets: c.Spec.Image.PullSecrets, TerminationGracePeriodSeconds: Ptr(int64(90)), NodeSelector: maps.Clone(comp.NodeSelector), Tolerations: comp.Tolerations, Affinity: comp.Affinity, TopologySpreadConstraints: comp.TopologySpreadConstraints, SecurityContext: &corev1.PodSecurityContext{FSGroup: Ptr(int64(65534)), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}, Volumes: []corev1.Volume{{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: cfg.Name(c, k)}}}}, {Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}}}
	for _, name := range []string{"primary", "users", "cont3xt"} {
		b, exists := cfg.Backends(c)[name]
		if !exists {
			continue
		}
		if b.TLS.CASecretRef != nil {
			r := b.TLS.CASecretRef
			p.Spec.Volumes = append(p.Spec.Volumes, corev1.Volume{Name: "ca-" + name, VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: r.Name, Items: []corev1.KeyToPath{{Key: r.Key, Path: "ca.crt"}}}}})
		}
	}
	for i, ref := range comp.ConfigMapRefs {
		p.Spec.Volumes = append(p.Spec.Volumes, corev1.Volume{Name: fmt.Sprintf("extra-%d", i), VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: ref}}})
	}

	caPaths := []string{}
	for _, name := range []string{"primary", "users", "cont3xt"} {
		if b, ok := cfg.Backends(c)[name]; ok && b.TLS.CASecretRef != nil {
			caPaths = append(caPaths, "/var/run/arkime-ca/"+name+"/ca.crt")
		}
	}
	if len(caPaths) > 0 {
		p.Spec.Volumes = append(p.Spec.Volumes, corev1.Volume{Name: "ca-bundle", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}})
		init := corev1.Container{Name: "trust-bundle", Image: c.Status.ResolvedImage, Command: []string{"/bin/sh", "-ec", "cat " + strings.Join(caPaths, " ") + " > /var/run/arkime-ca/bundle/ca.crt"}, SecurityContext: &corev1.SecurityContext{RunAsUser: Ptr(int64(65534)), RunAsGroup: Ptr(int64(65534)), RunAsNonRoot: Ptr(true), AllowPrivilegeEscalation: Ptr(false), ReadOnlyRootFilesystem: Ptr(true), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}, VolumeMounts: []corev1.VolumeMount{{Name: "ca-bundle", MountPath: "/var/run/arkime-ca/bundle"}}}
		for _, name := range []string{"primary", "users", "cont3xt"} {
			b, exists := cfg.Backends(c)[name]
			if !exists {
				continue
			}
			if b.TLS.CASecretRef != nil {
				init.VolumeMounts = append(init.VolumeMounts, corev1.VolumeMount{Name: "ca-" + name, MountPath: "/var/run/arkime-ca/" + name, ReadOnly: true})
			}
		}
		p.Spec.InitContainers = append(p.Spec.InitContainers, init)
	}
	if name := c.Spec.Database.TLS.ClientCertificateSecret; name != "" {
		p.Spec.Volumes = append(p.Spec.Volumes, corev1.Volume{Name: "mtls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: name}}})
	}

	return p
}
func container(c *api.ArkimeCluster, k string, port int32) corev1.Container {
	script := map[string]string{"viewer": "viewer/viewer.js", "node": "viewer/viewer.js", "external": "viewer/viewer.js", "cont3xt": "cont3xt/cont3xt.js", "wise": "wiseService/wiseService.js"}[k]
	ct := corev1.Container{Name: k, Image: c.Status.ResolvedImage, ImagePullPolicy: corev1.PullIfNotPresent, WorkingDir: "/opt/arkime/" + strings.Split(script, "/")[0], Command: []string{"/opt/arkime/bin/node", "/opt/arkime/" + script}, Args: []string{"-c", "/etc/arkime/config.ini"}, Env: Credentials(c, k), Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: port}}, Resources: cfg.Components(c)[k].Resources, SecurityContext: &corev1.SecurityContext{RunAsUser: Ptr(int64(65534)), RunAsGroup: Ptr(int64(65534)), RunAsNonRoot: Ptr(true), ReadOnlyRootFilesystem: Ptr(true), AllowPrivilegeEscalation: Ptr(false), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}, VolumeMounts: []corev1.VolumeMount{{Name: "config", MountPath: "/etc/arkime", ReadOnly: true}, {Name: "tmp", MountPath: "/tmp"}}}
	if len(ct.Resources.Requests) == 0 {
		ct.Resources.Requests = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("256Mi")}
	}
	for _, name := range []string{"primary", "users", "cont3xt"} {
		b, exists := cfg.Backends(c)[name]
		if !exists {
			continue
		}
		if b.TLS.CASecretRef != nil {
			ct.VolumeMounts = append(ct.VolumeMounts, corev1.VolumeMount{Name: "ca-" + name, MountPath: "/var/run/arkime-ca/" + name, ReadOnly: true})
		}
	}
	for i, ref := range cfg.Components(c)[k].ConfigMapRefs {
		ct.VolumeMounts = append(ct.VolumeMounts, corev1.VolumeMount{Name: fmt.Sprintf("extra-%d", i), MountPath: "/etc/arkime-extra/" + ref.Name, ReadOnly: true})
	}

	if hasCA(c) {
		ct.VolumeMounts = append(ct.VolumeMounts, corev1.VolumeMount{Name: "ca-bundle", MountPath: "/var/run/arkime-ca/bundle", ReadOnly: true})
		ct.Env = append(ct.Env, corev1.EnvVar{Name: "NODE_EXTRA_CA_CERTS", Value: "/var/run/arkime-ca/bundle/ca.crt"}, corev1.EnvVar{Name: "PERL_LWP_SSL_CA_FILE", Value: "/var/run/arkime-ca/bundle/ca.crt"})
	}
	if c.Spec.Database.TLS.ClientCertificateSecret != "" {
		ct.VolumeMounts = append(ct.VolumeMounts, corev1.VolumeMount{Name: "mtls", MountPath: "/var/run/arkime-mtls", ReadOnly: true})
	}
	if k == "viewer" {
		ct.Args = append(ct.Args, "-n", cfg.ID(c)+"-viewer")
	}
	ct.Lifecycle = &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Exec: &corev1.ExecAction{Command: []string{"/bin/sh", "-ec", "kill -INT 1"}}}}
	ct.StartupProbe = &corev1.Probe{ProbeHandler: corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(port)}}, PeriodSeconds: 5, FailureThreshold: 60}
	ct.ReadinessProbe = ct.StartupProbe.DeepCopy()
	ct.ReadinessProbe.FailureThreshold = 3
	if k == "cont3xt" {
		ct.ReadinessProbe.ProbeHandler = corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/_ns_/nstest.html", Port: intstr.FromInt32(port)}}

	}
	return ct
}
func Service(c *api.ArkimeCluster, k string, port int32, protocol corev1.Protocol) *corev1.Service {
	component := k
	if k == "udp" || k == "local-viewer" {
		component = "external"
	}
	return &corev1.Service{ObjectMeta: Meta(c, k), Spec: corev1.ServiceSpec{Selector: Labels(c, component), Ports: []corev1.ServicePort{{Name: map[bool]string{true: "tzsp", false: "http"}[protocol == corev1.ProtocolUDP], Port: port, TargetPort: intstr.FromInt32(port), Protocol: protocol}}}}
}
func Workload(c *api.ArkimeCluster, k, digest string) client.Object {
	p := pod(c, k, digest)
	port := int32(8005)
	if k == "node" {
		port = cfg.Port(c)
	}
	if k == "wise" {
		port = 8081
	}
	if k == "cont3xt" {
		port = 3218
	}
	ct := container(c, k, port)
	if k == "node" || k == "external" {
		ct.Name = "local-viewer"
		identity := cfg.ID(c) + "-external"
		host := cfg.Name(c, "local-viewer") + "." + c.Namespace + ".svc"
		if k == "node" {
			identity = cfg.ID(c) + "-$(NODE_NAME)"
			host = "$(HOST_IP)"
			ct.Env = append(ct.Env, corev1.EnvVar{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"}}}, corev1.EnvVar{Name: "HOST_IP", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.hostIP"}}})
			p.Spec.HostNetwork = true
			p.Spec.DNSPolicy = corev1.DNSClusterFirstWithHostNet
			if p.Spec.NodeSelector == nil {
				p.Spec.NodeSelector = map[string]string{}
			}
			p.Spec.NodeSelector["kubernetes.io/os"] = "linux"
		}
		ct.Args = append(ct.Args, "-n", identity, "--host", host)
		ct.VolumeMounts = append(ct.VolumeMounts, corev1.VolumeMount{Name: "pcap", MountPath: "/opt/arkime/raw"})
		p.Spec.Containers = append(p.Spec.Containers, ct)
		if api.Enabled(cfg.Components(c)[k]) {
			cap := *ct.DeepCopy()
			cap.Name = "capture"
			cap.Lifecycle = nil
			cleanEnv := []corev1.EnvVar{}
			for _, e := range cap.Env {
				if e.Name == "ARKIME__passwordSecret" || e.Name == "ARKIME__serverSecret" || strings.HasPrefix(e.Name, "ARKIME__users") {
					continue
				}
				cleanEnv = append(cleanEnv, e)
			}
			cap.Env = cleanEnv
			cap.Command = []string{"/opt/arkime/bin/capture"}
			cap.WorkingDir = "/opt/arkime"
			healthPort := int32(8006)
			if k == "node" {
				healthPort = cfg.Port(c) + 1
			}
			cap.Ports = []corev1.ContainerPort{{Name: "health", ContainerPort: healthPort}}
			cap.StartupProbe.TCPSocket.Port = intstr.FromInt32(healthPort)
			cap.ReadinessProbe.TCPSocket.Port = intstr.FromInt32(healthPort)
			if k == "node" {
				cap.SecurityContext.RunAsUser = Ptr(int64(0))
				cap.SecurityContext.RunAsGroup = Ptr(int64(0))
				cap.SecurityContext.RunAsNonRoot = Ptr(false)
				cap.SecurityContext.Capabilities.Add = []corev1.Capability{"NET_RAW", "NET_ADMIN", "SETUID", "SETGID", "IPC_LOCK"}
			}

			p.Spec.Containers = append(p.Spec.Containers, cap)
		}
		vol := corev1.Volume{Name: "pcap"}
		if k == "node" {
			vol.HostPath = &corev1.HostPathVolumeSource{Path: c.Spec.Capture.Node.Storage.HostPath + "/" + cfg.ID(c), Type: Ptr(corev1.HostPathDirectoryOrCreate)}
		} else {
			claim := c.Spec.Capture.External.Storage.ExistingClaim
			if claim == "" {
				claim = cfg.Name(c, "pcap")
			}
			vol.PersistentVolumeClaim = &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claim}
		}
		p.Spec.Volumes = append(p.Spec.Volumes, vol)
		p.Spec.InitContainers = append(p.Spec.InitContainers, corev1.Container{Name: "pcap-owner", Image: c.Status.ResolvedImage, Command: []string{"/bin/sh", "-ec", "chown 65534:65534 /opt/arkime/raw; chmod 0770 /opt/arkime/raw"}, SecurityContext: &corev1.SecurityContext{RunAsUser: Ptr(int64(0)), AllowPrivilegeEscalation: Ptr(false), ReadOnlyRootFilesystem: Ptr(true), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}, Add: []corev1.Capability{"CHOWN", "FOWNER"}}}, VolumeMounts: []corev1.VolumeMount{{Name: "pcap", MountPath: "/opt/arkime/raw"}}})
		if k == "node" && api.Enabled(cfg.Components(c)[k]) && c.Spec.Capture.Node.TuneInterfaces {
			t := *p.Spec.Containers[len(p.Spec.Containers)-1].DeepCopy()
			t.Name = "tune-interfaces"
			t.Command = []string{"/opt/arkime/bin/arkime_config_interfaces.sh"}
			t.Args = nil
			t.StartupProbe = nil
			t.ReadinessProbe = nil
			p.Spec.InitContainers = append(p.Spec.InitContainers, t)
		}
	} else {
		p.Spec.Containers = []corev1.Container{ct}
	}
	sel := &metav1.LabelSelector{MatchLabels: Labels(c, k)}
	if k == "node" {
		return &appsv1.DaemonSet{ObjectMeta: Meta(c, k), Spec: appsv1.DaemonSetSpec{Selector: sel, Template: p}}
	}
	strategy := appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType}
	if k == "external" {
		strategy.Type = appsv1.RecreateDeploymentStrategyType
	}
	return &appsv1.Deployment{ObjectMeta: Meta(c, k), Spec: appsv1.DeploymentSpec{Replicas: Ptr(int32(1)), Selector: sel, Template: p, Strategy: strategy}}
}
func PVC(c *api.ArkimeCluster) *corev1.PersistentVolumeClaim {
	v := c.Spec.Capture.External.Storage.VolumeClaim
	return &corev1.PersistentVolumeClaim{ObjectMeta: Meta(c, "pcap"), Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, StorageClassName: v.StorageClassName, Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(v.Size)}}}}
}
func Bootstrap(c *api.ArkimeCluster, operation string) *batchv1.Job {
	p := pod(c, "viewer", operation)
	p.Spec.RestartPolicy = corev1.RestartPolicyNever
	p.Labels = Labels(c, "schema")
	ct := container(c, "viewer", 8005)
	ct.Name = "bootstrap"
	ct.Lifecycle = nil
	ct.Env = append(ct.Env, corev1.EnvVar{Name: "ARKIME__authMode", Value: "digest"})
	ct.Command = []string{"/opt/arkime/bin/node", "/etc/arkime/bootstrap.js"}
	ct.Args = nil
	ct.StartupProbe = nil
	ct.ReadinessProbe = nil
	ct.Ports = nil
	ct.Env = append(ct.Env, corev1.EnvVar{Name: "CLUSTER_UID", Value: string(c.UID)}, corev1.EnvVar{Name: "DATABASE_URL", Value: c.Spec.Database.Endpoints[0]}, corev1.EnvVar{Name: "DATABASE_ENGINE", Value: c.Spec.Database.Engine}, corev1.EnvVar{Name: "DATABASE_PREFIX", Value: cfg.Prefix(c)}, corev1.EnvVar{Name: "SCHEMA_MODE", Value: c.Spec.Database.Schema.Mode}, corev1.EnvVar{Name: "UPGRADE", Value: strconv.FormatBool(c.Status.AppliedVersion != "" && c.Status.AppliedVersion != c.Status.ResolvedVersion)}, EnvSecret("ADMIN_USER", secretRef(c.Status.AdminSecret, "username")), EnvSecret("ADMIN_PASSWORD", secretRef(c.Status.AdminSecret, "password")))
	if u := c.Spec.Database.Users; u != nil {
		ct.Env = append(ct.Env, corev1.EnvVar{Name: "USERS_URL", Value: u.Endpoints[0]}, corev1.EnvVar{Name: "USERS_PREFIX", Value: u.IndexPrefix}, corev1.EnvVar{Name: "USERS_ENGINE", Value: u.Engine})
	}
	if a := c.Spec.Database.BootstrapAuth; a != nil {
		if a.BasicAuthSecretRef != nil {
			ct.Env = append(ct.Env, EnvSecret("BOOTSTRAP_BASIC", a.BasicAuthSecretRef))
		}
		if a.APIKeySecretRef != nil {
			ct.Env = append(ct.Env, EnvSecret("BOOTSTRAP_APIKEY", a.APIKeySecretRef))
		}
	}
	if c.Spec.Database.TLS.ClientCertificateSecret != "" {
		ct.Env = append(ct.Env, corev1.EnvVar{Name: "MTLS", Value: "true"})
	}
	p.Spec.Containers = []corev1.Container{ct}
	m := Meta(c, "schema-"+operation[:8])
	return &batchv1.Job{ObjectMeta: m, Spec: batchv1.JobSpec{BackoffLimit: Ptr(int32(0)), ActiveDeadlineSeconds: Ptr(int64(600)), Template: p}}
}
func Retention(c *api.ArkimeCluster) *batchv1.CronJob {
	p := pod(c, "viewer", "")
	p.Spec.RestartPolicy = corev1.RestartPolicyNever
	p.Labels = Labels(c, "retention")
	ct := container(c, "viewer", 8005)
	ct.Name = "expire"
	ct.Lifecycle = nil
	ct.Command = []string{"/opt/arkime/db/db.pl"}
	ct.Args = []string{"--prefix", cfg.Prefix(c), c.Spec.Database.Endpoints[0], "expire", "daily", strconv.Itoa(int(c.Spec.Retention.SessionsDays))}
	if c.Spec.Database.TLS.ClientCertificateSecret != "" {
		ct.Args = append([]string{"--clientkey", "/var/run/arkime-mtls/tls.key", "--clientcert", "/var/run/arkime-mtls/tls.crt"}, ct.Args...)
	}
	ct.StartupProbe = nil
	ct.ReadinessProbe = nil
	p.Spec.Containers = []corev1.Container{ct}
	return &batchv1.CronJob{ObjectMeta: Meta(c, "retention"), Spec: batchv1.CronJobSpec{Schedule: "17 2 * * *", ConcurrencyPolicy: batchv1.ForbidConcurrent, StartingDeadlineSeconds: Ptr(int64(1800)), SuccessfulJobsHistoryLimit: Ptr(int32(1)), FailedJobsHistoryLimit: Ptr(int32(2)), JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{BackoffLimit: Ptr(int32(1)), ActiveDeadlineSeconds: Ptr(int64(3600)), Template: p}}}}
}
func Ingress(c *api.ArkimeCluster) *networkingv1.Ingress {
	paths := []networkingv1.HTTPIngressPath{}
	for _, r := range []struct {
		k, path string
		port    int32
	}{{"viewer", "/", 8005}, {"cont3xt", "/cont3xt", 3218}} {
		if r.k == "cont3xt" && !api.Enabled(c.Spec.Cont3xt.ComponentSpec) {
			continue
		}
		paths = append(paths, networkingv1.HTTPIngressPath{Path: r.path, PathType: Ptr(networkingv1.PathTypePrefix), Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: cfg.Name(c, r.k), Port: networkingv1.ServiceBackendPort{Name: "http"}}}})
	}
	i := &networkingv1.Ingress{ObjectMeta: Meta(c, "web"), Spec: networkingv1.IngressSpec{IngressClassName: c.Spec.Web.IngressClassName, Rules: []networkingv1.IngressRule{{Host: c.Spec.Web.Host, IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: paths}}}}}}
	if c.Spec.Web.TLSSecret != "" {
		i.Spec.TLS = []networkingv1.IngressTLS{{Hosts: []string{c.Spec.Web.Host}, SecretName: c.Spec.Web.TLSSecret}}
	}
	return i
}
func GatewayObject(c *api.ArkimeCluster, kind, k string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gateway.networking.k8s.io/v1", "kind": kind, "metadata": map[string]any{"name": cfg.Name(c, k), "namespace": c.Namespace}, "spec": spec}}
}
func UDPRoutes(c *api.ArkimeCluster) []client.Object {
	g := c.Spec.Capture.External.Exposure.Gateway
	out := []client.Object{}
	parent := map[string]any{}
	if g.ParentRef != nil {
		parent["name"] = g.ParentRef.Name
		if g.ParentRef.SectionName != "" {
			parent["sectionName"] = g.ParentRef.SectionName
		}
	} else {
		parent["name"] = cfg.Name(c, "udp")
		parent["sectionName"] = "tzsp"
		listener := map[string]any{"name": "tzsp", "protocol": "UDP", "port": int64(37008)}
		out = append(out, GatewayObject(c, "Gateway", "udp", map[string]any{"gatewayClassName": g.GatewayClassName, "listeners": []any{listener}}))
	}
	backend := map[string]any{"name": cfg.Name(c, "udp"), "port": int64(37008)}
	rule := map[string]any{"backendRefs": []any{backend}}
	return append(out, GatewayObject(c, "UDPRoute", "udp", map[string]any{"parentRefs": []any{parent}, "rules": []any{rule}}))
}
func HTTPRoute(c *api.ArkimeCluster) *unstructured.Unstructured {
	p := map[string]any{"name": c.Spec.Web.ParentRef.Name}
	if c.Spec.Web.ParentRef.SectionName != "" {
		p["sectionName"] = c.Spec.Web.ParentRef.SectionName
	}
	rules := []any{}
	for _, v := range []struct {
		k, path string
		port    int64
	}{{"viewer", "/", 8005}, {"cont3xt", "/cont3xt", 3218}} {
		if v.k == "cont3xt" && !api.Enabled(c.Spec.Cont3xt.ComponentSpec) {
			continue
		}
		rules = append(rules, map[string]any{"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": v.path}}}, "backendRefs": []any{map[string]any{"name": cfg.Name(c, v.k), "port": v.port}}})
	}
	return GatewayObject(c, "HTTPRoute", "web", map[string]any{"parentRefs": []any{p}, "hostnames": []any{c.Spec.Web.Host}, "rules": rules})
}

func hasCA(c *api.ArkimeCluster) bool {
	for _, b := range cfg.Backends(c) {
		if b.TLS.CASecretRef != nil {
			return true
		}
	}
	return false
}

func NetworkPolicy(c *api.ArkimeCluster) *networkingv1.NetworkPolicy {
	selector := metav1.LabelSelector{MatchLabels: map[string]string{"arkime.arkime.com/cluster": cfg.ID(c)}}
	peer := networkingv1.NetworkPolicyPeer{PodSelector: &selector}
	policy := &networkingv1.NetworkPolicy{ObjectMeta: Meta(c, "apps"), Spec: networkingv1.NetworkPolicySpec{PodSelector: selector, PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}, Ingress: []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{peer}}}, Egress: []networkingv1.NetworkPolicyEgressRule{{To: []networkingv1.NetworkPolicyPeer{peer}}, {Ports: []networkingv1.NetworkPolicyPort{{Protocol: Ptr(corev1.ProtocolUDP), Port: Ptr(intstr.FromInt(53))}, {Protocol: Ptr(corev1.ProtocolTCP), Port: Ptr(intstr.FromInt(53))}}}}}}
	policy.Spec.Ingress = append(policy.Spec.Ingress, c.Spec.NetworkPolicy.Ingress...)
	policy.Spec.Egress = append(policy.Spec.Egress, c.Spec.NetworkPolicy.Egress...)
	return policy
}
