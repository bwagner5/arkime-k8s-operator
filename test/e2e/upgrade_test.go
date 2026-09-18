//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpgradeAndRetainedResources(t *testing.T) {
	if os.Getenv("E2E_CONTEXT") == "" {
		t.Fatal("run with make test-e2e")
	}
	kube(t, "apply", "-f", "upgrade.yaml")
	kube(t, "wait", "arkimecluster/upgrade", "--for=condition=Ready", "--timeout=300s")
	read := func() map[string]any {
		t.Helper()
		v := map[string]any{}
		if err := json.Unmarshal(kube(t, "get", "arkimecluster", "upgrade", "-o", "json"), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := read()
	status := before["status"].(map[string]any)
	kube(t, "patch", "arkimecluster", "upgrade", "--type=merge", "-p", `{"spec":{"version":"6.7.0"}}`)
	kube(t, "wait", "arkimecluster/upgrade", "--for=condition=UpgradeRequired", "--timeout=60s")
	current := read()
	if current["status"].(map[string]any)["appliedVersion"] != "6.6.0" {
		t.Fatal("unapproved version changed")
	}
	kube(t, "patch", "arkimecluster", "upgrade", "--type=merge", "-p", `{"spec":{"database":{"schema":{"approvedUpgradeVersion":"6.7.0"}}}}`)
	deadline := time.Now().Add(6 * time.Minute)
	for {
		current = read()
		s := current["status"].(map[string]any)
		if s["appliedVersion"] == "6.7.0" && s["completedOperation"] != status["completedOperation"] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("approved upgrade did not complete")
		}
		time.Sleep(3 * time.Second)
	}
	kube(t, "wait", "arkimecluster/upgrade", "--for=condition=Ready", "--timeout=180s")
	if current["status"].(map[string]any)["sharedSecret"] != status["sharedSecret"] {
		t.Fatal("upgrade replaced recovery secret")
	}
	// Exercise an operator restart after a completed operation; the Job must not recur.
	job := current["status"].(map[string]any)["schemaJob"].(string)
	kube(t, "delete", "job", job)
	command(t, "kubectl", "--context", os.Getenv("E2E_CONTEXT"), "-n", "arkime-system", "rollout", "restart", "deployment/arkime-operator")
	command(t, "kubectl", "--context", os.Getenv("E2E_CONTEXT"), "-n", "arkime-system", "rollout", "status", "deployment/arkime-operator", "--timeout=120s")
	time.Sleep(5 * time.Second)
	jobs := string(kube(t, "get", "jobs", "-o", "name"))
	if strings.Contains(jobs, job) {
		t.Fatal("completed migration Job recreated after restart")
	}
	shared := status["sharedSecret"].(string)
	admin := status["adminSecret"].(string)
	kube(t, "delete", "arkimecluster", "upgrade", "--wait=true")
	kube(t, "get", "secret", shared, admin, "-o", "name")
	// The retained claim shares the generated name stem with the auth Secret.
	claim := strings.TrimSuffix(shared, "-auth") + "-pcap"
	kube(t, "get", "pvc", claim, "-o", "name")
	// Retained CRD chart uninstall/reinstall must preserve the other cluster's UID.
	demo := string(kube(t, "get", "arkimecluster", "demo", "-o", "jsonpath={.metadata.uid}"))
	command(t, "helm", "uninstall", "arkime-crds", "--kube-context", os.Getenv("E2E_CONTEXT"))
	if got := string(kube(t, "get", "arkimecluster", "demo", "-o", "jsonpath={.metadata.uid}")); got != demo {
		t.Fatal("CRD chart uninstall lost CR data")
	}
	command(t, "helm", "upgrade", "--install", "arkime-crds", filepath.Join("..", "..", "charts", "arkime-k8s-operator-crds"), "--kube-context", os.Getenv("E2E_CONTEXT"))
}
