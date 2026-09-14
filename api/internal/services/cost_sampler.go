package services

import (
	"context"
	"log"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/k8s"
)

// CostSampler records what workloads are reserving, on an interval.
//
// It works with no Prometheus at all, which is the point: reading a Deployment's replica
// count and its containers' requests answers "what is this reserving" completely, and that
// is the whole basis of the cost figure. Prometheus, when present, only adds the usage
// column beside it.
//
// That also makes it the one place scale-to-zero becomes visible as money: a sleeping app
// samples zero replicas, so its compute cost for that interval is zero.
type CostSampler struct {
	DB  *db.DB
	K8s *k8s.Client

	interval time.Duration
	retain   time.Duration
}

const (
	// costSampleInterval is the resolution of the record. Five minutes is fine enough to
	// see a scale-up and coarse enough that a month of history stays small.
	costSampleInterval = 5 * time.Minute
	// costRetention bounds the table. Long enough to answer "what did last month cost",
	// short enough that nobody has to think about it.
	costRetention = 90 * 24 * time.Hour
)

func NewCostSampler(database *db.DB, kc *k8s.Client) *CostSampler {
	return &CostSampler{DB: database, K8s: kc, interval: costSampleInterval, retain: costRetention}
}

func (s *CostSampler) Start(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	log.Println("[cost] sampler started")
	for {
		select {
		case <-ctx.Done():
			log.Println("[cost] sampler stopped")
			return
		case <-ticker.C:
			s.sample(ctx)
		}
	}
}

func (s *CostSampler) sample(ctx context.Context) {
	// One replica samples. Two would double every figure on the page, which is a worse
	// failure than missing a sample: the numbers would be wrong rather than incomplete.
	locked, release, err := s.DB.TryAdvisoryLock(ctx, db.AdvisoryLockCostSampler)
	if err != nil || !locked {
		return
	}
	defer release()

	envs, err := s.K8s.ListResources(ctx, k8s.VestaEnvironmentGVR, vestaSystemNS, "")
	if err != nil {
		log.Printf("[cost] listing environments: %v", err)
		return
	}

	var samples []db.CostSample
	for i := range envs.Items {
		env := envs.Items[i]
		spec, _ := env.Object["spec"].(map[string]interface{})
		project, _ := spec["project"].(string)
		if project == "" {
			continue
		}

		namespace := project + "-" + env.GetName()
		samples = append(samples, s.sampleNamespace(ctx, project, env.GetName(), namespace)...)
	}

	if err := s.DB.InsertCostSamples(ctx, samples); err != nil {
		log.Printf("[cost] recording %d samples: %v", len(samples), err)
		return
	}

	if err := s.DB.PruneCostSamples(ctx, s.retain); err != nil {
		log.Printf("[cost] pruning old samples: %v", err)
	}
}

// sampleNamespace records every workload in one environment.
func (s *CostSampler) sampleNamespace(ctx context.Context, project, environment, namespace string) []db.CostSample {
	deploys, err := s.K8s.Clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/managed-by=vesta-operator",
	})
	if err != nil {
		// A namespace that does not exist yet is not an error worth logging every five
		// minutes; anything else is transient and the next sample will catch it.
		return nil
	}

	usage := s.podUsage(ctx, namespace)
	storage := s.claimedStorage(ctx, namespace)

	out := make([]db.CostSample, 0, len(deploys.Items))
	for i := range deploys.Items {
		d := &deploys.Items[i]
		app := d.Labels["kubernetes.getvesta.sh/app"]
		if app == "" {
			app = d.Name
		}

		cpu, memory := containerRequests(d)

		sample := db.CostSample{
			ProjectID:   project,
			Environment: environment,
			AppID:       app,
			Kind:        kindOf(d),
			// The actual count, not the desired one. A sleeping app reports zero here,
			// which is how scale-to-zero shows up as costing nothing.
			Replicas:      d.Status.Replicas,
			CPUMillicores: cpu,
			MemoryBytes:   memory,
			StorageBytes:  storage[app],
			Interval:      s.interval,
		}

		if u, ok := usage[app]; ok {
			sample.CPUUsedMillicores = u.cpu
			sample.MemoryUsedBytes = u.memory
		}

		out = append(out, sample)
	}

	// Add-ons run as StatefulSets and are billable too -- a database nobody is using still
	// holds a node's worth of memory and a volume.
	sets, err := s.K8s.Clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/component=addon",
	})
	if err == nil {
		for i := range sets.Items {
			st := &sets.Items[i]
			cpu, memory := statefulSetRequests(st)
			out = append(out, db.CostSample{
				ProjectID:     project,
				Environment:   environment,
				AppID:         st.Name,
				Kind:          "addon",
				Replicas:      st.Status.Replicas,
				CPUMillicores: cpu,
				MemoryBytes:   memory,
				StorageBytes:  storage[st.Name],
				Interval:      s.interval,
			})
		}
	}

	return out
}

type podUsage struct{ cpu, memory int64 }

// podUsage reads live consumption from metrics-server, when it is installed.
//
// Absent, every app simply reports no usage and the efficiency column is empty. The cost
// figure itself does not depend on this.
func (s *CostSampler) podUsage(ctx context.Context, namespace string) map[string]podUsage {
	metrics, err := s.K8s.GetPodMetrics(ctx, namespace, "")
	if err != nil {
		return nil
	}

	out := map[string]podUsage{}
	for pod, usage := range metrics {
		// Pod names are <deployment>-<replicaset>-<random>; the app is the prefix before
		// the generated suffixes.
		app := pod
		if i := strings.LastIndex(app, "-"); i > 0 {
			app = app[:i]
		}
		if i := strings.LastIndex(app, "-"); i > 0 {
			app = app[:i]
		}

		agg := out[app]
		agg.cpu += k8s.ParseCPUNano(usage.CPU) / 1_000_000
		agg.memory += k8s.ParseMemBytes(usage.Memory)
		out[app] = agg
	}
	return out
}

// claimedStorage totals the volumes each workload holds.
func (s *CostSampler) claimedStorage(ctx context.Context, namespace string) map[string]int64 {
	claims, err := s.K8s.Clientset.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}

	out := map[string]int64{}
	for i := range claims.Items {
		pvc := &claims.Items[i]
		owner := pvc.Labels["kubernetes.getvesta.sh/app"]
		if owner == "" {
			owner = pvc.Labels["kubernetes.getvesta.sh/addon"]
		}
		if owner == "" {
			// A StatefulSet's claims are named data-<set>-<ordinal>.
			owner = strings.TrimPrefix(pvc.Name, "data-")
			if i := strings.LastIndex(owner, "-"); i > 0 {
				owner = owner[:i]
			}
		}
		if q, ok := pvc.Status.Capacity["storage"]; ok {
			out[owner] += q.Value()
		} else if q, ok := pvc.Spec.Resources.Requests["storage"]; ok {
			out[owner] += q.Value()
		}
	}
	return out
}

func containerRequests(d *appsv1.Deployment) (cpuMillicores, memoryBytes int64) {
	for _, c := range d.Spec.Template.Spec.Containers {
		if q, ok := c.Resources.Requests["cpu"]; ok {
			cpuMillicores += q.MilliValue()
		}
		if q, ok := c.Resources.Requests["memory"]; ok {
			memoryBytes += q.Value()
		}
	}
	return
}

func statefulSetRequests(s *appsv1.StatefulSet) (cpuMillicores, memoryBytes int64) {
	for _, c := range s.Spec.Template.Spec.Containers {
		if q, ok := c.Resources.Requests["cpu"]; ok {
			cpuMillicores += q.MilliValue()
		}
		if q, ok := c.Resources.Requests["memory"]; ok {
			memoryBytes += q.Value()
		}
	}
	return
}

func kindOf(d *appsv1.Deployment) string {
	if d.Labels["app.kubernetes.io/component"] == "addon" {
		return "addon"
	}
	return "app"
}
