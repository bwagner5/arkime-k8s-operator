package controller

import (
	"context"
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	cfg "github.com/bwagner5/arkime-k8s-operator/internal/config"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"testing"
)

func TestEnrichmentLifecycleAndNoCaptureRollout(t *testing.T) {
	ctx := context.Background()
	c := testCluster()

	c.Spec.Enrichment.Kubernetes = &api.KubernetesEnrichmentSpec{Enabled: true, ClusterName: "test", Image: "enricher:test", ServiceAccountName: "pod-reader", APIEgress: []networkingv1.NetworkPolicyEgressRule{{To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.1/32"}}}}}, CaptureIngress: []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.1.0/24"}}}}}}
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "pod-reader", Namespace: "test"}}
	r := setup(t, c, sa, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "test"}})
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
	c.Spec.Enrichment.Kubernetes.Enabled = false
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
