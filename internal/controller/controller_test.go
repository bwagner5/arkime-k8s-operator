package controller

import (
	"context"
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	cfg "github.com/bwagner5/arkime-k8s-operator/internal/config"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func testCluster() *api.ArkimeCluster {
	return &api.ArkimeCluster{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test", UID: types.UID("test-uid")}, Spec: api.ArkimeClusterSpec{Database: api.DatabaseSpec{Backend: api.Backend{Engine: "OpenSearch", Endpoints: []string{"http://db:9200"}, Auth: api.DatabaseAuth{Unauthenticated: true}}}, Capture: api.CaptureSpec{External: &api.ExternalCapture{Storage: api.ClaimStorage{ExistingClaim: "data"}, Exposure: api.UDPExposure{Mode: "ClusterIP"}}}, Retention: api.RetentionSpec{Mode: "External"}}}
}
func setup(t *testing.T, objects ...client.Object) *Reconciler {
	t.Helper()
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = api.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&api.ArkimeCluster{}, &batchv1.Job{}, &appsv1.Deployment{}, &appsv1.DaemonSet{}).WithObjects(objects...).Build()
	return &Reconciler{Client: cl, Scheme: s}
}
func TestBootstrapGatesWritersAndSurvivesJobDeletion(t *testing.T) {
	ctx := context.Background()
	c := testCluster()
	r := setup(t, c, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "test"}})
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(c)}
	reconcile := func() {
		t.Helper()
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	reconcile()
	jobs := &batchv1.JobList{}
	_ = r.List(ctx, jobs)
	if len(jobs.Items) != 1 {
		t.Fatalf("wanted one bootstrap Job: %#v", jobs.Items)
	}
	deployments := &appsv1.DeploymentList{}
	_ = r.List(ctx, deployments)
	if len(deployments.Items) != 0 {
		t.Fatal("writers started before schema")
	}
	job := &jobs.Items[0]
	job.Status.Succeeded = 1
	if err := r.Status().Update(ctx, job); err != nil {
		t.Fatal(err)
	}
	reconcile()
	reconcile()
	_ = r.List(ctx, deployments)
	if len(deployments.Items) != 4 {
		t.Fatalf("wanted 4 deployments, got %d", len(deployments.Items))
	}
	if err := r.Delete(ctx, job); err != nil {
		t.Fatal(err)
	}
	reconcile()
	_ = r.List(ctx, jobs)
	if len(jobs.Items) != 0 {
		t.Fatal("completed Job recreated")
	}
	secrets := &corev1.SecretList{}
	_ = r.List(ctx, secrets)
	for _, s := range secrets.Items {
		if len(s.OwnerReferences) != 0 {
			t.Fatal("recovery secret would be garbage collected")
		}
	}
}
func TestRefusesUnownedObject(t *testing.T) {
	ctx := context.Background()
	c := testCluster()
	s := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: cfg.Name(c, "viewer"), Namespace: c.Namespace}}
	r := setup(t, c, s)
	if err := r.apply(ctx, c, s.DeepCopy(), false); err == nil {
		t.Fatal("adopted unowned Service")
	}
}
func TestManagedPrefixConflict(t *testing.T) {
	ctx := context.Background()
	c := testCluster()
	other := c.DeepCopy()
	other.Name = "other"
	other.UID = "other"
	c.Spec.Database.IndexPrefix = "same"
	other.Spec.Database.IndexPrefix = "same"
	r := setup(t, c, other)
	if err := r.conflicts(ctx, c); err == nil {
		t.Fatal("duplicate schema ownership accepted")
	}
}

func TestUpgradeNeedsApprovalAndQuiesces(t *testing.T) {
	ctx := context.Background()
	c := testCluster()
	c.Spec.Version = "6.7.0"
	c.Status.AppliedVersion = "6.6.0"
	c.Status.ResolvedVersion = "6.6.0"
	c.Status.CompletedOperation = "old"
	r := setup(t, c)
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(c)}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, req.NamespacedName, c); err != nil {
		t.Fatal(err)
	}
	if c.Status.UpgradePhase != "" {
		t.Fatal("upgrade started without approval")
	}
	c.Spec.Database.Schema.ApprovedUpgradeVersion = "6.7.0"
	if err := r.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	_ = r.Get(ctx, req.NamespacedName, c)
	if c.Status.UpgradePhase != "Quiescing" || c.Status.UpgradeTarget != "6.7.0" {
		t.Fatalf("approval not durably recorded: %+v", c.Status)
	}
	// A controller restart resumes the recorded state.
	r = &Reconciler{Client: r.Client, Scheme: r.Scheme}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	_ = r.Get(ctx, req.NamespacedName, c)
	if c.Status.UpgradePhase != "Migrating" {
		t.Fatal("did not resume upgrade after restart")
	}
}

func TestRouteParentMatching(t *testing.T) {
	wanted := []any{map[string]any{"name": "edge", "sectionName": "udp"}}
	for _, tc := range []struct {
		name string
		ref  map[string]any
		want bool
	}{
		{"default namespace", map[string]any{"name": "edge", "sectionName": "udp"}, true},
		{"explicit namespace", map[string]any{"name": "edge", "namespace": "test", "sectionName": "udp", "kind": "Gateway"}, true},
		{"obsolete gateway", map[string]any{"name": "old", "sectionName": "udp"}, false},
		{"other listener", map[string]any{"name": "edge", "sectionName": "http"}, false},
		{"other namespace", map[string]any{"name": "edge", "namespace": "other", "sectionName": "udp"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesParent(tc.ref, wanted, "test"); got != tc.want {
				t.Fatalf("matched=%v, want %v", got, tc.want)
			}
		})
	}
}
