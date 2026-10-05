package resources

import (
	"fmt"
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func enrichWise(c *api.ArkimeCluster, p *corev1.PodTemplateSpec, ct *corev1.Container) {
	e := c.Spec.Enrichment.Kubernetes
	p.Spec.ServiceAccountName = e.ServiceAccountName
	p.Spec.Volumes = append(p.Spec.Volumes, corev1.Volume{Name: "pod-inventory", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}, corev1.Volume{Name: "pod-api", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{DefaultMode: Ptr(int32(0444)), Sources: []corev1.VolumeProjection{
		{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{Path: "token", ExpirationSeconds: Ptr(int64(3600))}},
		{ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "kube-root-ca.crt"}, Items: []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}}}},
		{DownwardAPI: &corev1.DownwardAPIProjection{Items: []corev1.DownwardAPIVolumeFile{{Path: "namespace", FieldRef: &corev1.ObjectFieldSelector{APIVersion: "v1", FieldPath: "metadata.namespace"}}}}},
	}}}})
	ct.VolumeMounts = append(ct.VolumeMounts, corev1.VolumeMount{Name: "pod-inventory", MountPath: "/var/run/arkime-kubernetes", ReadOnly: true})
	// WISE must remain reachable to serve an empty inventory while the watcher is
	// unready, so the Service publishes unready endpoints (application probes remain).
	maxStale := e.MaxStaleSeconds
	if maxStale == 0 {
		maxStale = 60
	}
	probe := func(path string) *corev1.Probe {
		return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt(8082)}}, PeriodSeconds: 2, FailureThreshold: 3}
	}
	watcher := corev1.Container{Name: "pod-enricher", Image: e.Image, ImagePullPolicy: corev1.PullIfNotPresent, RestartPolicy: Ptr(corev1.ContainerRestartPolicyAlways), Args: []string{"--cluster-name=" + e.ClusterName, fmt.Sprintf("--max-stale=%ds", maxStale), fmt.Sprintf("--refresh=%ds", maxStale/3)}, Resources: *e.Resources.DeepCopy(), SecurityContext: ct.SecurityContext.DeepCopy(), Ports: []corev1.ContainerPort{{Name: "pod-health", ContainerPort: 8082}}, VolumeMounts: []corev1.VolumeMount{{Name: "pod-inventory", MountPath: "/var/run/arkime-kubernetes"}, {Name: "pod-api", MountPath: "/var/run/secrets/kubernetes.io/serviceaccount", ReadOnly: true}}, StartupProbe: probe("/readyz"), ReadinessProbe: probe("/readyz"), LivenessProbe: probe("/livez")}
	watcher.StartupProbe.FailureThreshold = 60
	if len(watcher.Resources.Requests) == 0 {
		watcher.Resources.Requests = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("64Mi")}
	}
	p.Spec.InitContainers = append(p.Spec.InitContainers, watcher)
}
func EnrichmentNetworkPolicy(c *api.ArkimeCluster) *networkingv1.NetworkPolicy {
	e := c.Spec.Enrichment.Kubernetes
	return &networkingv1.NetworkPolicy{ObjectMeta: Meta(c, "enrichment"), Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: Labels(c, "wise")}, PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}, Ingress: e.CaptureIngress, Egress: e.APIEgress}}
}
