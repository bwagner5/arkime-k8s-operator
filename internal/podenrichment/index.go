// Package podenrichment implements live, best-effort pod IP attribution.
package podenrichment

import (
	"fmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"net/netip"
	"sort"
	"strings"
)

var Fields = []string{"cluster", "namespace", "pod.name", "pod.uid", "node.name"}

func ValidateValue(s string) error {
	if strings.ContainsAny(s, ";=\r\n\x00\\") {
		return fmt.Errorf("invalid tagger value %q", s)
	}
	return nil
}
func Definition(field string) string {
	return "kind:termfield;db:" + field + ";friendly:" + field + ";help:Live Kubernetes pod metadata"
}
func Header() string {
	var b strings.Builder
	for _, f := range Fields {
		fmt.Fprintf(&b, "#field:k8s.%s;%s\n", f, Definition("k8s."+f))
	}
	return b.String()
}

// Transform deliberately discards annotations, labels, containers and other large fields.
func Transform(obj interface{}) (interface{}, error) {
	p, ok := obj.(*corev1.Pod)
	if !ok {
		return obj, nil
	}
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace, UID: p.UID, ResourceVersion: p.ResourceVersion, DeletionTimestamp: p.DeletionTimestamp}, Spec: corev1.PodSpec{NodeName: p.Spec.NodeName, HostNetwork: p.Spec.HostNetwork}, Status: corev1.PodStatus{Phase: p.Status.Phase, PodIP: p.Status.PodIP, PodIPs: p.Status.PodIPs}}, nil
}

// Index retains both directions so updates and delayed UID deletes cannot
// remove another owner's claim. Callers serialize access.
type Index struct {
	pods      map[types.UID]*corev1.Pod
	addresses map[types.UID][]netip.Addr
	owners    map[netip.Addr]map[types.UID]*corev1.Pod
}

func NewIndex() *Index {
	return &Index{pods: map[types.UID]*corev1.Pod{}, addresses: map[types.UID][]netip.Addr{}, owners: map[netip.Addr]map[types.UID]*corev1.Pod{}}
}
func (i *Index) remove(uid types.UID) {
	for _, a := range i.addresses[uid] {
		delete(i.owners[a], uid)
		if len(i.owners[a]) == 0 {
			delete(i.owners, a)
		}
	}
	delete(i.addresses, uid)
	delete(i.pods, uid)
}
func (i *Index) Upsert(p *corev1.Pod) {
	i.remove(p.UID)
	v, _ := Transform(p)
	p = v.(*corev1.Pod)
	i.pods[p.UID] = p
	if p.Spec.HostNetwork || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
		return
	}
	ips := p.Status.PodIPs
	if len(ips) == 0 {
		ips = []corev1.PodIP{{IP: p.Status.PodIP}}
	}
	seen := map[netip.Addr]bool{}
	for _, ip := range ips {
		a, err := netip.ParseAddr(ip.IP)
		if err != nil || a.Zone() != "" {
			continue
		}
		a = a.Unmap()
		if seen[a] {
			continue
		}
		seen[a] = true
		if i.owners[a] == nil {
			i.owners[a] = map[types.UID]*corev1.Pod{}
		}
		i.owners[a][p.UID] = p
		i.addresses[p.UID] = append(i.addresses[p.UID], a)
	}
}
func (i *Index) Delete(obj interface{}) {
	if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = t.Obj
	}
	if p, ok := obj.(*corev1.Pod); ok {
		i.remove(p.UID)
	}
}
func (i *Index) Render(cluster string) ([]byte, int, int, error) {
	if err := ValidateValue(cluster); err != nil {
		return nil, 0, 0, err
	}
	owners := i.owners
	addresses := []netip.Addr{}
	ambiguous := 0
	for a, ps := range owners {
		if len(ps) != 1 {
			ambiguous++
			continue
		}
		addresses = append(addresses, a)
	}
	sort.Slice(addresses, func(i, j int) bool { return addresses[i].Compare(addresses[j]) < 0 })
	var b strings.Builder
	b.WriteString(Header())
	for _, a := range addresses {
		var p *corev1.Pod
		for _, v := range owners[a] {
			p = v
		}
		values := []string{cluster, p.Namespace, p.Name, string(p.UID), p.Spec.NodeName}
		b.WriteString(a.String())
		for n, v := range values {
			if err := ValidateValue(v); err != nil {
				return nil, 0, ambiguous, err
			}
			fmt.Fprintf(&b, ";k8s.%s=%s", Fields[n], v)
		}
		b.WriteByte('\n')
	}
	return []byte(b.String()), len(addresses), ambiguous, nil
}
