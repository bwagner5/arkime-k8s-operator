package main

import (
	"flag"
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	cfg "github.com/bwagner5/arkime-k8s-operator/internal/config"
	"github.com/bwagner5/arkime-k8s-operator/internal/controller"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	clientconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"strings"
)

var version = "dev"
var commit = "unknown"

func main() {
	var metrics, probe, namespaces, kubeContext, podEnricherImage string
	var leader bool
	flag.StringVar(&podEnricherImage, "pod-enricher-image", cfg.DefaultPodEnricherImage(version), "default Kubernetes pod-enricher image")
	flag.StringVar(&kubeContext, "kube-context", "", "explicit kubeconfig context")
	flag.StringVar(&metrics, "metrics-bind-address", "0", "metrics address; 0 disables")
	flag.StringVar(&probe, "health-probe-bind-address", ":8081", "probe address")
	flag.StringVar(&namespaces, "watch-namespaces", "", "comma-separated namespaces; empty watches all")
	flag.BoolVar(&leader, "leader-elect", true, "enable leader election")
	logopts := zap.Options{}
	logopts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&logopts)))
	scheme := runtime.NewScheme()
	must(clientgoscheme.AddToScheme(scheme))
	must(api.AddToScheme(scheme))
	opts := ctrl.Options{Scheme: scheme, LeaderElection: leader, LeaderElectionID: "arkime-k8s-operator.arkime.com", HealthProbeBindAddress: probe, Metrics: metricsserver.Options{BindAddress: metrics}}
	if namespaces != "" {
		opts.Cache = cache.Options{DefaultNamespaces: map[string]cache.Config{}}
		for _, n := range strings.Split(namespaces, ",") {
			opts.Cache.DefaultNamespaces[strings.TrimSpace(n)] = cache.Config{}
		}
	}
	restConfig, err := clientconfig.GetConfigWithContext(kubeContext)
	must(err)
	manager, err := ctrl.NewManager(restConfig, opts)
	must(err)
	must((&controller.Reconciler{Client: manager.GetClient(), Scheme: scheme, PodEnricherImage: podEnricherImage}).SetupWithManager(manager))
	must(manager.AddHealthzCheck("healthz", healthz.Ping))
	must(manager.AddReadyzCheck("readyz", healthz.Ping))
	ctrl.Log.Info("starting Arkime operator", "version", version, "commit", commit)
	must(manager.Start(ctrl.SetupSignalHandler()))
}
func must(err error) {
	if err != nil {
		ctrl.Log.Error(err, "manager failed")
		os.Exit(1)
	}
}
