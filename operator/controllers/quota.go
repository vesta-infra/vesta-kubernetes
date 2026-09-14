package controllers

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// Resource quotas for an environment.
//
// The hazard here is not the quota itself, it is when it takes effect. Kubernetes does not
// apply a ResourceQuota retroactively: pods that already exist keep running, and the quota
// refuses the NEXT admission. So a quota set too low is silent until somebody deploys, and
// then it fails in the middle of their rollout and looks like a platform fault.
//
// Everything below is arranged around that: work out the answer first, report it, and only
// create the object when somebody has said yes to what it will do.

const (
	quotaName      = "vesta-quota"
	limitRangeName = "vesta-limits"
)

// ResolveQuota merges the three levels, narrowest first.
//
// Field by field rather than whole-object, so an environment can tighten one dimension
// without restating the rest of the project's.
func ResolveQuota(platform, project, env *vestav1alpha1.QuotaSpec) *vestav1alpha1.QuotaSpec {
	if platform == nil && project == nil && env == nil {
		return nil
	}

	out := &vestav1alpha1.QuotaSpec{}
	for _, layer := range []*vestav1alpha1.QuotaSpec{platform, project, env} {
		if layer == nil {
			continue
		}
		if layer.Enforce != nil {
			out.Enforce = layer.Enforce
		}
		overlayString(&out.RequestsCPU, layer.RequestsCPU)
		overlayString(&out.RequestsMemory, layer.RequestsMemory)
		overlayString(&out.LimitsCPU, layer.LimitsCPU)
		overlayString(&out.LimitsMemory, layer.LimitsMemory)
		overlayString(&out.StorageTotal, layer.StorageTotal)
		overlayString(&out.DefaultRequestCPU, layer.DefaultRequestCPU)
		overlayString(&out.DefaultRequestMemory, layer.DefaultRequestMemory)
		overlayString(&out.DefaultLimitCPU, layer.DefaultLimitCPU)
		overlayString(&out.DefaultLimitMemory, layer.DefaultLimitMemory)
		overlayInt32(&out.MaxPods, layer.MaxPods)
		overlayInt32(&out.MaxDeployments, layer.MaxDeployments)
		overlayInt32(&out.MaxPVCs, layer.MaxPVCs)
		overlayInt32(&out.MaxServices, layer.MaxServices)
	}
	return out
}

func overlayString(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

func overlayInt32(dst **int32, v *int32) {
	if v != nil {
		*dst = v
	}
}

// CommittedResources sums what the apps in an environment are entitled to.
//
// Entitled, not currently using: an autoscaling app's ceiling counts, because a quota that
// fits today's replica count and not tomorrow's does not fail when it is set -- it fails
// when the HPA tries to scale, as a FailedCreate on a ReplicaSet that nobody is watching.
//
// resolveSize turns a pod-size preset into requests, so this stays free of the config
// resolver and testable on its own.
func CommittedResources(
	apps []vestav1alpha1.VestaApp,
	environment string,
	resolveSize func(name string) (corev1.ResourceList, corev1.ResourceList),
) corev1.ResourceList {

	total := corev1.ResourceList{}

	for i := range apps {
		app := &apps[i]
		env, ok := environmentConfig(app, environment)
		if !ok {
			continue
		}

		replicas := int64(1)
		if env.Replicas != nil {
			replicas = int64(*env.Replicas)
		}
		// The ceiling, not the floor. An HPA blocked by a quota reports ScalingLimited and
		// nothing else happens, which is among the least visible failures available.
		if env.Autoscale != nil && env.Autoscale.Enabled && env.Autoscale.MaxReplicas > 0 {
			if max := int64(env.Autoscale.MaxReplicas); max > replicas {
				replicas = max
			}
		}

		requests := resourcesForEnv(app, env, resolveSize)
		for name, qty := range requests {
			// Milli-precision throughout. Quantity.Value() rounds a sub-unit quantity UP
			// to the nearest whole one, so multiplying a 500m request by ten replicas
			// through Value() gives 10 cores rather than 5 -- which overstates what is
			// committed and makes a correctly-sized quota look too small.
			scaled := *resource.NewMilliQuantity(qty.MilliValue()*replicas, qty.Format)
			if existing, ok := total[name]; ok {
				scaled.Add(existing)
			}
			total[name] = scaled
		}
	}
	return total
}

func environmentConfig(app *vestav1alpha1.VestaApp, environment string) (vestav1alpha1.AppEnvironmentConfig, bool) {
	// An app that declares no environments runs in every one of its project's.
	if len(app.Spec.Environments) == 0 {
		return vestav1alpha1.AppEnvironmentConfig{Name: environment}, true
	}
	for _, env := range app.Spec.Environments {
		if env.Name == environment {
			return env, true
		}
	}
	return vestav1alpha1.AppEnvironmentConfig{}, false
}

func resourcesForEnv(
	app *vestav1alpha1.VestaApp,
	env vestav1alpha1.AppEnvironmentConfig,
	resolveSize func(string) (corev1.ResourceList, corev1.ResourceList),
) corev1.ResourceList {

	res := env.Resources
	if res == nil {
		res = app.Spec.Resources
	}
	if res == nil {
		return corev1.ResourceList{}
	}
	if res.Size != "" && resolveSize != nil {
		requests, _ := resolveSize(res.Size)
		return requests
	}
	return res.Requests
}

// QuotaVerdict reports whether applying a quota would immediately refuse work.
//
// This is what turns "set a quota and find out during the next deploy" into a decision made
// in advance. A quota below what is already committed is not rejected outright -- it is
// reported, because lowering one deliberately and then scaling down to fit is a reasonable
// thing to want.
func QuotaVerdict(spec *vestav1alpha1.QuotaSpec, committed corev1.ResourceList) (exceeds bool, reason string) {
	if spec == nil {
		return false, ""
	}

	var over []string
	check := func(label, configured string, name corev1.ResourceName) {
		if configured == "" {
			return
		}
		limit, err := resource.ParseQuantity(configured)
		if err != nil {
			over = append(over, fmt.Sprintf("%s is not a quantity (%q)", label, configured))
			return
		}
		have, ok := committed[name]
		if !ok {
			return
		}
		if have.Cmp(limit) > 0 {
			over = append(over, fmt.Sprintf("%s: %s already committed, quota is %s",
				label, have.String(), limit.String()))
		}
	}

	check("CPU requests", spec.RequestsCPU, corev1.ResourceCPU)
	check("memory requests", spec.RequestsMemory, corev1.ResourceMemory)

	if len(over) == 0 {
		return false, ""
	}
	sort.Strings(over)
	return true, strings.Join(over, "; ")
}

// BuildResourceQuota renders the quota object.
func BuildResourceQuota(namespace string, spec *vestav1alpha1.QuotaSpec) (*corev1.ResourceQuota, error) {
	if spec == nil {
		return nil, nil
	}

	hard := corev1.ResourceList{}
	add := func(name corev1.ResourceName, value string) error {
		if value == "" {
			return nil
		}
		q, err := resource.ParseQuantity(value)
		if err != nil {
			return fmt.Errorf("%s: %q is not a quantity", name, value)
		}
		hard[name] = q
		return nil
	}

	for name, value := range map[corev1.ResourceName]string{
		corev1.ResourceRequestsCPU:     spec.RequestsCPU,
		corev1.ResourceRequestsMemory:  spec.RequestsMemory,
		corev1.ResourceLimitsCPU:       spec.LimitsCPU,
		corev1.ResourceLimitsMemory:    spec.LimitsMemory,
		corev1.ResourceRequestsStorage: spec.StorageTotal,
	} {
		if err := add(name, value); err != nil {
			return nil, err
		}
	}

	for name, value := range map[corev1.ResourceName]*int32{
		corev1.ResourcePods:                   spec.MaxPods,
		"count/deployments.apps":              spec.MaxDeployments,
		corev1.ResourcePersistentVolumeClaims: spec.MaxPVCs,
		corev1.ResourceServices:               spec.MaxServices,
	} {
		if value != nil {
			hard[name] = *resource.NewQuantity(int64(*value), resource.DecimalSI)
		}
	}

	if len(hard) == 0 {
		return nil, nil
	}

	return &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      quotaName,
			Namespace: namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "vesta-operator"},
		},
		Spec: corev1.ResourceQuotaSpec{Hard: hard},
	}, nil
}

// BuildLimitRange renders the defaults that ship with a quota.
//
// Not optional, and this is the detail that decides whether quotas are usable at all. The
// moment a ResourceQuota names requests.cpu, Kubernetes requires every new pod in that
// namespace to set one -- and refuses the ones that do not. Vesta's own containers always
// carry requests, but nothing else in the namespace necessarily does, and a quota without
// this turns those into admission failures the first time anything is created.
func BuildLimitRange(namespace string, spec *vestav1alpha1.QuotaSpec) (*corev1.LimitRange, error) {
	if spec == nil {
		return nil, nil
	}

	defaultRequest := corev1.ResourceList{}
	defaultLimit := corev1.ResourceList{}

	parse := func(into corev1.ResourceList, name corev1.ResourceName, value, fallback string) error {
		if value == "" {
			value = fallback
		}
		if value == "" {
			return nil
		}
		q, err := resource.ParseQuantity(value)
		if err != nil {
			return fmt.Errorf("%s: %q is not a quantity", name, value)
		}
		into[name] = q
		return nil
	}

	// Fallbacks match what buildContainer already injects for an app that names no
	// resources, so a pod created outside Vesta is admitted on the same terms as one
	// created by it.
	if err := parse(defaultRequest, corev1.ResourceCPU, spec.DefaultRequestCPU, "100m"); err != nil {
		return nil, err
	}
	if err := parse(defaultRequest, corev1.ResourceMemory, spec.DefaultRequestMemory, "128Mi"); err != nil {
		return nil, err
	}
	if err := parse(defaultLimit, corev1.ResourceCPU, spec.DefaultLimitCPU, "500m"); err != nil {
		return nil, err
	}
	if err := parse(defaultLimit, corev1.ResourceMemory, spec.DefaultLimitMemory, "512Mi"); err != nil {
		return nil, err
	}

	return &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{
			Name:      limitRangeName,
			Namespace: namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "vesta-operator"},
		},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{{
				Type:           corev1.LimitTypeContainer,
				DefaultRequest: defaultRequest,
				Default:        defaultLimit,
			}},
		},
	}, nil
}

// ResourceListStrings renders a resource list for status, which is string-valued.
func ResourceListStrings(list corev1.ResourceList) map[string]string {
	if len(list) == 0 {
		return nil
	}
	out := make(map[string]string, len(list))
	for name, qty := range list {
		out[string(name)] = qty.String()
	}
	return out
}
