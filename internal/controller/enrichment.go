package controller

import (
	"context"
	"fmt"
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	cfg "github.com/bwagner5/arkime-k8s-operator/internal/config"
	res "github.com/bwagner5/arkime-k8s-operator/internal/resources"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const enrichmentFinalizer = "arkime.arkime.com/pod-enrichment-rbac"
const enrichmentOwnerUID = "arkime.arkime.com/owner-uid"

// Cluster-scoped grants cannot have a namespaced owner reference. A UID-specific
// name plus an ownership label protects replacements; the finalizer removes them.
func enrichmentRBAC(c *api.ArkimeCluster) (*rbacv1.ClusterRole, *rbacv1.ClusterRoleBinding) {
	name := cfg.EnrichmentRBACName(c)
	meta := metav1.ObjectMeta{Name: name, Labels: res.Labels(c, "pod-enricher")}
	meta.Labels[enrichmentOwnerUID] = string(c.UID)
	role := &rbacv1.ClusterRole{ObjectMeta: meta, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list", "watch"}}}}
	binding := &rbacv1.ClusterRoleBinding{ObjectMeta: *meta.DeepCopy(), RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: name}, Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: cfg.Name(c, "pod-enricher"), Namespace: c.Namespace}}}
	return role, binding
}
func (r *Reconciler) enrichment(ctx context.Context, c *api.ArkimeCluster) error {
	if !api.KubernetesEnrichment(c) {
		return r.cleanupEnrichment(ctx, c)
	}
	if !controllerutil.ContainsFinalizer(c, enrichmentFinalizer) {
		before := c.DeepCopy()
		controllerutil.AddFinalizer(c, enrichmentFinalizer)
		if err := r.Patch(ctx, c, client.MergeFrom(before)); err != nil {
			return err
		}
	}
	sa := &corev1.ServiceAccount{ObjectMeta: res.Meta(c, "pod-enricher"), AutomountServiceAccountToken: res.Ptr(false)}
	if err := r.apply(ctx, c, sa, false); err != nil {
		return err
	}
	role, binding := enrichmentRBAC(c)
	for _, desired := range []client.Object{role, binding} {
		current := desired.DeepCopyObject().(client.Object)
		err := r.Get(ctx, client.ObjectKeyFromObject(desired), current)
		if apierrors.IsNotFound(err) {
			if err = r.Create(ctx, desired); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if current.GetLabels()[enrichmentOwnerUID] != string(c.UID) {
			return fmt.Errorf("refusing to modify unowned enrichment RBAC %s", current.GetName())
		}
		switch obj := current.(type) {
		case *rbacv1.ClusterRole:
			if reflect.DeepEqual(obj.Rules, role.Rules) {
				continue
			}
			obj.Rules = role.Rules
		case *rbacv1.ClusterRoleBinding:
			if obj.RoleRef != binding.RoleRef {
				return fmt.Errorf("enrichment binding %s has an unexpected role", obj.Name)
			}
			if reflect.DeepEqual(obj.Subjects, binding.Subjects) {
				continue
			}
			obj.Subjects = binding.Subjects
		}
		if err = r.Update(ctx, current); err != nil {
			return err
		}
	}
	return nil
}
func (r *Reconciler) cleanupEnrichment(ctx context.Context, c *api.ArkimeCluster) error {
	if !controllerutil.ContainsFinalizer(c, enrichmentFinalizer) {
		return nil
	}
	role, binding := enrichmentRBAC(c)
	for _, obj := range []client.Object{binding, role} {
		err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		if obj.GetLabels()[enrichmentOwnerUID] != string(c.UID) {
			return fmt.Errorf("refusing to remove unowned enrichment RBAC %s", obj.GetName())
		}
		uid := obj.GetUID()
		rv := obj.GetResourceVersion()
		if err = r.Delete(ctx, obj, client.Preconditions{UID: &uid, ResourceVersion: &rv}); client.IgnoreNotFound(err) != nil {
			return err
		}
		// Preserve the finalizer if another finalizer is holding the grant alive.
		if len(obj.GetFinalizers()) != 0 {
			return fmt.Errorf("waiting for enrichment RBAC %s deletion", obj.GetName())
		}
	}
	if err := r.remove(ctx, c, &corev1.ServiceAccount{ObjectMeta: res.Meta(c, "pod-enricher")}); err != nil {
		return err
	}
	before := c.DeepCopy()
	controllerutil.RemoveFinalizer(c, enrichmentFinalizer)
	return r.Patch(ctx, c, client.MergeFrom(before))
}
