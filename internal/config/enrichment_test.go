package config

import (
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	"github.com/bwagner5/arkime-k8s-operator/internal/podenrichment"
	"strings"
	"testing"
)

func enrichmentFixture() *api.ArkimeCluster {
	c := fixture()
	c.Spec.Wise.KubernetesEnrichment = api.KubernetesEnrichmentSpec{Image: "enricher:test"}
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
	c.Spec.Wise.KubernetesEnrichment.Enabled = boolPtr(false)
	if _, ok := Sections(c, "node")["custom-fields-remap"]; ok {
		t.Fatal("disable retained generated fields")
	}
}
func TestEnrichmentConflicts(t *testing.T) {
	for _, change := range []func(*api.ArkimeCluster){
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

func boolPtr(v bool) *bool { return &v }

func TestEnrichmentDefaults(t *testing.T) {
	c := fixture()
	if !api.KubernetesEnrichment(c) {
		t.Fatal("omitted enrichment should be enabled")
	}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
	c.Spec.Wise.KubernetesEnrichment.Enabled = boolPtr(false)
	if api.KubernetesEnrichment(c) {
		t.Fatal("explicit false ignored")
	}
	c.Spec.Wise.KubernetesEnrichment.Enabled = boolPtr(true)
	c.Spec.Wise.Enabled = boolPtr(false)
	if api.KubernetesEnrichment(c) {
		t.Fatal("disabled WISE starts enrichment")
	}
	if err := Validate(c); err != nil {
		t.Fatal("disabled WISE should accept dormant enrichment settings", err)
	}
	if got := DefaultPodEnricherImage("v1.2.3"); got != "ghcr.io/bwagner5/arkime-pod-enricher:1.2.3" {
		t.Fatal(got)
	}
}
