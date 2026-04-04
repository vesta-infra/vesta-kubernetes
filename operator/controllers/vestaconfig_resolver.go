package controllers

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

type ConfigResolver struct {
	client.Client
	mu     sync.RWMutex
	config *vestav1alpha1.VestaConfigSpec
}

func NewConfigResolver(c client.Client) *ConfigResolver {
	return &ConfigResolver{Client: c}
}

func (cr *ConfigResolver) Refresh(ctx context.Context) error {
	logger := log.FromContext(ctx)

	var configList vestav1alpha1.VestaConfigList
	if err := cr.List(ctx, &configList); err != nil {
		return fmt.Errorf("list VestaConfig: %w", err)
	}

	if len(configList.Items) == 0 {
		logger.Info("no VestaConfig found, using defaults")
		return nil
	}

	cr.mu.Lock()
	defer cr.mu.Unlock()
	cr.config = &configList.Items[0].Spec

	logger.Info("VestaConfig loaded", "domain", cr.config.Domain)
	return nil
}

func (cr *ConfigResolver) GetConfig() *vestav1alpha1.VestaConfigSpec {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	return cr.config
}

func (cr *ConfigResolver) ResolvePodSize(sizeName string) (corev1.ResourceList, corev1.ResourceList) {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	if cr.config != nil {
		for _, preset := range cr.config.PodSizeList {
			if preset.Name == sizeName {
				return preset.Requests, preset.Limits
			}
		}
	}

	// Fall back to built-in presets
	if reqs, lims, ok := builtinPodSize(sizeName); ok {
		return reqs, lims
	}

	return defaultRequests(), defaultLimits()
}

func (cr *ConfigResolver) GetGlobalImagePullSecrets() []corev1.LocalObjectReference {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	if cr.config == nil || cr.config.Registry == nil {
		return nil
	}
	return cr.config.Registry.GlobalImagePullSecrets
}

func (cr *ConfigResolver) GetAutoscaleDefaults() (minReplicas, maxReplicas, targetCPU, targetMemory int32) {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	minReplicas, maxReplicas, targetCPU, targetMemory = 1, 5, 70, 80

	if cr.config == nil || cr.config.AutoscaleDefaults == nil {
		return
	}

	d := cr.config.AutoscaleDefaults
	if d.MinReplicas != nil {
		minReplicas = *d.MinReplicas
	}
	if d.MaxReplicas != nil {
		maxReplicas = *d.MaxReplicas
	}
	if d.TargetCPU != nil {
		targetCPU = *d.TargetCPU
	}
	if d.TargetMemory != nil {
		targetMemory = *d.TargetMemory
	}
	return
}

func (cr *ConfigResolver) GetDomain() string {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	if cr.config == nil {
		return ""
	}
	return cr.config.Domain
}

func (cr *ConfigResolver) GetClusterIssuer() string {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	if cr.config == nil {
		return ""
	}
	return cr.config.ClusterIssuer
}

func (cr *ConfigResolver) GetIngressClassName() string {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	if cr.config == nil {
		return ""
	}
	return cr.config.IngressClassName
}

func defaultRequests() corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("100m"),
		corev1.ResourceMemory: resource.MustParse("128Mi"),
	}
}

func defaultLimits() corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("250m"),
		corev1.ResourceMemory: resource.MustParse("256Mi"),
	}
}

// builtinPodSize returns the resource requests and limits for a built-in size
// preset. This is used as a fallback when no VestaConfig CRD is present.
func builtinPodSize(name string) (corev1.ResourceList, corev1.ResourceList, bool) {
	type preset struct {
		cpuReq, memReq, cpuLim, memLim string
	}
	sizes := map[string]preset{
		"xxsmall": {"50m", "64Mi", "100m", "128Mi"},
		"xsmall":  {"100m", "128Mi", "250m", "256Mi"},
		"small":   {"250m", "256Mi", "500m", "512Mi"},
		"medium":  {"500m", "512Mi", "1", "1Gi"},
		"large":   {"1", "1Gi", "2", "2Gi"},
		"xlarge":  {"2", "2Gi", "4", "4Gi"},
	}
	p, ok := sizes[name]
	if !ok {
		return nil, nil, false
	}
	return corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(p.cpuReq),
			corev1.ResourceMemory: resource.MustParse(p.memReq),
		}, corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(p.cpuLim),
			corev1.ResourceMemory: resource.MustParse(p.memLim),
		}, true
}
