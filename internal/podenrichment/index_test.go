package podenrichment

import (
	"bytes"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pod(uid, ip string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-" + uid, Namespace: "shop", UID: types.UID(uid)}, Spec: corev1.PodSpec{NodeName: "worker"}, Status: corev1.PodStatus{PodIP: ip}}
}
func TestOwnership(t *testing.T) {
	for _, tc := range []struct {
		name             string
		change           func(*Index)
		count, ambiguous int
		contains         string
	}{
		{"empty", func(i *Index) {}, 0, 0, ""},
		{"unready", func(i *Index) { i.Upsert(pod("a", "10.0.0.1")) }, 1, 0, "pod-a"},
		{"dual-stack", func(i *Index) {
			p := pod("a", "")
			p.Status.PodIPs = []corev1.PodIP{{IP: "::ffff:10.0.0.1"}, {IP: "2001:0db8::1"}, {IP: "10.0.0.1"}}
			i.Upsert(p)
		}, 2, 0, "2001:db8::1"},
		{"host-network", func(i *Index) { p := pod("a", "10.0.0.1"); p.Spec.HostNetwork = true; i.Upsert(p) }, 0, 0, ""},
		{"terminal", func(i *Index) { p := pod("a", "10.0.0.1"); p.Status.Phase = corev1.PodSucceeded; i.Upsert(p) }, 0, 0, ""},
		{"failed", func(i *Index) { p := pod("a", "10.0.0.1"); p.Status.Phase = corev1.PodFailed; i.Upsert(p) }, 0, 0, ""},
		{"terminating", func(i *Index) {
			p := pod("a", "10.0.0.1")
			now := metav1.Now()
			p.DeletionTimestamp = &now
			i.Upsert(p)
		}, 1, 0, "pod-a"},
		{"ambiguous", func(i *Index) { i.Upsert(pod("a", "10.0.0.1")); i.Upsert(pod("b", "10.0.0.1")) }, 0, 1, ""},
		{"reuse-tombstone", func(i *Index) {
			i.Upsert(pod("a", "10.0.0.1"))
			i.Upsert(pod("b", "10.0.0.1"))
			i.Delete(cache.DeletedFinalStateUnknown{Obj: pod("a", "10.0.0.1")})
			i.Delete(pod("a", "10.0.0.1"))
		}, 1, 0, "pod-b"},
		{"assignment-update", func(i *Index) { i.Upsert(pod("a", "10.0.0.1")); i.Upsert(pod("a", "10.0.0.2")) }, 1, 0, "10.0.0.2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := NewIndex()
			tc.change(i)
			b, n, a, e := i.Render("home")
			if e != nil || n != tc.count || a != tc.ambiguous || !strings.Contains(string(b), tc.contains) {
				t.Fatalf("%s %d %d %v", b, n, a, e)
			}
			if n == 0 && string(b) != Header() {
				t.Fatal("empty/ambiguous inventory has rows")
			}
			again, _, _, _ := i.Render("home")
			if !bytes.Equal(b, again) {
				t.Fatal("unstable output")
			}
		})
	}
}
func TestValuesAndAtomicWrite(t *testing.T) {
	for _, v := range []string{"x;y", "x\ny", "x=y", "x\x00", "x\\y"} {
		if ValidateValue(v) == nil {
			t.Fatalf("accepted %q", v)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "pods.tagger")
	if e := AtomicWrite(path, []byte(Header())); e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 5; n++ {
		if e := AtomicWrite(path, []byte("new")); e != nil {
			t.Fatal(e)
		}
	}
	if e := AtomicWrite(filepath.Join(path, "bad"), []byte("bad")); e == nil {
		t.Fatal("expected error")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "new" {
		t.Fatal("lost previous snapshot")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("temporary file leaked")
	}
}
func TestTransform(t *testing.T) {
	p := pod("a", "1.2.3.4")
	p.Annotations = map[string]string{"secret": "unused"}
	p.Spec.Containers = []corev1.Container{{Name: "large"}}
	v, _ := Transform(p)
	got := v.(*corev1.Pod)
	if len(got.Annotations) != 0 || len(got.Spec.Containers) != 0 || got.UID != p.UID {
		t.Fatal(got)
	}
}

func BenchmarkRender10000(b *testing.B) {
	index := NewIndex()
	for n := 0; n < 10000; n++ {
		index.Upsert(pod(fmt.Sprintf("pod-%d", n), fmt.Sprintf("10.%d.%d.%d", n/65536, (n/256)%256, n%256)))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if _, _, _, err := index.Render("benchmark"); err != nil {
			b.Fatal(err)
		}
	}
}
