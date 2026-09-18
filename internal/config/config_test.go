package config

import (
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
)

func fixture() *api.ArkimeCluster {
	return &api.ArkimeCluster{ObjectMeta: metav1.ObjectMeta{Name: "home", Namespace: "arkime"}, Spec: api.ArkimeClusterSpec{Database: api.DatabaseSpec{Backend: api.Backend{Engine: "OpenSearch", Endpoints: []string{"https://db:9200"}, Auth: api.DatabaseAuth{Unauthenticated: true}}}, Capture: api.CaptureSpec{Node: &api.NodeCapture{Interfaces: []string{"eno1"}, Storage: api.HostStorage{HostPath: "/var/lib/pcap"}}}, Retention: api.RetentionSpec{SessionsDays: 7}}}
}
func TestValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*api.ArkimeCluster)
		bad    bool
	}{{"valid", func(c *api.ArkimeCluster) {}, false}, {"missing-interface", func(c *api.ArkimeCluster) { c.Spec.Capture.Node.Interfaces = nil }, true}, {"root-storage", func(c *api.ArkimeCluster) { c.Spec.Capture.Node.Storage.HostPath = "/" }, true}, {"url-credentials", func(c *api.ArkimeCluster) { c.Spec.Database.Endpoints = []string{"https://user:pass@db:9200"} }, true}, {"owned-setting", func(c *api.ArkimeCluster) {
		c.Spec.Viewer.Config = map[string]map[string]string{"default": {"elasticsearch": "https://other"}}
	}, true}, {"ini-injection", func(c *api.ArkimeCluster) {
		c.Spec.Viewer.Config = map[string]map[string]string{"default": {"foo": "bar\npasswordSecret=bad"}}
	}, true}, {"retained-viewer", func(c *api.ArkimeCluster) {
		f := false
		c.Spec.Capture.Node.Enabled = &f
		c.Status.NodeStorageRetained = true
		c.Spec.Capture.Node.Interfaces = nil
	}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture()
			tc.change(c)
			if err := Validate(c); (err != nil) != tc.bad {
				t.Fatalf("Validate()=%v", err)
			}
		})
	}
}
func TestIdentityAndRendering(t *testing.T) {
	c := fixture()
	old := ID(c)
	c.ResourceVersion = "2"
	if ID(c) != old {
		t.Fatal("identity changed")
	}
	d := c.DeepCopy()
	d.Namespace = "other"
	if Prefix(c) == Prefix(d) {
		t.Fatal("namespace collision")
	}
	s := Render(Sections(c, "viewer"))
	if !strings.Contains(s, "wiseURL=http://"+Name(c, "wise")+".arkime.svc:8081") {
		t.Fatal(s)
	}
	if strings.Contains(s, "cluster.local") {
		t.Fatal("hard coded cluster suffix")
	}
	for i := 0; i < 20; i++ {
		if Render(Sections(c, "viewer")) != s {
			t.Fatal("nondeterministic INI")
		}
	}
}
