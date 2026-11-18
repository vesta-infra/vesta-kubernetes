package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
	"kubernetes.getvesta.sh/operator/controllers"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(vestav1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "vesta-operator-lock",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.VestaProjectReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VestaProject")
		os.Exit(1)
	}

	// Created before the controllers that read it. The environment reconciler needs it for
	// platform quota defaults and pod-size presets, and it used to be built after that
	// block.
	configResolver := controllers.NewConfigResolver(mgr.GetClient())

	if err = (&controllers.VestaEnvironmentReconciler{
		ConfigResolver: configResolver,
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VestaEnvironment")
		os.Exit(1)
	}

	// The activator image and the ClusterRoles its ServiceAccounts bind to. Empty disables
	// wake-on-traffic: nothing is created, and a sleeping app simply stays down rather
	// than being answered by something that does not exist.
	activatorImage := os.Getenv("VESTA_ACTIVATOR_IMAGE")
	activatorNamespaceRole := envOrDefault("VESTA_ACTIVATOR_NAMESPACE_ROLE", "vesta-activator-namespace")
	activatorWakeRole := envOrDefault("VESTA_ACTIVATOR_WAKE_ROLE", "vesta-activator-wake")

	if err = (&controllers.VestaAppReconciler{
		ActivatorImage:         activatorImage,
		ActivatorNamespaceRole: activatorNamespaceRole,
		ActivatorWakeRole:      activatorWakeRole,
		Client:                 mgr.GetClient(),
		Scheme:                 mgr.GetScheme(),
		ConfigResolver:         configResolver,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VestaApp")
		os.Exit(1)
	}

	if err = (&controllers.VestaSecretReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		// Uncached, for verifying a Secret immediately after writing it.
		APIReader: mgr.GetAPIReader(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VestaSecret")
		os.Exit(1)
	}

	if err = (&controllers.VestaMiddlewareReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		// The RESTMapper is how the reconciler discovers whether Traefik's CRD is
		// installed. Without it the controller would report every middleware broken on a
		// cluster that simply uses a different ingress controller.
		Mapper: mgr.GetRESTMapper(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VestaMiddleware")
		os.Exit(1)
	}

	if err = (&controllers.VestaLogDrainReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VestaLogDrain")
		os.Exit(1)
	}

	if err = (&controllers.VestaAddonReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		ConfigResolver: configResolver,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VestaAddon")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
