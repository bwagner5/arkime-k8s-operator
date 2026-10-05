package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	cfg "github.com/bwagner5/arkime-k8s-operator/internal/config"
	res "github.com/bwagner5/arkime-k8s-operator/internal/resources"
	"github.com/bwagner5/arkime-k8s-operator/internal/version"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"net"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sort"
	"strings"
	"time"
)

// +kubebuilder:rbac:groups=arkime.arkime.com,resources=arkimeclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=arkime.arkime.com,resources=arkimeclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets;configmaps;services;serviceaccounts;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments;daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs;cronjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses;networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways;udproutes;httproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch

type Reconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *Reconciler) SetupWithManager(m ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(m).For(&api.ArkimeCluster{}).Owns(&appsv1.Deployment{}).Owns(&appsv1.DaemonSet{}).Owns(&batchv1.Job{}).Owns(&corev1.ConfigMap{}).Owns(&corev1.Service{}).Complete(r)
}
func condition(c *api.ArkimeCluster, t string, ok bool, reason, message string) {
	status := metav1.ConditionFalse
	if ok {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{Type: t, Status: status, Reason: reason, Message: message, ObservedGeneration: c.Generation})
}
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c := &api.ArkimeCluster{}
	if err := r.Get(ctx, req.NamespacedName, c); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !c.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	before := c.DeepCopy()
	finish := func(reason, message string, delay time.Duration) (ctrl.Result, error) {
		condition(c, "Ready", reason == "Available", reason, message)
		condition(c, "Progressing", reason == "Progressing" || reason == "Initializing" || reason == "Quiescing" || reason == "Migrating", reason, message)
		c.Status.ObservedGeneration = c.Generation
		if !reflect.DeepEqual(before.Status, c.Status) {
			if err := r.Status().Patch(ctx, c, client.MergeFrom(before)); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{RequeueAfter: delay}, nil
	}
	if err := cfg.Validate(c); err != nil {
		return finish("InvalidSpec", err.Error(), time.Minute)
	}
	v, image, err := version.Resolve(c.Spec.Version, c.Status.ResolvedVersion, c.Spec.Image.Reference, c.Spec.Image.AllowUnverified)
	if err != nil {
		return finish("UnsupportedVersion", err.Error(), time.Minute)
	}
	if c.Status.IndexPrefix != "" && c.Status.IndexPrefix != cfg.Prefix(c) {
		return finish("ImmutablePrefix", "index prefix cannot change after resolution", time.Minute)
	}
	if err = r.conflicts(ctx, c); err != nil {
		return finish("OwnershipConflict", err.Error(), time.Minute)
	}
	identity := cfg.DatabaseIdentity(c)
	if c.Status.DatabaseIdentity != "" && c.Status.DatabaseIdentity != identity {
		return finish("ImmutableDatabase", "database endpoint and prefix topology require a migration", time.Minute)
	}
	c.Status.DatabaseIdentity = identity
	c.Status.ResolvedVersion = v
	c.Status.ResolvedImage = image
	c.Status.IndexPrefix = cfg.Prefix(c)

	if c.Status.UpgradeTarget != "" && v != c.Status.UpgradeTarget {
		return finish("UpgradeInProgress", "complete the recorded upgrade target before changing version again", time.Minute)
	}
	if c.Status.AppliedVersion != "" && c.Status.AppliedVersion != v {
		if !version.CanUpgrade(c.Status.AppliedVersion, v) {
			return finish("UnsupportedUpgrade", "only the pinned 6.6.0 to 6.7.0 transition is implemented; downgrades are forbidden", time.Minute)
		}
		condition(c, "UpgradeRequired", true, "ApprovalRequired", "snapshot the database and approve the exact target version")
		if c.Status.UpgradeTarget == "" {
			if c.Spec.Database.Schema.ApprovedUpgradeVersion != v {
				return finish("UpgradeRequired", "database.schema.approvedUpgradeVersion must match requested version", time.Minute)
			}
			c.Status.UpgradeTarget = v
			c.Status.UpgradePhase = "Quiescing"
			return finish("Quiescing", "upgrade approval recorded; stopping writers before migration", time.Second)
		}
		if c.Status.UpgradePhase == "Quiescing" {
			for _, k := range []string{"viewer", "cont3xt", "wise", "external"} {
				if err = r.remove(ctx, c, &appsv1.Deployment{ObjectMeta: res.Meta(c, k)}); err != nil {
					return ctrl.Result{}, err
				}
			}
			if err = r.remove(ctx, c, &appsv1.DaemonSet{ObjectMeta: res.Meta(c, "node")}); err != nil {
				return ctrl.Result{}, err
			}
			if err = r.remove(ctx, c, &batchv1.CronJob{ObjectMeta: res.Meta(c, "retention")}); err != nil {
				return ctrl.Result{}, err
			}
			pods := &corev1.PodList{}
			if err = r.List(ctx, pods, client.InNamespace(c.Namespace), client.MatchingLabels{"arkime.arkime.com/cluster": cfg.ID(c)}); err != nil {
				return ctrl.Result{}, err
			}
			for _, p := range pods.Items {
				if p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
					return finish("Quiescing", "waiting for application shutdown and PCAP flush", 5*time.Second)
				}
			}
			c.Status.UpgradePhase = "Migrating"
			return finish("Migrating", "writers stopped; starting approved schema operation", time.Second)
		}
	}
	condition(c, "UnverifiedImage", c.Spec.Image.Reference != "", "ImageOverride", "explicit image overrides require independent qualification")

	condition(c, "UpgradeRequired", false, "NoUpgrade", "no schema upgrade pending")
	if err = r.auth(ctx, c); err != nil {
		return finish("SecretUnavailable", err.Error(), 30*time.Second)
	}
	digest, err := r.dependencies(ctx, c)
	if err != nil {
		return finish("DependencyUnavailable", err.Error(), 30*time.Second)
	}
	sa := &corev1.ServiceAccount{ObjectMeta: res.Meta(c, "app"), AutomountServiceAccountToken: res.Ptr(false)}
	if err = r.apply(ctx, c, sa, false); err != nil {
		return ctrl.Result{}, err
	}
	if api.KubernetesEnrichment(c) && c.Spec.NetworkPolicy != nil {
		if err = r.apply(ctx, c, res.EnrichmentNetworkPolicy(c), false); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		if err = r.remove(ctx, c, &networkingv1.NetworkPolicy{ObjectMeta: res.Meta(c, "enrichment")}); err != nil {
			return ctrl.Result{}, err
		}
	}
	if c.Spec.NetworkPolicy != nil {
		if err = r.apply(ctx, c, res.NetworkPolicy(c), false); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		if err = r.remove(ctx, c, &networkingv1.NetworkPolicy{ObjectMeta: res.Meta(c, "apps")}); err != nil {
			return ctrl.Result{}, err
		}
	}
	for k := range cfg.Components(c) {
		cm := &corev1.ConfigMap{ObjectMeta: res.Meta(c, k), Data: map[string]string{"config.ini": cfg.Render(cfg.Sections(c, k))}}
		if k == "viewer" {
			cm.Data["bootstrap.js"] = res.BootstrapScript
		}
		if err = r.apply(ctx, c, cm, false); err != nil {
			return ctrl.Result{}, err
		}
	}
	// Operation identity excludes credentials: rotation must not rerun initialization.
	data, _ := json.Marshal(struct {
		DB    api.DatabaseSpec
		Image string
	}{c.Spec.Database, image})
	operation := cfg.Hash(string(data))
	if c.Status.CompletedOperation != "" && c.Status.AppliedVersion == v {
		operation = c.Status.CompletedOperation
	}
	if c.Status.CompletedOperation != operation {
		job := res.Bootstrap(c, operation)
		c.Status.SchemaOperation = operation
		c.Status.SchemaJob = job.Name
		current := &batchv1.Job{}
		err = r.Get(ctx, client.ObjectKeyFromObject(job), current)
		if apierrors.IsNotFound(err) {
			if err = r.apply(ctx, c, job, false); err != nil {
				return ctrl.Result{}, err
			}
			if err = r.pruneBootstrap(ctx, c, job.Name); err != nil {
				return ctrl.Result{}, err
			}
			condition(c, "SchemaReady", false, "Initializing", "waiting for guarded schema/admin Job")
			return finish("Initializing", "schema and admin Job is running", 5*time.Second)
		}
		if err != nil {
			return ctrl.Result{}, err
		}
		if !metav1.IsControlledBy(current, c) {
			return finish("OwnershipConflict", "bootstrap Job belongs to another owner", time.Minute)
		}
		for _, jc := range current.Status.Conditions {
			if jc.Type == batchv1.JobFailed && jc.Status == corev1.ConditionTrue {
				condition(c, "SchemaReady", false, "JobFailed", "inspect Job "+job.Name+"; correct cause and change spec.database.schema.retryToken")
				return finish("BootstrapFailed", "inspect Job "+job.Name, 30*time.Second)
			}
		}
		if current.Status.Succeeded == 0 {
			return finish("Initializing", "waiting for schema/admin Job "+job.Name, 5*time.Second)
		}
		c.Status.CompletedOperation = operation
		c.Status.AppliedVersion = v
		c.Status.UpgradePhase = ""
		c.Status.UpgradeTarget = ""
		condition(c, "SchemaReady", true, "Completed", "guarded schema/admin Job succeeded")
		condition(c, "DatabaseReady", true, "Verified", "database engine and schema checked by Job")
		// Persist success before creating writers; survives controller restart/Job deletion.
		return finish("Progressing", "schema complete; creating applications", time.Second)
	}
	condition(c, "SchemaReady", true, "Completed", "schema/admin operation recorded")
	if err = r.pruneBootstrap(ctx, c, ""); err != nil {
		return ctrl.Result{}, err
	}
	if e := c.Spec.Capture.External; e != nil {
		if e.Storage.VolumeClaim != nil {
			if err = r.apply(ctx, c, res.PVC(c), true); err != nil {
				return finish("StorageUnavailable", err.Error(), 30*time.Second)
			}
		}
		c.Status.ExternalStorageRetained = true
	}
	if c.Spec.Capture.Node != nil {
		c.Status.NodeStorageRetained = true
	}
	if c.Status.NodeStorageRetained && c.Spec.Capture.Node == nil || c.Status.ExternalStorageRetained && c.Spec.Capture.External == nil {
		return finish("RetainedCaptureRequiresSpec", "retain capture block with enabled:false to keep historical PCAP accessible", time.Minute)
	}
	for k := range cfg.Components(c) {
		if err = r.apply(ctx, c, res.Workload(c, k, digest), false); err != nil {
			return ctrl.Result{}, err
		}
	}
	for _, p := range []struct {
		k    string
		port int32
	}{{"viewer", 8005}, {"cont3xt", 3218}, {"wise", 8081}} {
		if _, ok := cfg.Components(c)[p.k]; ok {
			if err = r.apply(ctx, c, res.Service(c, p.k, p.port, corev1.ProtocolTCP), false); err != nil {
				return ctrl.Result{}, err
			}
		} else {
			if err = r.remove(ctx, c, &appsv1.Deployment{ObjectMeta: res.Meta(c, p.k)}); err != nil {
				return ctrl.Result{}, err
			}
			if err = r.remove(ctx, c, &corev1.Service{ObjectMeta: res.Meta(c, p.k)}); err != nil {
				return ctrl.Result{}, err
			}
		}
	}
	if c.Spec.Capture.External != nil {
		if err = r.apply(ctx, c, res.Service(c, "local-viewer", 8005, corev1.ProtocolTCP), false); err != nil {
			return ctrl.Result{}, err
		}
		udp := res.Service(c, "udp", 37008, corev1.ProtocolUDP)
		e := c.Spec.Capture.External.Exposure
		if e.Mode == "LoadBalancer" {
			udp.Spec.Type = corev1.ServiceTypeLoadBalancer
			udp.Spec.LoadBalancerClass = e.LoadBalancerClass
			udp.Spec.LoadBalancerSourceRanges = e.SourceRanges
			udp.Spec.ExternalTrafficPolicy = e.ExternalTrafficPolicy
		}
		udp.Annotations = e.Annotations
		if err = r.apply(ctx, c, udp, false); err != nil {
			return ctrl.Result{}, err
		}
	}
	if c.Spec.Retention.Mode != "External" {
		if err = r.apply(ctx, c, res.Retention(c), false); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		if err = r.remove(ctx, c, &batchv1.CronJob{ObjectMeta: res.Meta(c, "retention")}); err != nil {
			return ctrl.Result{}, err
		}
	}
	exposed, message := r.exposure(ctx, c)
	condition(c, "ExposureReady", exposed, "Observed", message)
	ready := exposed
	captureReady := true
	if !api.KubernetesEnrichment(c) {
		condition(c, "EnrichmentReady", true, "Disabled", "Kubernetes enrichment is disabled")
	}
	captureReason, captureMessage := "WorkloadObserved", "capture and retained-viewer workloads observed"
	for k := range cfg.Components(c) {
		ok := false
		if k == "node" {
			d := &appsv1.DaemonSet{}
			if err = r.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: cfg.Name(c, k)}, d); err != nil {
				return ctrl.Result{}, err
			}
			c.Status.NodeDesired = d.Status.DesiredNumberScheduled
			c.Status.NodeReady = d.Status.NumberReady
			ok = d.Status.ObservedGeneration >= d.Generation && d.Status.DesiredNumberScheduled > 0 && d.Status.NumberReady == d.Status.DesiredNumberScheduled && d.Status.UpdatedNumberScheduled == d.Status.DesiredNumberScheduled && d.Status.NumberUnavailable == 0
		} else {
			d := &appsv1.Deployment{}
			if err = r.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: cfg.Name(c, k)}, d); err != nil {
				return ctrl.Result{}, err
			}
			ok = d.Status.ObservedGeneration >= d.Generation && d.Status.AvailableReplicas > 0 && d.Status.UpdatedReplicas == 1
		}
		typ := strings.ToUpper(k[:1]) + k[1:] + "Ready"
		reason, message := "WorkloadObserved", "workload availability; does not assert packet arrival"
		if !ok && (k == "node" || k == "external") {
			if fr, fm := r.captureFault(ctx, c, k); fr != "" {
				reason, message = fr, fm
				captureReason, captureMessage = fr, fm
			}
		}
		condition(c, typ, ok, reason, message)
		if k == "wise" && api.KubernetesEnrichment(c) {
			condition(c, "EnrichmentReady", ok, "WatcherAndWISEObserved", "WISE availability includes synchronized watcher readiness")
		}
		ready = ready && ok
		if k == "node" || k == "external" {
			captureReady = captureReady && ok
		}
	}
	condition(c, "CaptureReady", captureReady, captureReason, captureMessage)
	condition(c, "Degraded", api.Enabled(c.Spec.Wise) && !api.KubernetesEnrichment(c) && len(c.Spec.Wise.Config) == 0, "EnrichmentConfiguration", "WISE requires configured sources to enrich sessions")
	c.Status.Endpoints = map[string]string{"viewer": cfg.ViewerURL(c)}
	if e := c.Spec.Capture.External; e != nil {
		addresses := []string{}
		if e.Exposure.Mode == "Gateway" {
			gateway := res.GatewayObject(c, "Gateway", "udp", nil)
			if e.Exposure.Gateway.ParentRef != nil {
				gateway.SetName(e.Exposure.Gateway.ParentRef.Name)
			}
			if r.Get(ctx, client.ObjectKeyFromObject(gateway), gateway) == nil {
				values, _, _ := unstructured.NestedSlice(gateway.Object, "status", "addresses")
				for _, value := range values {
					address, _ := value.(map[string]any)
					if host, ok := address["value"].(string); ok && host != "" {
						addresses = append(addresses, net.JoinHostPort(host, "37008"))
					}
				}
			}
		} else {
			svc := &corev1.Service{}
			if r.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: cfg.Name(c, "udp")}, svc) == nil {
				for _, a := range svc.Status.LoadBalancer.Ingress {
					host := a.IP
					if host == "" {
						host = a.Hostname
					}
					if host != "" {
						addresses = append(addresses, net.JoinHostPort(host, "37008"))
					}
				}
				if e.Exposure.Mode == "ClusterIP" {
					addresses = append(addresses, cfg.Name(c, "udp")+"."+c.Namespace+".svc:37008")
				}
			}
		}
		if len(addresses) > 0 {
			c.Status.Endpoints["tzsp"] = strings.Join(addresses, ",")
		}
	}

	if api.Enabled(c.Spec.Cont3xt.ComponentSpec) {
		c.Status.Endpoints["cont3xt"] = cfg.ServiceURL(c, "cont3xt", 3218)
	}
	if ready {
		return finish("Available", "requested workloads and exposure are ready", 30*time.Second)
	}
	if !captureReady && captureReason != "WorkloadObserved" {
		return finish("Progressing", captureMessage, 10*time.Second)
	}
	return finish("Progressing", "waiting for workload availability and exposure", 10*time.Second)
}

// A capture container that never reaches Running is indistinguishable from a slow
// rollout in the DaemonSet counts alone, and the usual causes -- an interface name
// that does not exist on the node, or a Pod that cannot be placed -- are only
// visible on the Pods. Report the first fault so the cause lands on the status
// instead of a permanent Progressing.
func (r *Reconciler) captureFault(ctx context.Context, c *api.ArkimeCluster, k string) (string, string) {
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(c.Namespace), client.MatchingLabels(res.Labels(c, k))); err != nil {
		return "", ""
	}
	sort.Slice(pods.Items, func(i, j int) bool { return pods.Items[i].Name < pods.Items[j].Name })
	for i := range pods.Items {
		p := &pods.Items[i]
		for _, cs := range p.Status.ContainerStatuses {
			if cs.Name != "capture" {
				continue
			}
			t := cs.LastTerminationState.Terminated
			if t == nil {
				t = cs.State.Terminated
			}
			if t == nil || t.ExitCode == 0 {
				continue
			}
			detail := truncate(t.Message)
			if strings.Contains(t.Message, "No such device") && c.Spec.Capture.Node != nil {
				return "InterfaceUnavailable", fmt.Sprintf("capture in %s: an interface in spec.capture.node.interfaces %v does not exist on node %s: %s", p.Name, c.Spec.Capture.Node.Interfaces, p.Spec.NodeName, detail)
			}
			return "CaptureFailed", fmt.Sprintf("capture in %s exited %d: %s", p.Name, t.ExitCode, detail)
		}
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		for _, pc := range p.Status.Conditions {
			if pc.Type == corev1.PodScheduled && pc.Status == corev1.ConditionFalse && pc.Reason == corev1.PodReasonUnschedulable {
				return "Unschedulable", fmt.Sprintf("%s cannot be scheduled: %s", p.Name, truncate(pc.Message))
			}
		}
	}
	return "", ""
}

// Arkime brackets a fatal error with rows of v/^; they carry nothing and would
// eat the message budget.
func truncate(s string) string {
	kept := []string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "vvv") || strings.HasPrefix(line, "^^^") {
			continue
		}
		kept = append(kept, line)
	}
	s = strings.Join(kept, "; ")
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return s
}
func (r *Reconciler) apply(ctx context.Context, c *api.ArkimeCluster, obj client.Object, retain bool) error {
	if !retain {
		if err := ctrl.SetControllerReference(c, obj, r.Scheme); err != nil {
			return err
		}
	}
	current := obj.DeepCopyObject().(client.Object)
	err := r.Get(ctx, client.ObjectKeyFromObject(obj), current)
	if apierrors.IsNotFound(err) {
		gvk, e := apiutil.GVKForObject(obj, r.Scheme)
		if e != nil {
			return e
		}
		obj.GetObjectKind().SetGroupVersionKind(gvk)
		return r.Patch(ctx, obj, client.Apply, client.FieldOwner("arkime-k8s-operator"), client.ForceOwnership)
	}
	if err != nil {
		return err
	}
	if !retain && !metav1.IsControlledBy(current, c) {
		return fmt.Errorf("%s already exists and is not owned by this ArkimeCluster", obj.GetName())
	}
	if retain && current.GetLabels()["arkime.arkime.com/cluster"] != cfg.ID(c) {
		return fmt.Errorf("retained resource %s has different ownership", obj.GetName())
	}
	switch x := obj.(type) {
	case *corev1.Service:
		old := current.(*corev1.Service)
		x.Spec.ClusterIP = old.Spec.ClusterIP
		x.Spec.ClusterIPs = old.Spec.ClusterIPs
		x.Spec.IPFamilies = old.Spec.IPFamilies
		x.Spec.IPFamilyPolicy = old.Spec.IPFamilyPolicy
		for i := range x.Spec.Ports {
			for _, p := range old.Spec.Ports {
				if p.Name == x.Spec.Ports[i].Name {
					x.Spec.Ports[i].NodePort = p.NodePort
				}
			}
		}
	case *corev1.PersistentVolumeClaim:
		old := current.(*corev1.PersistentVolumeClaim)
		if x.Spec.StorageClassName != nil && !reflect.DeepEqual(x.Spec.StorageClassName, old.Spec.StorageClassName) {
			return fmt.Errorf("PVC storage class is immutable")
		}
		want := x.Spec.Resources.Requests[corev1.ResourceStorage]
		have := old.Spec.Resources.Requests[corev1.ResourceStorage]
		if want.Cmp(have) < 0 {
			return fmt.Errorf("PVC shrinking is forbidden")
		}
		x.Spec = old.Spec
		x.Spec.Resources.Requests[corev1.ResourceStorage] = want
	}
	gvk, err := apiutil.GVKForObject(obj, r.Scheme)
	if err != nil {
		return err
	}
	obj.GetObjectKind().SetGroupVersionKind(gvk)
	return r.Patch(ctx, obj, client.Apply, client.FieldOwner("arkime-k8s-operator"), client.ForceOwnership)
}

// Bootstrap Jobs are content-addressed, so every retryToken mints a new name and
// nothing reclaims the old one. Keep is retained so a failure stays inspectable;
// "" prunes all, which is only safe once CompletedOperation is persisted.
func (r *Reconciler) pruneBootstrap(ctx context.Context, c *api.ArkimeCluster, keep string) error {
	jobs := &batchv1.JobList{}
	if err := r.List(ctx, jobs, client.InNamespace(c.Namespace), client.MatchingLabels{"app.kubernetes.io/managed-by": "arkime-k8s-operator", "arkime.arkime.com/cluster": cfg.ID(c)}); err != nil {
		return err
	}
	prefix := cfg.Name(c, "schema-")
	for i := range jobs.Items {
		j := &jobs.Items[i]
		if j.Name == keep || !strings.HasPrefix(j.Name, prefix) || !metav1.IsControlledBy(j, c) {
			continue
		}
		if err := r.remove(ctx, c, j, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
			return err
		}
	}
	return nil
}
func (r *Reconciler) remove(ctx context.Context, c *api.ArkimeCluster, obj client.Object, opts ...client.DeleteOption) error {
	if err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(obj, c) {
		return fmt.Errorf("refusing to delete unowned %s", obj.GetName())
	}
	return client.IgnoreNotFound(r.Delete(ctx, obj, opts...))
}
func randomSecret() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}
func (r *Reconciler) auth(ctx context.Context, c *api.ArkimeCluster) error {
	c.Status.SharedSecret = c.Spec.Auth.SharedSecret
	if c.Status.SharedSecret == "" {
		c.Status.SharedSecret = cfg.Name(c, "auth")
	}
	c.Status.AdminSecret = c.Spec.Auth.AdminSecret
	if c.Status.AdminSecret == "" {
		c.Status.AdminSecret = cfg.Name(c, "admin")
	}
	for _, item := range []struct {
		name     string
		existing bool
		keys     []string
	}{{c.Status.SharedSecret, c.Spec.Auth.SharedSecret != "", []string{"passwordSecret", "serverSecret"}}, {c.Status.AdminSecret, c.Spec.Auth.AdminSecret != "", []string{"username", "password"}}} {
		s := &corev1.Secret{}
		err := r.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: item.name}, s)
		if apierrors.IsNotFound(err) && !item.existing {
			if c.Status.AppliedVersion != "" {
				return fmt.Errorf("recovery Secret %s is missing; restore it from backup", item.name)
			}
			s = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: item.name, Namespace: c.Namespace, Labels: res.Labels(c, "recovery")}, Data: map[string][]byte{}}
			for _, k := range item.keys {
				v, e := randomSecret()
				if e != nil {
					return e
				}
				if k == "username" {
					v = "admin"
				}
				s.Data[k] = []byte(v)
			}
			if err = r.Create(ctx, s); err != nil {
				return err
			}
		} else if err != nil {
			return fmt.Errorf("Secret %s is unavailable", item.name)
		}
		if !item.existing && s.Labels["arkime.arkime.com/cluster"] != cfg.ID(c) {
			return fmt.Errorf("generated Secret name %s is occupied by an unowned Secret", item.name)
		}
		for _, k := range item.keys {
			if len(s.Data[k]) == 0 {
				return fmt.Errorf("Secret %s needs key %s", item.name, k)
			}
		}
	}
	return nil
}
func (r *Reconciler) dependencies(ctx context.Context, c *api.ArkimeCluster) (string, error) {
	refs := []*corev1.SecretKeySelector{}
	if a := c.Spec.Database.BootstrapAuth; a != nil {
		refs = append(refs, a.BasicAuthSecretRef, a.APIKeySecretRef)
	}
	if name := c.Spec.Database.TLS.ClientCertificateSecret; name != "" {
		for _, key := range []string{"tls.crt", "tls.key"} {
			refs = append(refs, &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key})
		}
	}
	if o := c.Spec.Auth.OIDC; o != nil {
		refs = append(refs, &o.ClientSecretRef)
	}
	for _, b := range cfg.Backends(c) {
		refs = append(refs, b.Auth.BasicAuthSecretRef, b.Auth.APIKeySecretRef, b.TLS.CASecretRef)
	}
	for _, comp := range cfg.Components(c) {
		for _, s := range comp.ConfigSecretRefs {
			ref := s.SecretKeyRef
			refs = append(refs, &ref)
		}
	}
	for _, ref := range c.Spec.Image.PullSecrets {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: ref.Name}, secret); err != nil {
			return "", fmt.Errorf("image pull Secret %s is unavailable", ref.Name)
		}
	}
	if c.Spec.Web.TLSSecret != "" {
		for _, key := range []string{"tls.crt", "tls.key"} {
			refs = append(refs, &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: c.Spec.Web.TLSSecret}, Key: key})
		}
	}
	configParts := []string{}
	for _, component := range cfg.Components(c) {
		for _, ref := range component.ConfigMapRefs {
			cm := &corev1.ConfigMap{}
			if err := r.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: ref.Name}, cm); err != nil {
				return "", fmt.Errorf("ConfigMap %s is unavailable", ref.Name)
			}
			data, _ := json.Marshal(struct {
				Data   map[string]string
				Binary map[string][]byte
			}{cm.Data, cm.BinaryData})
			configParts = append(configParts, ref.Name+string(data))
		}
	}

	parts := append([]string{}, configParts...)
	for _, name := range []string{c.Status.SharedSecret, c.Status.AdminSecret} {
		s := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: name}, s); err != nil {
			return "", err
		}
		data, _ := json.Marshal(s.Data)
		parts = append(parts, name+string(data))
	}
	for _, ref := range refs {
		if ref == nil {
			continue
		}
		s := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: ref.Name}, s); err != nil {
			return "", fmt.Errorf("Secret %s is unavailable", ref.Name)
		}
		if len(s.Data[ref.Key]) == 0 {
			return "", fmt.Errorf("Secret %s needs key %s", ref.Name, ref.Key)
		}
		parts = append(parts, ref.Name+ref.Key+string(s.Data[ref.Key]))
	}
	if e := c.Spec.Capture.External; e != nil && e.Storage.ExistingClaim != "" {
		p := &corev1.PersistentVolumeClaim{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: e.Storage.ExistingClaim}, p); err != nil {
			return "", fmt.Errorf("existing PVC %s is unavailable", e.Storage.ExistingClaim)
		}
	}
	spec, _ := json.Marshal(c.Spec)
	parts = append(parts, string(spec))
	sort.Strings(parts)
	return cfg.Hash(strings.Join(parts, "\x00")), nil
}
func sameBackend(a, b api.Backend) bool {
	for _, x := range a.Endpoints {
		for _, y := range b.Endpoints {
			if strings.TrimSuffix(x, "/") == strings.TrimSuffix(y, "/") {
				return true
			}
		}
	}
	return false
}
func (r *Reconciler) conflicts(ctx context.Context, c *api.ArkimeCluster) error {
	list := &api.ArkimeClusterList{}
	if err := r.List(ctx, list); err != nil {
		return err
	}
	for i := range list.Items {
		o := &list.Items[i]
		if o.UID == c.UID {
			continue
		}
		for _, a := range schemas(c) {
			for _, b := range schemas(o) {
				if sameBackend(a.backend, b.backend) && strings.TrimSuffix(a.prefix, "_") == strings.TrimSuffix(b.prefix, "_") {
					return fmt.Errorf("database/users prefix is also claimed by %s/%s", o.Namespace, o.Name)
				}
			}
		}
		if c.Spec.Capture.Node != nil && o.Spec.Capture.Node != nil && (cfg.Port(c) == cfg.Port(o) || cfg.Port(c)+1 == cfg.Port(o) || cfg.Port(c) == cfg.Port(o)+1) {
			overlap := true
			for k, v := range c.Spec.Capture.Node.NodeSelector {
				if w, ok := o.Spec.Capture.Node.NodeSelector[k]; ok && v != w {
					overlap = false
				}
			}
			if overlap {
				return fmt.Errorf("node viewer port may conflict with %s/%s", o.Namespace, o.Name)
			}
		}
		if api.Enabled(c.Spec.Cont3xt.ComponentSpec) && api.Enabled(o.Spec.Cont3xt.ComponentSpec) {
			a := c.Spec.Database.Backend
			b := o.Spec.Database.Backend
			if c.Spec.Cont3xt.Database != nil {
				a = *c.Spec.Cont3xt.Database
			}
			if o.Spec.Cont3xt.Database != nil {
				b = *o.Spec.Cont3xt.Database
			}
			if sameBackend(a, b) && !(c.Spec.Cont3xt.AllowSharedDatabase && o.Spec.Cont3xt.AllowSharedDatabase) {
				return fmt.Errorf("Cont3xt uses fixed indices shared with %s/%s; supply distinct backend or explicit sharing on both clusters", o.Namespace, o.Name)
			}
		}
	}
	return nil
}
func (r *Reconciler) exposure(ctx context.Context, c *api.ArkimeCluster) (bool, string) {
	keep := map[string]bool{}
	if c.Spec.Web.Mode == "Gateway" {
		keep["HTTPRoute/web"] = true
	}
	if e := c.Spec.Capture.External; e != nil && e.Exposure.Mode == "Gateway" {
		keep["UDPRoute/udp"] = true
		if e.Exposure.Gateway.ParentRef == nil {
			keep["Gateway/udp"] = true
		}
	}
	for _, entry := range []struct{ kind, key string }{{"HTTPRoute", "web"}, {"UDPRoute", "udp"}, {"Gateway", "udp"}} {
		if keep[entry.kind+"/"+entry.key] {
			continue
		}
		obj := res.GatewayObject(c, entry.kind, entry.key, map[string]any{})
		if err := r.remove(ctx, c, obj); err != nil && !meta.IsNoMatchError(err) {
			return false, err.Error()
		}
	}

	objects := []client.Object{}
	if c.Spec.Web.Mode == "Ingress" {
		objects = append(objects, res.Ingress(c))
	} else {
		if err := r.remove(ctx, c, &networkingv1.Ingress{ObjectMeta: res.Meta(c, "web")}); err != nil {
			return false, err.Error()
		}
	}
	if c.Spec.Web.Mode == "Gateway" {
		objects = append(objects, res.HTTPRoute(c))
	}
	if e := c.Spec.Capture.External; e != nil && e.Exposure.Mode == "Gateway" {
		objects = append(objects, res.UDPRoutes(c)...)
	}
	for _, o := range objects {
		if err := r.apply(ctx, c, o, false); err != nil {
			return false, "exposure unavailable (Gateway API v1 and a capable controller required): " + err.Error()
		}
		if u, ok := o.(*unstructured.Unstructured); ok {
			if err := r.Get(ctx, client.ObjectKeyFromObject(u), u); err != nil {
				return false, err.Error()
			}
			if u.GetKind() == "Gateway" {
				conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
				if !accepted(conditions, u.GetGeneration(), "Programmed") {
					return false, "waiting for Gateway Programmed"
				}
			} else {
				parents, _, _ := unstructured.NestedSlice(u.Object, "status", "parents")
				wanted, _, _ := unstructured.NestedSlice(u.Object, "spec", "parentRefs")
				ok := false
				for _, parent := range parents {
					m, _ := parent.(map[string]any)
					ref, _, _ := unstructured.NestedMap(m, "parentRef")
					if !matchesParent(ref, wanted, c.Namespace) {
						continue
					}
					conditions, _, _ := unstructured.NestedSlice(m, "conditions")
					if accepted(conditions, u.GetGeneration(), "Accepted") && accepted(conditions, u.GetGeneration(), "ResolvedRefs") {
						gateway := res.GatewayObject(c, "Gateway", "udp", nil)
						name, _ := ref["name"].(string)
						gateway.SetName(name)
						if err := r.Get(ctx, client.ObjectKeyFromObject(gateway), gateway); err != nil {
							return false, "route parent unavailable: " + err.Error()
						}
						gc, _, _ := unstructured.NestedSlice(gateway.Object, "status", "conditions")
						if !accepted(gc, gateway.GetGeneration(), "Programmed") {
							return false, "waiting for route parent Gateway Programmed"
						}
						ok = true
					}
				}
				if !ok {
					return false, "waiting for Route Accepted and ResolvedRefs"
				}
			}
		}
	}
	if e := c.Spec.Capture.External; e != nil && e.Exposure.Mode == "LoadBalancer" {
		s := &corev1.Service{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: cfg.Name(c, "udp")}, s); err != nil {
			return false, err.Error()
		}
		if len(s.Status.LoadBalancer.Ingress) == 0 {
			return false, "waiting for UDP LoadBalancer address"
		}
	}
	return true, "requested exposure resources observed"
}
func accepted(conditions []any, generation int64, typ string) bool {
	for _, v := range conditions {
		m, _ := v.(map[string]any)
		if m["type"] == typ && m["status"] == "True" && m["observedGeneration"] == generation {
			return true
		}
	}
	return false
}

type schemaOwner struct {
	backend api.Backend
	prefix  string
}

func schemas(c *api.ArkimeCluster) []schemaOwner {
	out := []schemaOwner{{c.Spec.Database.Backend, cfg.Prefix(c)}}
	if u := c.Spec.Database.Users; u != nil {
		out = append(out, schemaOwner{u.Backend, u.IndexPrefix})
	}
	return out
}

// Match only the configured attachment; an obsolete parent status is not readiness.
func matchesParent(actual map[string]any, wanted []any, namespace string) bool {
	normalize := func(m map[string]any, key, fallback string) any {
		if v, ok := m[key]; ok {
			return v
		}
		return fallback
	}
	for _, item := range wanted {
		expected, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if actual["name"] != expected["name"] {
			continue
		}
		if normalize(actual, "namespace", namespace) != normalize(expected, "namespace", namespace) || normalize(actual, "group", "gateway.networking.k8s.io") != normalize(expected, "group", "gateway.networking.k8s.io") || normalize(actual, "kind", "Gateway") != normalize(expected, "kind", "Gateway") {
			continue
		}
		if actual["sectionName"] != expected["sectionName"] || actual["port"] != expected["port"] {
			continue
		}
		return true
	}
	return false
}
