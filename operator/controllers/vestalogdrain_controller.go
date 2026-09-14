package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

const (
	collectorName      = "vesta-logs"
	collectorConfigMap = "vesta-logs-config"
	drainHomeNamespace = "vesta-system"
)

// VestaLogDrainReconciler renders the collector's configuration from every VestaLogDrain
// and reports back what the collector says it actually delivered.
//
// It reconciles the whole set on any change rather than one drain at a time: the collector
// has a single configuration file, so a per-drain reconcile would still have to read every
// other drain to write it.
type VestaLogDrainReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// MetricsURL is the collector's metrics endpoint. Overridable so tests can point it at
	// a local server.
	MetricsURL string
}

// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestalogdrains,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestalogdrains/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;update;patch

func (r *VestaLogDrainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var drains vestav1alpha1.VestaLogDrainList
	if err := r.List(ctx, &drains); err != nil {
		return ctrl.Result{}, fmt.Errorf("list log drains: %w", err)
	}

	targets, renderErrors, err := r.resolveTargets(ctx, drains.Items)
	if err != nil {
		return ctrl.Result{}, err
	}

	config, err := RenderFluentBitConfig(targets)
	if err != nil {
		// One unrenderable drain must not take the others down: resolveTargets has already
		// excluded it and recorded why, so reaching here means a bug rather than bad input.
		return ctrl.Result{}, fmt.Errorf("render collector config: %w", err)
	}

	checksum, err := r.writeConfig(ctx, config)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.rollCollector(ctx, checksum); err != nil {
		// A missing DaemonSet is the normal state when logging is disabled in the chart.
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		logger.V(1).Info("collector is not deployed; configuration written but nothing is shipping",
			"hint", "set logging.enabled=true in the chart")
	}

	stats := r.collectorStats(ctx)
	for i := range drains.Items {
		if err := r.updateStatus(ctx, &drains.Items[i], targets, renderErrors, stats); err != nil {
			logger.Error(err, "failed to update drain status", "drain", drains.Items[i].Name)
		}
	}

	// Metrics move without anything in the cluster changing, so status would otherwise go
	// stale until the next edit.
	return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
}

// resolveTargets turns each drain's scope into concrete namespaces, and works out which
// apps have opted out of a broader drain of the same name.
func (r *VestaLogDrainReconciler) resolveTargets(ctx context.Context, drains []vestav1alpha1.VestaLogDrain) ([]DrainTarget, map[string]string, error) {
	var apps vestav1alpha1.VestaAppList
	if err := r.List(ctx, &apps); err != nil {
		return nil, nil, fmt.Errorf("list apps: %w", err)
	}

	// Every "<namespace>/<app>" in the cluster, which an opt-out needs in order to
	// enumerate what remains.
	var allApps []string
	for i := range apps.Items {
		app := &apps.Items[i]
		for _, ns := range r.namespacesForApp(ctx, app) {
			allApps = append(allApps, ns+"/"+app.Name)
		}
	}
	sort.Strings(allApps)

	// A disabled drain is not an output; it is an instruction that a narrower scope should
	// not receive a broader drain of the same name.
	optOuts := map[string][]string{}
	for i := range drains {
		d := &drains[i]
		if boolOr(d.Spec.Enabled, true) {
			continue
		}
		for _, pair := range allApps {
			if scopeCovers(d.Spec, pair) {
				optOuts[d.Name] = append(optOuts[d.Name], pair)
			}
		}
	}

	renderErrors := map[string]string{}
	var targets []DrainTarget

	for i := range drains {
		d := drains[i]
		if !boolOr(d.Spec.Enabled, true) {
			continue
		}

		target := DrainTarget{
			Drain:        d,
			Namespaces:   r.namespacesForScope(ctx, d.Spec),
			ExcludedApps: optOuts[d.Name],
		}
		if len(target.ExcludedApps) > 0 {
			for _, pair := range allApps {
				if scopeCovers(d.Spec, pair) {
					target.IncludedApps = append(target.IncludedApps, pair)
				}
			}
		}

		// Render each drain on its own first, so one bad spec is reported against itself
		// rather than breaking the whole file.
		if _, err := renderOutput(target); err != nil {
			renderErrors[d.Name] = err.Error()
			continue
		}
		targets = append(targets, target)
	}

	return targets, renderErrors, nil
}

// scopeCovers reports whether a drain's scope includes a given "<namespace>/<app>".
func scopeCovers(spec vestav1alpha1.VestaLogDrainSpec, pair string) bool {
	ns, app, found := strings.Cut(pair, "/")
	if !found {
		return false
	}
	if spec.App != "" && spec.App != app {
		return false
	}
	if spec.Project != "" {
		expected := spec.Project + "-"
		if spec.Environment != "" {
			return ns == spec.Project+"-"+spec.Environment
		}
		// Prefix alone is ambiguous between "shop" and "shop-extra", so this is only a
		// cheap pre-filter; namespacesForScope is what decides the real list.
		if !strings.HasPrefix(ns, expected) {
			return false
		}
	}
	return true
}

func (r *VestaLogDrainReconciler) namespacesForScope(ctx context.Context, spec vestav1alpha1.VestaLogDrainSpec) []string {
	if spec.Project == "" {
		return nil
	}
	if spec.Environment != "" {
		return []string{spec.Project + "-" + spec.Environment}
	}

	var envs vestav1alpha1.VestaEnvironmentList
	if err := r.List(ctx, &envs, client.MatchingLabels{
		"kubernetes.getvesta.sh/project": spec.Project,
	}); err != nil {
		return nil
	}

	out := make([]string, 0, len(envs.Items))
	for _, env := range envs.Items {
		out = append(out, spec.Project+"-"+env.Name)
	}
	sort.Strings(out)
	return out
}

// namespacesForApp mirrors VestaAppReconciler.resolveTargetNamespaces. The two must agree:
// a namespace this misses is one whose logs are never routed.
func (r *VestaLogDrainReconciler) namespacesForApp(ctx context.Context, app *vestav1alpha1.VestaApp) []string {
	if len(app.Spec.Environments) > 0 {
		out := make([]string, 0, len(app.Spec.Environments))
		for _, env := range app.Spec.Environments {
			out = append(out, app.Spec.Project+"-"+env.Name)
		}
		return out
	}

	var envs vestav1alpha1.VestaEnvironmentList
	if err := r.List(ctx, &envs, client.MatchingLabels{
		"kubernetes.getvesta.sh/project": app.Spec.Project,
	}); err != nil {
		return nil
	}
	out := make([]string, 0, len(envs.Items))
	for _, env := range envs.Items {
		out = append(out, app.Spec.Project+"-"+env.Name)
	}
	return out
}

func (r *VestaLogDrainReconciler) writeConfig(ctx context.Context, config string) (string, error) {
	script := RenderRouteScript()
	sum := sha256.Sum256([]byte(config + script))
	checksum := hex.EncodeToString(sum[:])

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: collectorConfigMap, Namespace: drainHomeNamespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Labels = map[string]string{
			"app.kubernetes.io/managed-by": "vesta-operator",
			"app.kubernetes.io/component":  "logs",
		}
		cm.Data = map[string]string{
			"fluent-bit.conf": config,
			"vesta.lua":       script,
			"parsers.conf":    defaultParsers,
		}
		return nil
	})
	return checksum, err
}

// rollCollector stamps the config checksum on the DaemonSet's pod template.
//
// Fluent Bit's hot reload is deliberately not used: a reload that half-applies leaves nodes
// disagreeing about where logs go, with nothing saying so. A rolling restart is observable
// through `kubectl rollout status` and atomic per node.
func (r *VestaLogDrainReconciler) rollCollector(ctx context.Context, checksum string) error {
	ds := &appsv1.DaemonSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: drainHomeNamespace, Name: collectorName}, ds); err != nil {
		return err
	}

	if ds.Spec.Template.Annotations["checksum/config"] == checksum {
		return nil
	}
	if ds.Spec.Template.Annotations == nil {
		ds.Spec.Template.Annotations = map[string]string{}
	}
	ds.Spec.Template.Annotations["checksum/config"] = checksum
	return r.Update(ctx, ds)
}

// outputStats is one drain's counters as the collector reports them.
type outputStats struct {
	ProcRecords   int64 `json:"proc_records"`
	Errors        int64 `json:"errors"`
	RetriesFailed int64 `json:"retries_failed"`
}

// collectorStats reads the collector's own metrics.
//
// This is what separates a drain that is working from one that is merely configured.
// Without it a misconfigured destination is indistinguishable from an app that logged
// nothing, and the first sign of trouble is an empty dashboard during an incident.
func (r *VestaLogDrainReconciler) collectorStats(ctx context.Context) map[string]outputStats {
	url := r.MetricsURL
	if url == "" {
		url = fmt.Sprintf("http://%s.%s.svc:2020/api/v1/metrics", collectorName, drainHomeNamespace)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	// Short: this runs inside a reconcile, and a hung collector must not stall it.
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil
	}

	var parsed struct {
		Output map[string]outputStats `json:"output"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	return parsed.Output
}

func (r *VestaLogDrainReconciler) updateStatus(
	ctx context.Context,
	drain *vestav1alpha1.VestaLogDrain,
	targets []DrainTarget,
	renderErrors map[string]string,
	stats map[string]outputStats,
) error {
	status := vestav1alpha1.VestaLogDrainStatus{
		ObservedGeneration: drain.Generation,
		Scope:              describeScope(drain.Spec),
	}

	switch {
	case !boolOr(drain.Spec.Enabled, true):
		status.Reason = "disabled"
	case renderErrors[drain.Name] != "":
		status.Reason = renderErrors[drain.Name]
	default:
		for _, t := range targets {
			if t.Drain.Name != drain.Name {
				continue
			}
			status.Ready = true
			if s, ok := stats[drain.Name]; ok {
				status.RecordsDelivered = s.ProcRecords
				status.Errors = s.Errors
				status.RetriesFailed = s.RetriesFailed
				if s.Errors > 0 || s.RetriesFailed > 0 {
					status.Ready = false
					status.Reason = fmt.Sprintf("%d delivery errors, %d batches given up on",
						s.Errors, s.RetriesFailed)
				}
				if s.ProcRecords > 0 {
					status.LastDeliveryAt = time.Now().UTC().Format(time.RFC3339)
				}
			}
			break
		}
	}

	if drain.Status == status {
		return nil
	}
	drain.Status = status
	return r.Status().Update(ctx, drain)
}

const defaultParsers = `[PARSER]
    Name   json
    Format json
    Time_Key time
    Time_Format %d/%b/%Y:%H:%M:%S %z

[PARSER]
    Name        docker
    Format      json
    Time_Key    time
    Time_Format %Y-%m-%dT%H:%M:%S.%L
    Time_Keep   On
`

func (r *VestaLogDrainReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vestav1alpha1.VestaLogDrain{}).
		// An app appearing or disappearing changes which namespaces a project-scoped drain
		// covers, and which apps an opt-out has to enumerate around.
		Watches(
			&vestav1alpha1.VestaApp{},
			handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []ctrl.Request {
				return []ctrl.Request{{NamespacedName: types.NamespacedName{
					Name: "resync", Namespace: drainHomeNamespace,
				}}}
			}),
		).
		Complete(r)
}
