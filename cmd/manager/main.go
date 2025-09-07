package main

import (
	"flag"
	"os"
	"strings"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth" // cloud auth providers
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/loft-demos/vcluster-platform-flux-secret-controller/internal/controller"
)

func main() {
	// Add core scheme (we use core types + unstructured CRDs)
	_ = clientgoscheme.AddToScheme(nil)

	var (
		labelSelector  string
		secretKey      string
		secretPrefix   string
		serverTmpl     string
		loftDomain     string
		caSecretNS     string
		caSecretName   string
		caSecretKey    string
		fluxNSPatterns string
		controllerNS   string
	)

	flag.StringVar(&labelSelector, "selector", "gitops.flux/enabled=true", "label selector for VCIs")
	flag.StringVar(&secretKey, "secret-key", "value", "Secret.data key to store kubeconfig (Flux expects 'value' or 'value.yaml')")
	flag.StringVar(&secretPrefix, "secret-name-prefix", "vci-", "prefix for created kubeconfig secret names")
	flag.StringVar(&serverTmpl, "server-template",
		"https://{{ .Domain }}/kubernetes/project/{{ .Project }}/virtualcluster/{{ .Name }}",
		"Go template for kube-apiserver URL (vars: Domain, Project, Namespace, Name)")
	flag.StringVar(&loftDomain, "loft-domain", "beta.us.demo.dev", "Base domain used in --server-template")
	flag.StringVar(&caSecretNS, "ca-secret-namespace", "", "Namespace of Secret with custom CA PEM (optional)")
	flag.StringVar(&caSecretName, "ca-secret-name", "", "Secret name containing custom CA PEM (optional)")
	flag.StringVar(&caSecretKey, "ca-secret-key", "ca.pem", "Key in Secret with PEM-encoded CA (optional)")
	flag.StringVar(&fluxNSPatterns, "flux-namespaces", "flux-system", "comma-separated Flux namespace patterns (globs OK, e.g. 'flux-*,gitops-*')")
	flag.StringVar(&controllerNS, "controller-namespace", "vci-flux-secret-controller", "namespace where this controller runs (stores AccessKey tokens)")

	flag.Parse()

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
	    Metrics:               server.Options{BindAddress: ":8080"}, // was MetricsBindAddress
	    HealthProbeBindAddress: ":8081",
	    LeaderElection:         true,
	    LeaderElectionID:       "vcluster-platform-flux-secret-controller",
	})
	if err != nil {
		panic(err)
	}

	log := ctrl.Log.WithName("setup")
	opts := controller.Options{
		LabelSelector:         labelSelector,
		SecretKey:             secretKey,
		SecretPrefix:          secretPrefix,
		LoftDomain:            loftDomain,
		ServerTemplate:        serverTmpl,
		CASecretNS:            caSecretNS,
		CASecretName:          caSecretName,
		CASecretKey:           caSecretKey,
		FluxNamespacePatterns: strings.Split(fluxNSPatterns, ","),
		ControllerNamespace:   controllerNS,
	}
	if err := controller.NewVciReconciler(mgr.GetClient(), log, opts).SetupWithManager(mgr); err != nil {
		panic(err)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		panic(err)
	}
	_ = os.Stdout.Sync()
}
