package podenrichment

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	kt "k8s.io/client-go/testing"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func eventually(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
func TestFreshnessRecoveryAndChurn(t *testing.T) {
	client := fake.NewClientset(pod("a", "10.0.0.1"))
	var outage atomic.Bool
	client.PrependReactor("list", "pods", func(a kt.Action) (bool, runtime.Object, error) {
		if outage.Load() {
			return true, nil, fmt.Errorf("API unavailable")
		}
		return false, nil, nil
	})
	o := Options{Output: filepath.Join(t.TempDir(), "pods.tagger"), ClusterName: "home", Debounce: 10 * time.Millisecond, MaxDelay: 40 * time.Millisecond, Refresh: 200 * time.Millisecond, MaxStale: 800 * time.Millisecond}
	h := &Health{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, client, o, h) }()
	read := func() string { b, _ := os.ReadFile(o.Output); return string(b) }
	eventually(t, func() bool { return h.Ready.Load() && strings.Contains(read(), "pod-a") })
	writes := h.Writes.Load()
	time.Sleep(500 * time.Millisecond)
	if !h.Ready.Load() || h.Writes.Load() != writes {
		t.Fatal("quiet inventory lost freshness or rewrote snapshot")
	}
	p := pod("a", "10.0.0.2")
	if _, e := client.CoreV1().Pods("shop").Update(ctx, p, metav1.UpdateOptions{}); e != nil {
		t.Fatal(e)
	}
	eventually(t, func() bool { return strings.Contains(read(), "10.0.0.2") && !strings.Contains(read(), "10.0.0.1") })
	outage.Store(true)
	eventually(t, func() bool { return !h.Ready.Load() && read() == Header() })
	diagnostic, _ := os.ReadFile(o.Output + ".last-good")
	if !strings.Contains(string(diagnostic), "pod-a") {
		t.Fatal("missing diagnostic snapshot")
	}
	if e := client.CoreV1().Pods("shop").Delete(ctx, p.Name, metav1.DeleteOptions{}); e != nil {
		t.Fatal(e)
	}
	if _, e := client.CoreV1().Pods("shop").Create(ctx, pod("b", "10.0.0.2"), metav1.CreateOptions{}); e != nil {
		t.Fatal(e)
	}
	outage.Store(false)
	eventually(t, func() bool {
		return h.Ready.Load() && strings.Contains(read(), "pod-b") && !strings.Contains(read(), "pod-a")
	})
	cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
func TestOnceAndInvalidOptions(t *testing.T) {
	o := Options{Output: filepath.Join(t.TempDir(), "pods.tagger"), Debounce: time.Millisecond, MaxDelay: time.Second, Refresh: time.Second, MaxStale: 3 * time.Second, Once: true}
	h := &Health{}
	if e := Serve(context.Background(), fake.NewClientset(), o, h); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(o.Output)
	if string(b) != Header() {
		t.Fatal(string(b))
	}
	o.Debounce = 0
	if e := Serve(context.Background(), fake.NewClientset(), o, h); e == nil {
		t.Fatal("invalid options accepted")
	}
}
