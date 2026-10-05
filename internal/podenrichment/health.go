package podenrichment

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

type Health struct {
	Ready, Synced                                                     atomic.Bool
	lastSync                                                          atomic.Int64
	Contact, Inventory, Ambiguous, Writes, WriteFailures, APIFailures atomic.Int64
}

func (h *Health) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if !h.Ready.Load() {
			http.Error(w, "inventory unavailable", 503)
			return
		}
		w.WriteHeader(200)
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		synced := 0
		if h.Synced.Load() {
			synced = 1
		}
		fmt.Fprintf(w, "pod_enricher_synced %d\npod_enricher_last_api_contact_seconds %d\npod_enricher_inventory_addresses %d\npod_enricher_ambiguous_addresses %d\npod_enricher_writes_total %d\npod_enricher_write_failures_total %d\npod_enricher_api_failures_total %d\n", synced, h.Contact.Load(), h.Inventory.Load(), h.Ambiguous.Load(), h.Writes.Load(), h.WriteFailures.Load(), h.APIFailures.Load())
	})
	return mux
}
