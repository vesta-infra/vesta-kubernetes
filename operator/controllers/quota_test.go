package controllers

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

func quota(cpu, mem string) *vestav1alpha1.QuotaSpec {
	return &vestav1alpha1.QuotaSpec{RequestsCPU: cpu, RequestsMemory: mem}
}

// A quota is not enforced unless somebody said so.
//
// Kubernetes does not apply a ResourceQuota to pods that already exist -- it refuses the
// next admission. A quota that defaults to on is therefore silent until somebody deploys,
// and then fails in the middle of their rollout looking like a platform fault.
func TestQuotasAreReportOnlyByDefault(t *testing.T) {
	if quota("2", "4Gi").Enforced() {
		t.Error("a quota with no explicit enforce was applied")
	}

	off := quota("2", "4Gi")
	off.Enforce = boolPtr(false)
	if off.Enforced() {
		t.Error("an explicitly disabled quota was applied")
	}

	on := quota("2", "4Gi")
	on.Enforce = boolPtr(true)
	if !on.Enforced() {
		t.Error("an explicitly enabled quota was not applied")
	}

	var none *vestav1alpha1.QuotaSpec
	if none.Enforced() {
		t.Error("a nil quota was applied")
	}
}

// The detail that decides whether quotas are usable at all.
//
// The moment a ResourceQuota names requests.cpu, Kubernetes requires every new pod in that
// namespace to set one and refuses those that do not. A quota shipped without defaults turns
// every pod created outside Vesta into an admission failure.
func TestLimitRangeAlwaysAccompaniesAQuota(t *testing.T) {
	lr, err := BuildLimitRange("acme-production", quota("2", "4Gi"))
	if err != nil {
		t.Fatalf("BuildLimitRange: %v", err)
	}
	if lr == nil {
		t.Fatal("a quota was built with no LimitRange; pods without explicit requests would be refused")
	}

	item := lr.Spec.Limits[0]
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		if _, ok := item.DefaultRequest[name]; !ok {
			t.Errorf("no default request for %s; a pod omitting it would be refused", name)
		}
		if _, ok := item.Default[name]; !ok {
			t.Errorf("no default limit for %s", name)
		}
	}
	if item.Type != corev1.LimitTypeContainer {
		t.Errorf("limit type = %q, want Container", item.Type)
	}
}

// Explicit defaults win over the fallbacks.
func TestLimitRangeHonoursConfiguredDefaults(t *testing.T) {
	spec := quota("2", "4Gi")
	spec.DefaultRequestCPU = "250m"
	spec.DefaultLimitMemory = "1Gi"

	lr, err := BuildLimitRange("ns", spec)
	if err != nil {
		t.Fatalf("BuildLimitRange: %v", err)
	}
	item := lr.Spec.Limits[0]

	if got := item.DefaultRequest[corev1.ResourceCPU]; got.String() != "250m" {
		t.Errorf("default CPU request = %s, want 250m", got.String())
	}
	if got := item.Default[corev1.ResourceMemory]; got.String() != "1Gi" {
		t.Errorf("default memory limit = %s, want 1Gi", got.String())
	}
	// Unset ones still get a fallback, or the quota refuses pods on that dimension.
	if _, ok := item.DefaultRequest[corev1.ResourceMemory]; !ok {
		t.Error("an unconfigured default was left empty")
	}
}

// Limits are opt-in, and sharper than they look: a limits.* quota makes every pod without
// that limit set unadmittable, and most pods do not set one.
func TestLimitsAreNotSetUnlessAsked(t *testing.T) {
	rq, err := BuildResourceQuota("ns", quota("2", "4Gi"))
	if err != nil {
		t.Fatalf("BuildResourceQuota: %v", err)
	}
	for _, name := range []corev1.ResourceName{corev1.ResourceLimitsCPU, corev1.ResourceLimitsMemory} {
		if _, ok := rq.Spec.Hard[name]; ok {
			t.Errorf("%s was set without being configured", name)
		}
	}

	withLimits := quota("2", "4Gi")
	withLimits.LimitsCPU = "4"
	rq, _ = BuildResourceQuota("ns", withLimits)
	if _, ok := rq.Spec.Hard[corev1.ResourceLimitsCPU]; !ok {
		t.Error("a configured limit was not set")
	}
}

// Working the answer out in advance is what turns "set a quota and find out during the next
// deploy" into a decision.
func TestQuotaVerdict(t *testing.T) {
	committed := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("3"),
		corev1.ResourceMemory: resource.MustParse("6Gi"),
	}

	exceeds, reason := QuotaVerdict(quota("2", "8Gi"), committed)
	if !exceeds {
		t.Error("a quota below what is already committed was reported as fine")
	}
	if !strings.Contains(reason, "CPU") {
		t.Errorf("reason = %q, want it to name the dimension that does not fit", reason)
	}
	if strings.Contains(reason, "memory") {
		t.Errorf("reason = %q, want only the dimension that actually exceeds", reason)
	}

	if exceeds, _ := QuotaVerdict(quota("4", "8Gi"), committed); exceeds {
		t.Error("a quota above what is committed was reported as exceeding")
	}

	// An unset dimension constrains nothing.
	if exceeds, _ := QuotaVerdict(quota("", ""), committed); exceeds {
		t.Error("an empty quota was reported as exceeding")
	}

	// A malformed quantity is reported rather than silently ignored -- the alternative is a
	// quota that looks set and does nothing.
	if exceeds, reason := QuotaVerdict(quota("two cores", ""), committed); !exceeds || !strings.Contains(reason, "quantity") {
		t.Errorf("a malformed quota gave (%v, %q), want it reported", exceeds, reason)
	}
}

// An autoscaling app's ceiling counts, not its current replicas. A quota that fits today and
// not tomorrow does not fail when it is set -- it fails when the HPA tries to scale, as a
// FailedCreate on a ReplicaSet nobody is watching.
func TestCommittedCountsTheAutoscalingCeiling(t *testing.T) {
	sizes := func(name string) (corev1.ResourceList, corev1.ResourceList) {
		return corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")}, nil
	}

	two := int32(2)
	ten := int32(10)
	app := vestav1alpha1.VestaApp{
		Spec: vestav1alpha1.VestaAppSpec{
			Project: "acme",
			Environments: []vestav1alpha1.AppEnvironmentConfig{{
				Name:      "production",
				Replicas:  &two,
				Resources: &vestav1alpha1.ResourceConfig{Size: "small"},
				Autoscale: &vestav1alpha1.AutoscaleConfig{Enabled: true, MaxReplicas: ten},
			}},
		},
	}

	total := CommittedResources([]vestav1alpha1.VestaApp{app}, "production", sizes)
	cpu := total[corev1.ResourceCPU]

	// 10 x 500m, not 2 x 500m.
	if cpu.MilliValue() != 5000 {
		t.Errorf("committed CPU = %s, want 5 (ten replicas at 500m, the autoscaling ceiling)", cpu.String())
	}
}

func TestCommittedIgnoresOtherEnvironments(t *testing.T) {
	sizes := func(string) (corev1.ResourceList, corev1.ResourceList) {
		return corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}, nil
	}

	one := int32(1)
	app := vestav1alpha1.VestaApp{
		Spec: vestav1alpha1.VestaAppSpec{
			Project: "acme",
			Environments: []vestav1alpha1.AppEnvironmentConfig{
				{Name: "staging", Replicas: &one, Resources: &vestav1alpha1.ResourceConfig{Size: "small"}},
			},
		},
	}

	if total := CommittedResources([]vestav1alpha1.VestaApp{app}, "production", sizes); len(total) != 0 {
		t.Errorf("an app that does not run in production contributed %v to its quota", total)
	}
}

// An app declaring no environments runs in all of them, so it counts everywhere.
func TestCommittedIncludesAppsWithNoEnvironments(t *testing.T) {
	sizes := func(string) (corev1.ResourceList, corev1.ResourceList) {
		return corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}, nil
	}

	app := vestav1alpha1.VestaApp{
		Spec: vestav1alpha1.VestaAppSpec{
			Project:   "acme",
			Resources: &vestav1alpha1.ResourceConfig{Size: "small"},
		},
	}

	total := CommittedResources([]vestav1alpha1.VestaApp{app}, "production", sizes)
	if cpu := total[corev1.ResourceCPU]; cpu.MilliValue() != 1000 {
		t.Errorf("committed CPU = %s, want 1", cpu.String())
	}
}

// An environment tightens one dimension without restating the project's whole quota.
func TestResolveQuotaMergesFieldByField(t *testing.T) {
	platform := &vestav1alpha1.QuotaSpec{RequestsCPU: "16", RequestsMemory: "32Gi", MaxPods: int32Ptr(100)}
	project := &vestav1alpha1.QuotaSpec{RequestsCPU: "8"}
	env := &vestav1alpha1.QuotaSpec{RequestsMemory: "4Gi", Enforce: boolPtr(true)}

	got := ResolveQuota(platform, project, env)

	if got.RequestsCPU != "8" {
		t.Errorf("CPU = %q, want the project's 8", got.RequestsCPU)
	}
	if got.RequestsMemory != "4Gi" {
		t.Errorf("memory = %q, want the environment's 4Gi", got.RequestsMemory)
	}
	if got.MaxPods == nil || *got.MaxPods != 100 {
		t.Error("the platform's pod cap was lost because narrower layers did not restate it")
	}
	if !got.Enforced() {
		t.Error("the environment's enforce flag was lost")
	}

	if ResolveQuota(nil, nil, nil) != nil {
		t.Error("three empty layers produced a quota")
	}
}

func int32Ptr(v int32) *int32 { return &v }
