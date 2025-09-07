package main

import (
	"flag"
	"os"
	"strings"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	crlog "sigs.k8s.io/controller-runtime/pkg/log"       // NEW
	"sigs.k8s.io/controller-runtime/pkg/log/zap"         // NEW

	"github.com/loft-demos/vcluster-platform-flux-secret-controller/internal/controller"
)

func main() {
	// Build a real scheme and register core types
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	// ---- Logger setup (fixes "log.SetLogger(...) was never called") ----
	zopts := zap.Options{
		Development: false, // set true for verbose dev logs
	}
	zopts.BindFlags(flag.CommandLine) // allows --zap-log-level, --zap-devel, etc.
	// -------------------------------------------------------------------

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
		passthroughLbls string
		akType         string
		akTeam         string
		akDisplayNameTmpl string
	)

	flag.StringVar(&labelSelector, "selector", "vcluster.com/import-fluxcd=true", "label selector for VCIs")
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
	flag.StringVar(&passthroughLbls, "passthrough-label-prefixes", "flux-app/", "comma-separated label prefixes to copy from VCI to Flux Secret (e.g. 'flux-app/,rsip.loft.sh/')")
	flag.StringVar(&akType, "accesskey-type", "User", "AccessKey spec.type (User|Other)")
	flag.StringVar(&akTeam, "accesskey-team", "loft-admins", "AccessKey team (used when type=User)")
	flag.StringVar(&akDisplayNameTmpl, "accesskey-display-name-template", "flux-{{ .Name }}", "Go template for AccessKey displayName (vars: Name, Project, Namespace)")

	flag.Parse()

	// Set the global logger AFTER flag.Parse so zap picks up CLI flags
	crlog.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                server.Options{BindAddress: ":8080"},
		HealthProbeBindAddress: ":8081",
		LeaderElection:         true,
		LeaderElectionID:       "vcluster-platform-flux-secret-controller",
	})
	if err != nil {
		panic(err)
	}

	log := crlog.Log.WithName("setup")

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
		PassthroughPrefixes:   strings.Split(passthroughLbls, ","),
		AccessKeyType: akType,
		AccessKeyTeam: akTeam,
	}
	if err := controller.NewVciReconciler(mgr.GetClient(), log, opts).SetupWithManager(mgr); err != nil {
		panic(err)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		panic(err)
	}
	_ = os.Stdout.Sync()
}
