package controller

import (
	"context"
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	cfg "github.com/bwagner5/arkime-k8s-operator/internal/config"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"testing"
)

func TestEnrichmentLifecycleAndNoCaptureRollout(t *testing.T) {
	ctx := context.Background()
	c := testCluster()

	c.Spec.Wise.KubernetesEnrichment = api.KubernetesEnrichmentSpec{Image: "enricher:test", APIEgress: []networkingv1.NetworkPolicyEgressRule{{To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.1/32"}}}}}, CaptureIngress: []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.1.0/24"}}}}}}
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "pod-reader", Namespace: "test"}}
	r := setup(t, c, sa, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "test"}})
	r.PodEnricherImage = "ghcr.io/bwagner5/arkime-pod-enricher:1.2.3"
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(c)}
	reconcile := func() {
		t.Helper()
		if _, e := r.Reconcile(ctx, req); e != nil {
			t.Fatal(e)
		}
	}
	reconcile()
	jobs := &batchv1.JobList{}
	if e := r.List(ctx, jobs); e != nil {
		t.Fatal(e)
	}
	if len(jobs.Items) != 1 {
		t.Fatal("no bootstrap")
	}
	jobs.Items[0].Status.Succeeded = 1
	if e := r.Status().Update(ctx, &jobs.Items[0]); e != nil {
		t.Fatal(e)
	}
	reconcile()
	reconcile()
	enrichedWise := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: cfg.Name(c, "wise")}, enrichedWise); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ct := range enrichedWise.Spec.Template.Spec.InitContainers {
		if ct.Name == "pod-enricher" {
			found = true
			if ct.Image != "enricher:test" {
				t.Fatal("CR image override ignored", ct.Image)
			}
		}
	}
	if !found {
		t.Fatal("enricher not deployed")
	}
	key := client.ObjectKey{Namespace: c.Namespace, Name: cfg.Name(c, "external")}
	before := &appsv1.Deployment{}
	if e := r.Get(ctx, key, before); e != nil {
		t.Fatal(e)
	}
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "arbitrary-workload", Namespace: "test"}, Status: corev1.PodStatus{PodIP: "10.0.0.1"}}
	if e := r.Create(ctx, p); e != nil {
		t.Fatal(e)
	}
	reconcile()
	if e := r.Delete(ctx, p); e != nil {
		t.Fatal(e)
	}
	reconcile()
	after := &appsv1.Deployment{}
	if e := r.Get(ctx, key, after); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before.Spec.Template, after.Spec.Template) {
		t.Fatal("pod churn rolled capture")
	}
	if e := r.Get(ctx, req.NamespacedName, c); e != nil {
		t.Fatal(e)
	}
	// Seed a previously owned policy to verify cleanup independently of the fake
	// client's unsupported NetworkPolicy server-side apply schema conversion.
	ownedPolicy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: cfg.Name(c, "enrichment"), Namespace: c.Namespace}}
	if e := ctrl.SetControllerReference(c, ownedPolicy, r.Scheme); e != nil {
		t.Fatal(e)
	}
	if e := r.Create(ctx, ownedPolicy); e != nil {
		t.Fatal(e)
	}
	c.Spec.Wise.KubernetesEnrichment.Enabled = boolPtr(false)
	if e := r.Update(ctx, c); e != nil {
		t.Fatal(e)
	}
	reconcile()
	wise := &appsv1.Deployment{}
	if e := r.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: cfg.Name(c, "wise")}, wise); e != nil {
		t.Fatal(e)
	}
	for _, ct := range wise.Spec.Template.Spec.InitContainers {
		if ct.Name == "pod-enricher" {
			t.Fatal("watcher survived disable")
		}
	}
	policies := &networkingv1.NetworkPolicyList{}
	if e := r.List(ctx, policies); e != nil {
		t.Fatal(e)
	}
	for _, np := range policies.Items {
		if np.Name == cfg.Name(c, "enrichment") {
			t.Fatal("enrichment policy leaked")
		}
	}
	if e := r.Get(ctx, client.ObjectKeyFromObject(sa), sa); e != nil {
		t.Fatal("administrator-owned service account removed")
	}
}

func boolPtr(v bool) *bool { return &v }

func TestAutomaticEnrichmentRBAC(t *testing.T) {
	ctx := context.Background()
	c := testCluster()
	r := setup(t, c)
	if err := r.enrichment(ctx, c); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(c, enrichmentFinalizer) {
		t.Fatal("no cleanup finalizer")
	}
	role, binding := enrichmentRBAC(c)
	if err := r.Get(ctx, client.ObjectKeyFromObject(role), role); err != nil {
		t.Fatal(err)
	}
	if len(role.OwnerReferences) != 0 || len(role.Rules) != 1 || !reflect.DeepEqual(role.Rules[0].Resources, []string{"pods"}) || !reflect.DeepEqual(role.Rules[0].Verbs, []string{"get", "list", "watch"}) {
		t.Fatal("incorrect pod-reader permissions", role)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(binding), binding); err != nil {
		t.Fatal(err)
	}
	if len(binding.OwnerReferences) != 0 || len(binding.Subjects) != 1 || binding.Subjects[0].Name != cfg.Name(c, "pod-enricher") || binding.Subjects[0].Namespace != c.Namespace {
		t.Fatal(binding)
	}
	sa := &corev1.ServiceAccount{}
	key := client.ObjectKey{Namespace: c.Namespace, Name: cfg.Name(c, "pod-enricher")}
	if err := r.Get(ctx, key, sa); err != nil {
		t.Fatal(err)
	}
	if *sa.AutomountServiceAccountToken {
		t.Fatal("automount enabled")
	}
	// Drift is repaired without broadening the role.
	role.Rules = append(role.Rules, rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get"}})
	if err := r.Update(ctx, role); err != nil {
		t.Fatal(err)
	}
	if err := r.enrichment(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(role), role); err != nil {
		t.Fatal(err)
	}
	if len(role.Rules) != 1 {
		t.Fatal("role drift not corrected")
	}
	c.Spec.Wise.KubernetesEnrichment.Enabled = boolPtr(false)
	if err := r.enrichment(ctx, c); err != nil {
		t.Fatal(err)
	}
	for _, obj := range []client.Object{role, binding, sa} {
		if err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj); !apierrors.IsNotFound(err) {
			t.Fatal("resource survived disable", err)
		}
	}
	if controllerutil.ContainsFinalizer(c, enrichmentFinalizer) {
		t.Fatal("finalizer survived cleanup")
	}
}
func TestEnrichmentDeletionAndOwnership(t *testing.T) {
	ctx := context.Background()
	c := testCluster()
	r := setup(t, c)
	if err := r.enrichment(ctx, c); err != nil {
		t.Fatal(err)
	}
	role, binding := enrichmentRBAC(c)
	replacement := c.DeepCopy()
	replacement.UID = "replacement"
	newRole, _ := enrichmentRBAC(replacement)
	if newRole.Name == role.Name {
		t.Fatal("UID replacement reuses cluster-wide RBAC")
	}
	// Leave another instance's grant untouched during cleanup.
	if err := r.Create(ctx, newRole); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(c)}); err != nil {
		t.Fatal(err)
	}
	for _, obj := range []client.Object{role, binding} {
		if err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj); !apierrors.IsNotFound(err) {
			t.Fatal("grant survived CR deletion", err)
		}
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(newRole), newRole); err != nil {
		t.Fatal("replacement grant removed", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(c), c); !apierrors.IsNotFound(err) {
		t.Fatal("CR deletion stuck", err)
	}
}
func TestEnrichmentRefusesForeignGrant(t *testing.T) {
	ctx := context.Background()
	c := testCluster()
	role, _ := enrichmentRBAC(c)
	role.Labels[enrichmentOwnerUID] = "other"
	r := setup(t, c, role)
	if err := r.enrichment(ctx, c); err == nil {
		t.Fatal("foreign grant adopted")
	}
	if err := r.cleanupEnrichment(ctx, c); err == nil {
		t.Fatal("foreign grant deleted")
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(role), role); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultEnrichmentUsesOperatorImage(t *testing.T) {
	c := testCluster()
	ctx := context.Background()
	r := setup(t, c, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "test"}})
	r.PodEnricherImage = "ghcr.io/bwagner5/arkime-pod-enricher:9.8.7"
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(c)}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	jobs := &batchv1.JobList{}
	if err := r.List(ctx, jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 1 {
		t.Fatal("missing schema job")
	}
	jobs.Items[0].Status.Succeeded = 1
	if err := r.Status().Update(ctx, &jobs.Items[0]); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	wise := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: cfg.Name(c, "wise")}, wise); err != nil {
		t.Fatal(err)
	}
	for _, ct := range wise.Spec.Template.Spec.InitContainers {
		if ct.Name == "pod-enricher" {
			if ct.Image != r.PodEnricherImage {
				t.Fatal("release image lost", ct.Image)
			}
			return
		}
	}
	t.Fatal("default enrichment sidecar missing")
}
