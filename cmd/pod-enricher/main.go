package main

import (
	"context"
	"flag"
	"github.com/bwagner5/arkime-k8s-operator/internal/podenrichment"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	o := podenrichment.Options{}
	var kubeconfig, kubeContext, probe string
	flag.StringVar(&o.Output, "output", "/var/run/arkime-kubernetes/pods.tagger", "output file")
	flag.StringVar(&o.ClusterName, "cluster-name", "", "cluster identity")
	flag.StringVar(&o.Namespace, "namespace", "", "restrict namespace (reduces coverage)")
	flag.StringVar(&o.LabelSelector, "label-selector", "", "restrict pods (reduces coverage)")
	flag.DurationVar(&o.Debounce, "debounce", 250*time.Millisecond, "publication debounce")
	flag.DurationVar(&o.MaxDelay, "max-delay", time.Second, "maximum debounce delay")
	flag.DurationVar(&o.MaxStale, "max-stale", 60*time.Second, "freshness deadline")
	flag.DurationVar(&o.Refresh, "refresh", 20*time.Second, "full inventory verification interval")
	flag.BoolVar(&o.Once, "once", false, "publish once and exit")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "local kubeconfig")
	flag.StringVar(&kubeContext, "kube-context", "", "explicit local context")
	flag.StringVar(&probe, "probe-address", ":8082", "health and metrics listener")
	flag.Parse()
	var config *rest.Config
	var err error
	if kubeContext != "" {
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		rules.ExplicitPath = kubeconfig
		config, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: kubeContext}).ClientConfig()
	} else {
		if kubeconfig != "" {
			log.Fatal("--kubeconfig requires --kube-context")
		}
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		log.Fatal(err)
	}
	config.Timeout = 25 * time.Second
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	h := &podenrichment.Health{}
	srv := &http.Server{Addr: probe, Handler: h.Handler(), ReadHeaderTimeout: 5 * time.Second}
	if !o.Once {
		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Print(err)
				cancel()
			}
		}()
		defer srv.Close()
	}
	if err := podenrichment.Serve(ctx, client, o, h); err != nil {
		log.Fatal(err)
	}
}
