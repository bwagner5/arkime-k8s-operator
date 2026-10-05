package config

import (
	"fmt"
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	"github.com/bwagner5/arkime-k8s-operator/internal/podenrichment"
	"strconv"
	"strings"
)

func validateEnrichment(c *api.ArkimeCluster) error {
	if !api.KubernetesEnrichment(c) {
		return nil
	}
	e := c.Spec.Wise.KubernetesEnrichment
	if e.CaptureCacheSeconds < 0 || (e.MaxStaleSeconds != 0 && e.MaxStaleSeconds < 10) {
		return fmt.Errorf("invalid enrichment cache/freshness duration")
	}
	if c.Spec.NetworkPolicy != nil && (len(e.APIEgress) == 0 || len(e.CaptureIngress) == 0) {
		return fmt.Errorf("enrichment with NetworkPolicy requires apiEgress and captureIngress rules for your cluster")
	}
	check := func(sec, key string) error {
		sec = strings.ToLower(sec)
		key = strings.ToLower(key)
		if sec == "file:kubernetes-pods" || key == "wisecachesecs" || ((sec == "custom-fields" || sec == "custom-fields-remap") && strings.HasPrefix(key, "k8s.")) || (sec == "custom-views" && key == "kubernetes") {
			return fmt.Errorf("enrichment owns %s/%s", sec, key)
		}
		return nil
	}
	for _, comp := range Components(c) {
		for sec, kv := range comp.Config {
			for key := range kv {
				if err := check(sec, key); err != nil {
					return err
				}
			}
		}
		for _, ref := range comp.ConfigSecretRefs {
			if err := check(ref.Section, ref.Key); err != nil {
				return err
			}
		}
	}
	return nil
}
func enrichmentSections(c *api.ArkimeCluster, component string, s map[string]map[string]string) {
	if !api.KubernetesEnrichment(c) {
		return
	}
	if component == "wise" {
		s["file:kubernetes-pods"] = map[string]string{"file": "/var/run/arkime-kubernetes/pods.tagger", "type": "ip", "format": "tagger"}
		return
	}
	if component != "viewer" && component != "node" && component != "external" {
		return
	}
	for _, sec := range []string{"custom-fields", "custom-fields-remap", "custom-views"} {
		if s[sec] == nil {
			s[sec] = map[string]string{}
		}
	}
	fields := []string{}
	for _, f := range podenrichment.Fields {
		generic := "k8s." + f
		for _, prefix := range []string{"k8s.", "k8s.src.", "k8s.dst."} {
			field := prefix + f
			s["custom-fields"][field] = podenrichment.Definition(field)
			if prefix != "k8s." {
				fields = append(fields, field)
			}
		}
		s["custom-fields-remap"][generic] = "ip.src=k8s.src." + f + ";ip.dst=k8s.dst." + f
	}
	s["custom-views"]["kubernetes"] = "title:Kubernetes Endpoints;require:k8s;fields:" + strings.Join(fields, ",")
	if component != "viewer" {
		seconds := c.Spec.Wise.KubernetesEnrichment.CaptureCacheSeconds
		if seconds == 0 {
			seconds = 5
		}
		s["default"]["wiseCacheSecs"] = strconv.Itoa(int(seconds))
	}
}

// DefaultPodEnricherImage follows the Operator release, independently of Arkime.
func DefaultPodEnricherImage(operatorVersion string) string {
	if operatorVersion == "" || operatorVersion == "dev" {
		operatorVersion = "0.0.0-dev"
	}
	return "ghcr.io/bwagner5/arkime-pod-enricher:" + strings.TrimPrefix(operatorVersion, "v")
}
func EnrichmentRBACName(c *api.ArkimeCluster) string { return ID(c) + "-pods-" + Hash(string(c.UID)) }
