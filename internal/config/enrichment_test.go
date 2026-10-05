package config

import (
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	"github.com/bwagner5/arkime-k8s-operator/internal/podenrichment"
	"strings"
	"testing"
)

func enrichmentFixture() *api.ArkimeCluster {
	c := fixture()
	c.Spec.Enrichment.Kubernetes = &api.KubernetesEnrichmentSpec{Enabled: true, ClusterName: "home", Image: "enricher:test", ServiceAccountName: "pod-reader"}
	return c
}
func TestEnrichmentConfig(t *testing.T) {
	c := enrichmentFixture()
	if e := Validate(c); e != nil {
		t.Fatal(e)
	}
	for _, component := range []string{"node", "viewer"} {
		s := Sections(c, component)
		for _, f := range podenrichment.Fields {
			if s["custom-fields-remap"]["k8s."+f] != "ip.src=k8s.src."+f+";ip.dst=k8s.dst."+f {
				t.Fatal(s)
			}
			for _, side := range []string{"", "src.", "dst."} {
				if s["custom-fields"]["k8s."+side+f] == "" {
					t.Fatal("undefined field")
				}
			}
		}
		if strings.Contains(s["custom-views"]["kubernetes"], ",k8s.pod") {
			t.Fatal("generic field displayed as endpoint")
		}
	}
	if Sections(c, "node")["default"]["wiseCacheSecs"] != "5" {
		t.Fatal("cache default")
	}
	if Sections(c, "wise")["file:kubernetes-pods"]["format"] != "tagger" {
		t.Fatal("source")
	}
	c.Spec.Enrichment.Kubernetes.Enabled = false
	if _, ok := Sections(c, "node")["custom-fields-remap"]; ok {
		t.Fatal("disable retained generated fields")
	}
}
func TestEnrichmentConflicts(t *testing.T) {
	for _, change := range []func(*api.ArkimeCluster){
		func(c *api.ArkimeCluster) { f := false; c.Spec.Wise.Enabled = &f },
		func(c *api.ArkimeCluster) { c.Spec.Enrichment.Kubernetes.ClusterName = "bad;value" },
		func(c *api.ArkimeCluster) { c.Spec.Enrichment.Kubernetes.ServiceAccountName = "default" },
		func(c *api.ArkimeCluster) { c.Spec.NetworkPolicy = &api.NetworkPolicySpec{} },
		func(c *api.ArkimeCluster) {
			c.Spec.Wise.Config = map[string]map[string]string{"file:kubernetes-pods": {"file": "elsewhere"}}
		},
		func(c *api.ArkimeCluster) {
			c.Spec.Viewer.Config = map[string]map[string]string{"custom-fields": {"k8s.src.pod.name": "other"}}
		},
	} {
		c := enrichmentFixture()
		change(c)
		if Validate(c) == nil {
			t.Fatal("accepted conflict")
		}
	}
}
