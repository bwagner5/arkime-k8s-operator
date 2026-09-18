package resources

import (
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestCaptureStorageLifecycle(t *testing.T) {
	c := &api.ArkimeCluster{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"}, Spec: api.ArkimeClusterSpec{Capture: api.CaptureSpec{External: &api.ExternalCapture{Storage: api.ClaimStorage{ExistingClaim: "pcap"}}}}}
	d := Workload(c, "external", "hash").(*appsv1.Deployment)
	if d.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType || *d.Spec.Replicas != 1 || len(d.Spec.Template.Spec.Containers) != 2 {
		t.Fatal("external receiver must have one writer and local viewer")
	}
	c.Spec.Capture.External.Enabled = Ptr(false)
	d = Workload(c, "external", "hash").(*appsv1.Deployment)
	if len(d.Spec.Template.Spec.Containers) != 1 || d.Spec.Template.Spec.Containers[0].Name != "local-viewer" {
		t.Fatal("disabling capture lost historical access")
	}
	if *d.Spec.Template.Spec.AutomountServiceAccountToken {
		t.Fatal("application token mounted")
	}
}
func TestNodePrivileges(t *testing.T) {
	c := &api.ArkimeCluster{Spec: api.ArkimeClusterSpec{Capture: api.CaptureSpec{Node: &api.NodeCapture{Storage: api.HostStorage{HostPath: "/pcap"}}}}}
	d := Workload(c, "node", "hash").(*appsv1.DaemonSet)
	if !d.Spec.Template.Spec.HostNetwork || d.Spec.Template.Spec.HostPID {
		t.Fatal("incorrect host namespace")
	}
	for _, ct := range d.Spec.Template.Spec.Containers {
		if ct.SecurityContext.Privileged != nil && *ct.SecurityContext.Privileged {
			t.Fatal("privileged")
		}
		if ct.Name == "local-viewer" && len(ct.SecurityContext.Capabilities.Add) != 0 {
			t.Fatal("viewer has capture capabilities")
		}
	}
}

func TestNodePortOverride(t *testing.T) {
	c := &api.ArkimeCluster{Spec: api.ArkimeClusterSpec{Capture: api.CaptureSpec{Node: &api.NodeCapture{ViewerPort: 8105, Storage: api.HostStorage{HostPath: "/pcap"}}}}}
	d := Workload(c, "node", "hash").(*appsv1.DaemonSet)
	if d.Spec.Template.Spec.Containers[1].ReadinessProbe.TCPSocket.Port.IntVal != 8106 {
		t.Fatal("health listener collides across clusters")
	}
}

func TestExternalCaptureNeedsNoCapabilities(t *testing.T) {
	c := &api.ArkimeCluster{Spec: api.ArkimeClusterSpec{Capture: api.CaptureSpec{External: &api.ExternalCapture{Storage: api.ClaimStorage{ExistingClaim: "pcap"}}}}}
	d := Workload(c, "external", "hash").(*appsv1.Deployment)
	capture := d.Spec.Template.Spec.Containers[1]
	if !*capture.SecurityContext.RunAsNonRoot || len(capture.SecurityContext.Capabilities.Add) > 0 {
		t.Fatal("TZSP needs no root or network capabilities")
	}
	if capture.Lifecycle != nil {
		t.Fatal("capture must receive SIGTERM directly")
	}
}
