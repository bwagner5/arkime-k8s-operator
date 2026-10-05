package resources

import (
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	cfg "github.com/bwagner5/arkime-k8s-operator/internal/config"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"testing"
)

func TestEnrichmentIsolationAndOrdering(t *testing.T) {
	c := &api.ArkimeCluster{}
	c.Spec.Wise.KubernetesEnrichment = api.KubernetesEnrichmentSpec{Image: "enricher:test", APIEgress: []networkingv1.NetworkPolicyEgressRule{{}}, CaptureIngress: []networkingv1.NetworkPolicyIngressRule{{}}}
	d := Workload(c, "wise", "hash").(*appsv1.Deployment)
	p := d.Spec.Template.Spec
	if p.ServiceAccountName != cfg.Name(c, "pod-enricher") || *p.AutomountServiceAccountToken || len(p.InitContainers) != 1 {
		t.Fatal("wrong service account/sidecar")
	}
	sidecar := p.InitContainers[0]
	if *sidecar.RestartPolicy != corev1.ContainerRestartPolicyAlways || sidecar.StartupProbe.HTTPGet.Path != "/readyz" || sidecar.ReadinessProbe == nil {
		t.Fatal("startup ordering/readiness")
	}
	for _, ct := range p.Containers {
		for _, m := range ct.VolumeMounts {
			if m.Name == "pod-api" {
				t.Fatal("application receives API token")
			}
			if m.Name == "pod-inventory" && !m.ReadOnly {
				t.Fatal("application can overwrite inventory")
			}
		}
	}
	if len(sidecar.Env) != 0 {
		t.Fatal("watcher inherits database credentials")
	}
	if !Service(c, "wise", 8081, corev1.ProtocolTCP).Spec.PublishNotReadyAddresses {
		t.Fatal("freshness loss blocks header-only service")
	}
	np := EnrichmentNetworkPolicy(c)
	if np.Spec.PodSelector.MatchLabels["app.kubernetes.io/component"] != "wise" || len(np.Spec.Egress) != 1 {
		t.Fatal("API access not scoped to WISE")
	}
	c.Spec.Wise.KubernetesEnrichment.Enabled = boolPtr(false)
	p = Workload(c, "wise", "hash").(*appsv1.Deployment).Spec.Template.Spec
	if len(p.InitContainers) != 0 || p.ServiceAccountName == cfg.Name(c, "pod-enricher") {
		t.Fatal("disable retains watcher")
	}
}

func boolPtr(v bool) *bool { return &v }

func TestEnrichmentDefaultImageAndIdentity(t *testing.T) {
	c := &api.ArkimeCluster{}
	c.Name = "home"
	c.Namespace = "arkime"
	image := "ghcr.io/bwagner5/arkime-pod-enricher:1.2.3"
	p := Workload(c, "wise", "hash", image).(*appsv1.Deployment).Spec.Template.Spec
	if len(p.InitContainers) != 1 || p.InitContainers[0].Image != image {
		t.Fatal("release default image not used")
	}
	if p.InitContainers[0].Args[0] != "--cluster-name=arkime/home" {
		t.Fatal(p.InitContainers[0].Args)
	}
	c.Spec.Wise.KubernetesEnrichment.Image = "private.example/enricher@sha256:override"
	p = Workload(c, "wise", "hash", image).(*appsv1.Deployment).Spec.Template.Spec
	if p.InitContainers[0].Image != c.Spec.Wise.KubernetesEnrichment.Image {
		t.Fatal("image override ignored")
	}
}
