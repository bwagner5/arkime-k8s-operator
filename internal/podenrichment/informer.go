package podenrichment

import (
	"bytes"
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"os"
	"sync"
	"time"
)

type Options struct {
	Output, ClusterName, Namespace, LabelSelector string
	Debounce, MaxDelay, MaxStale, Refresh         time.Duration
	Once                                          bool
}

func Run(ctx context.Context, client kubernetes.Interface, o Options, h *Health) error {
	if o.Debounce <= 0 || o.MaxDelay < o.Debounce || o.Refresh <= 0 || o.MaxStale < 2*o.Refresh {
		return fmt.Errorf("require 0 < debounce <= max-delay and max-stale >= 2 * refresh")
	}
	if err := ValidateValue(o.ClusterName); err != nil {
		return err
	}
	pods := client.CoreV1().Pods(o.Namespace)
	lw := &cache.ListWatch{ListFunc: func(lo metav1.ListOptions) (runtime.Object, error) {
		lo.LabelSelector = o.LabelSelector
		v, e := pods.List(ctx, lo)
		if e != nil {
			h.APIFailures.Add(1)
		}
		return v, e
	}, WatchFunc: func(lo metav1.ListOptions) (watch.Interface, error) {
		lo.LabelSelector = o.LabelSelector
		v, e := pods.Watch(ctx, lo)
		if e != nil {
			h.APIFailures.Add(1)
		}
		return v, e
	}}
	informer := cache.NewSharedIndexInformer(cache.ToListWatcherWithWatchListSemantics(lw, client), &corev1.Pod{}, 0, cache.Indexers{})
	if err := informer.SetWatchErrorHandlerWithContext(func(ctx context.Context, r *cache.Reflector, err error) {
		h.APIFailures.Add(1)
		cache.DefaultWatchErrorHandler(ctx, r, err)
	}); err != nil {
		return err
	}
	if err := informer.SetTransform(Transform); err != nil {
		return err
	}
	events := make(chan struct{}, 1)
	signal := func() {
		select {
		case events <- struct{}{}:
		default:
		}
	}
	index := NewIndex()
	var indexMu sync.Mutex
	handler, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) { indexMu.Lock(); index.Upsert(obj.(*corev1.Pod)); indexMu.Unlock(); signal() },
		UpdateFunc: func(old, obj interface{}) {
			indexMu.Lock()
			index.Delete(old)
			index.Upsert(obj.(*corev1.Pod))
			indexMu.Unlock()
			signal()
		},
		DeleteFunc: func(obj interface{}) { indexMu.Lock(); index.Delete(obj); indexMu.Unlock(); signal() },
	})
	if err != nil {
		return err
	}
	go informer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced, handler.HasSynced) {
		return ctx.Err()
	}
	h.Synced.Store(true)
	previous, _ := os.ReadFile(o.Output)
	lastGood := time.Now()
	publish := func(data []byte, count, amb int, healthy bool) error {
		if !bytes.Equal(previous, data) {
			if err := AtomicWrite(o.Output, data); err != nil {
				h.WriteFailures.Add(1)
				h.Ready.Store(false)
				return err
			}
			previous = bytes.Clone(data)
			h.Writes.Add(1)
		}
		h.Inventory.Store(int64(count))
		h.Ambiguous.Store(int64(amb))
		h.Ready.Store(healthy)
		return nil
	}
	render := func() error {
		indexMu.Lock()
		data, n, a, err := index.Render(o.ClusterName)
		indexMu.Unlock()
		if err != nil {
			return err
		}
		return publish(data, n, a, true)
	}
	if err := render(); err != nil {
		return err
	}
	h.Contact.Store(lastGood.Unix())
	h.lastSync.Store(lastGood.UnixNano())
	if o.Once {
		return nil
	}
	// Periodic complete lists both verify freshness and force a new synchronized
	// informer after an outage. A successful health request alone cannot recover data.
	checks := time.NewTicker(o.Refresh)
	defer checks.Stop()
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	defer debounce.Stop()
	var pending <-chan time.Time
	var first time.Time
	for {
		select {
		case <-ctx.Done():
			h.Ready.Store(false)
			return ctx.Err()
		case <-events:
			if first.IsZero() {
				first = time.Now()
			}
			delay := min(o.Debounce, time.Until(first.Add(o.MaxDelay)))
			debounce.Reset(max(delay, 0))
			pending = debounce.C
		case <-pending:
			if err := render(); err != nil {
				// Retain the old file and retry without waiting for another Pod event.
				h.Ready.Store(false)
				debounce.Reset(o.Debounce)
			} else {
				first = time.Time{}
				pending = nil
			}

		case <-checks.C:
			// Restart with a complete list periodically. Only a newly synchronized
			// informer may publish after this point; API reachability alone is
			// insufficient to trust an old watch cache.
			return errResync
		}
	}
}

var errResync = fmt.Errorf("resynchronize inventory")

// Serve keeps freshness state across informer restarts. Failed synchronization
// is bounded by max-stale; the last valid file is withdrawn before retrying.
func Serve(ctx context.Context, client kubernetes.Interface, o Options, h *Health) error {
	if o.Debounce <= 0 || o.MaxDelay < o.Debounce || o.Refresh <= 0 || o.MaxStale < 2*o.Refresh {
		return fmt.Errorf("invalid debounce/freshness durations")
	}
	if err := ValidateValue(o.ClusterName); err != nil {
		return err
	}
	for {
		deadline := time.Now().Add(o.MaxStale)
		if contact := h.lastSync.Load(); contact != 0 {
			deadline = time.Unix(0, contact).Add(o.MaxStale)
			if deadline.Before(time.Now()) {
				deadline = time.Now().Add(o.MaxStale)
			}
		}
		runCtx, cancel := context.WithDeadline(ctx, deadline)
		err := Run(runCtx, client, o, h)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		if o.Once {
			return err
		}
		if err == errResync {
			continue
		}
		h.Ready.Store(false)
		if err != context.DeadlineExceeded && err != context.Canceled {
			return err
		}
		if data, e := os.ReadFile(o.Output); e == nil && !bytes.Equal(data, []byte(Header())) {
			_ = AtomicWrite(o.Output+".last-good", data)
		}
		if e := AtomicWrite(o.Output, []byte(Header())); e != nil {
			h.WriteFailures.Add(1)
			return e
		}
		h.Writes.Add(1)
		h.Inventory.Store(0)
		h.Ambiguous.Store(0)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}
