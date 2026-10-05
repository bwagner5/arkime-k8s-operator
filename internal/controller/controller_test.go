package controller

import (
	"context"
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	cfg "github.com/bwagner5/arkime-k8s-operator/internal/config"
	res "github.com/bwagner5/arkime-k8s-operator/internal/resources"
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
	"strings"
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
	// Success is persisted in status, so the operator reclaims the completed Job.
	_ = r.List(ctx, jobs)
	if len(jobs.Items) != 0 {
		t.Fatalf("completed Job not pruned: %#v", jobs.Items)
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
func TestRetryTokenPrunesSupersededBootstrapJob(t *testing.T) {
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
	failed := jobs.Items[0]
	failed.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}
	if err := r.Status().Update(ctx, &failed); err != nil {
		t.Fatal(err)
	}
	// A failure is retained for inspection; the status message points operators at it.
	reconcile()
	_ = r.List(ctx, jobs)
	if len(jobs.Items) != 1 || jobs.Items[0].Name != failed.Name {
		t.Fatalf("failed Job not retained for inspection: %#v", jobs.Items)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	c.Spec.Database.Schema.RetryToken = "retry-1"
	if err := r.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	reconcile()
	_ = r.List(ctx, jobs)
	if len(jobs.Items) != 1 {
		t.Fatalf("superseded Job not pruned: %#v", jobs.Items)
	}
	if jobs.Items[0].Name == failed.Name {
		t.Fatal("retry reused the failed Job name")
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

func nodeCluster() *api.ArkimeCluster {
	c := testCluster()
	c.Spec.Capture = api.CaptureSpec{Node: &api.NodeCapture{Interfaces: []string{"eth0"}, Storage: api.HostStorage{HostPath: "/var/lib/arkime-pcap"}, ViewerPort: 8005}}
	return c
}
func TestCaptureFaultsSurfaceOnStatus(t *testing.T) {
	ctx := context.Background()
	c := nodeCluster()
	r := setup(t, c)
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
	job := &jobs.Items[0]
	job.Status.Succeeded = 1
	if err := r.Status().Update(ctx, job); err != nil {
		t.Fatal(err)
	}
	reconcile()
	reconcile()
	ds := &appsv1.DaemonSet{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: "test", Name: cfg.Name(c, "node")}, ds); err != nil {
		t.Fatal(err)
	}
	reasonOf := func(typ string) (string, string) {
		t.Helper()
		if err := r.Get(ctx, client.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
		for _, cond := range c.Status.Conditions {
			if cond.Type == typ {
				return cond.Reason, cond.Message
			}
		}
		t.Fatalf("no %s condition", typ)
		return "", ""
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "node-abc", Namespace: "test", Labels: res.Labels(c, "node")}, Spec: corev1.PodSpec{NodeName: "worker-1"}}
	if err := r.Create(ctx, pod); err != nil {
		t.Fatal(err)
	}
	pod.Status = corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "capture", LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Message: "vvvvvvv IMPORTANT vvvvvvv\nFATAL CONFIG ERROR - Error setting PROMISC: No such device\n^^^^^^^ IMPORTANT ^^^^^^^\n"}}}}}
	if err := r.Status().Update(ctx, pod); err != nil {
		t.Fatal(err)
	}
	reconcile()
	for _, typ := range []string{"NodeReady", "CaptureReady"} {
		reason, message := reasonOf(typ)
		if reason != "InterfaceUnavailable" {
			t.Fatalf("%s reason = %q, want InterfaceUnavailable (%s)", typ, reason, message)
		}
		if !strings.Contains(message, "worker-1") || !strings.Contains(message, "eth0") || strings.Contains(message, "vvv") {
			t.Fatalf("%s message = %q", typ, message)
		}
	}
	if reason, message := reasonOf("Ready"); reason != "Progressing" || !strings.Contains(message, "No such device") {
		t.Fatalf("Ready = %q / %q; fault not surfaced", reason, message)
	}
	// An unplaceable Pod is the other invisible failure; a container fault outranks it.
	pod.Status = corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable, Message: "0/3 nodes are available: 3 Insufficient memory."}}}
	if err := r.Status().Update(ctx, pod); err != nil {
		t.Fatal(err)
	}
	reconcile()
	if reason, message := reasonOf("CaptureReady"); reason != "Unschedulable" || !strings.Contains(message, "Insufficient memory") {
		t.Fatalf("CaptureReady = %q / %q, want Unschedulable", reason, message)
	}
}
