package controller

import (
	"context"
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	res "github.com/bwagner5/arkime-k8s-operator/internal/resources"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"os"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"testing"
)

func TestEnvtestStructuralAdmission(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("set KUBEBUILDER_ASSETS to run real API admission tests")
	}
	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "charts", "arkime-k8s-operator-crds", "templates")}, ErrorIfCRDPathMissing: true}
	config, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Error(err)
		}
	})
	s := runtime.NewScheme()
	_ = api.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	cl, err := client.New(config, client.Options{Scheme: s})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = cl.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test"}}); err != nil {
		t.Fatal(err)
	}
	c := testCluster()
	c.UID = ""
	if err = cl.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	if c.Spec.Wise.KubernetesEnrichment.Enabled == nil || !*c.Spec.Wise.KubernetesEnrichment.Enabled {
		t.Fatal("enrichment enabled default missing from CRD admission")
	}
	c.Spec.Wise.KubernetesEnrichment.Enabled = res.Ptr(false)
	if err = cl.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err = cl.Get(ctx, client.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	if c.Spec.Wise.KubernetesEnrichment.Enabled == nil || *c.Spec.Wise.KubernetesEnrichment.Enabled {
		t.Fatal("explicit false lost on API round trip")
	}
	// Disabling WISE also disables its default enrichment.
	incompatible := testCluster()
	incompatible.UID = ""
	incompatible.Name = "incompatible-enrichment"
	incompatible.Spec.Wise.Enabled = res.Ptr(false)
	incompatible.Spec.Wise.KubernetesEnrichment = api.KubernetesEnrichmentSpec{Image: "enricher:test"}
	if err = cl.Create(ctx, incompatible); err != nil {
		t.Fatal("disabled WISE should admit dormant enrichment configuration", err)
	}
	invalid := testCluster()
	invalid.UID = ""
	invalid.Name = "invalid"
	invalid.Spec.Database.Auth = api.DatabaseAuth{}
	if err = cl.Create(ctx, invalid); err == nil {
		t.Fatal("authentication ambiguity admitted")
	}
}

func TestEnvtestApplyDoesNotChangeGeneration(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("set KUBEBUILDER_ASSETS")
	}
	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "charts", "arkime-k8s-operator-crds", "templates")}}
	config, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Stop() })
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = api.AddToScheme(scheme)
	cl, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	r := &Reconciler{Client: cl, Scheme: scheme}
	ctx := context.Background()
	if err = cl.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test"}}); err != nil {
		t.Fatal(err)
	}
	c := testCluster()
	c.UID = ""
	if err = cl.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.Status.ResolvedImage = "test:1"
	c.Status.SharedSecret = "test-auth"
	build := func() client.Object { return res.Workload(c, "external", "stable") }
	for i := 0; i < 4; i++ {
		if err = r.apply(ctx, c, build(), false); err != nil {
			t.Fatal(err)
		}
	}
	obj := build()
	if err = cl.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		t.Fatal(err)
	}
	if obj.GetGeneration() != 1 {
		t.Fatalf("unchanged reconcile caused generation %d", obj.GetGeneration())
	}
	c.Spec.Capture.External.Enabled = res.Ptr(false)
	if err = r.apply(ctx, c, build(), false); err != nil {
		t.Fatal(err)
	}
	disabled := &appsv1.Deployment{}
	if err = cl.Get(ctx, client.ObjectKeyFromObject(obj), disabled); err != nil {
		t.Fatal(err)
	}
	if len(disabled.Spec.Template.Spec.Containers) != 1 || disabled.Spec.Template.Spec.Containers[0].Name != "local-viewer" {
		t.Fatal("disabled capture container survived server-side apply")
	}

	// Native sidecar and policy admission must use the real API server: the fake
	// client's NetworkPolicy SSA conversion does not cover this resource.
	c.Spec.Wise.KubernetesEnrichment = api.KubernetesEnrichmentSpec{Image: "enricher:test", APIEgress: []networkingv1.NetworkPolicyEgressRule{{}}, CaptureIngress: []networkingv1.NetworkPolicyIngressRule{{}}}
	wise := res.Workload(c, "wise", "enriched")
	if err = r.apply(ctx, c, wise, false); err != nil {
		t.Fatal(err)
	}
	if err = r.apply(ctx, c, res.EnrichmentNetworkPolicy(c), false); err != nil {
		t.Fatal(err)
	}
	c.Spec.Wise.KubernetesEnrichment.Enabled = boolPtr(false)
	if err = r.apply(ctx, c, res.Workload(c, "wise", "disabled"), false); err != nil {
		t.Fatal(err)
	}
	observedWise := &appsv1.Deployment{}
	if err = cl.Get(ctx, client.ObjectKeyFromObject(wise), observedWise); err != nil {
		t.Fatal(err)
	}
	for _, ct := range observedWise.Spec.Template.Spec.InitContainers {
		if ct.Name == "pod-enricher" {
			t.Fatal("sidecar survived server-side apply disable")
		}
	}

}
