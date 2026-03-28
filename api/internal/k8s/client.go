package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

var (
	VestaAppGVR = schema.GroupVersionResource{
		Group: "kubernetes.getvesta.sh", Version: "v1alpha1", Resource: "vestaapps",
	}
	VestaProjectGVR = schema.GroupVersionResource{
		Group: "kubernetes.getvesta.sh", Version: "v1alpha1", Resource: "vestaprojects",
	}
	VestaEnvironmentGVR = schema.GroupVersionResource{
		Group: "kubernetes.getvesta.sh", Version: "v1alpha1", Resource: "vestaenvironments",
	}
	VestaSecretGVR = schema.GroupVersionResource{
		Group: "kubernetes.getvesta.sh", Version: "v1alpha1", Resource: "vestasecrets",
	}
	VestaConfigGVR = schema.GroupVersionResource{
		Group: "kubernetes.getvesta.sh", Version: "v1alpha1", Resource: "vestaconfigs",
	}

	DeploymentGVR = schema.GroupVersionResource{
		Group: "apps", Version: "v1", Resource: "deployments",
	}
)

type Client struct {
	Dynamic   dynamic.Interface
	Clientset kubernetes.Interface
}

func NewClient() (*Client, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			if home := homedir.HomeDir(); home != "" {
				kubeconfig = filepath.Join(home, ".kube", "config")
			}
		}
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("cannot build k8s config: %w", err)
		}
	}

	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("cannot create dynamic client: %w", err)
	}

	cs, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("cannot create clientset: %w", err)
	}

	return &Client{Dynamic: dyn, Clientset: cs}, nil
}

func (c *Client) CreateResource(ctx context.Context, gvr schema.GroupVersionResource, namespace string, obj map[string]interface{}) (*unstructured.Unstructured, error) {
	u := &unstructured.Unstructured{Object: obj}
	return c.Dynamic.Resource(gvr).Namespace(namespace).Create(ctx, u, metav1.CreateOptions{})
}

func (c *Client) GetResource(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error) {
	return c.Dynamic.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (c *Client) ListResources(ctx context.Context, gvr schema.GroupVersionResource, namespace string, labelSelector string) (*unstructured.UnstructuredList, error) {
	opts := metav1.ListOptions{}
	if labelSelector != "" {
		opts.LabelSelector = labelSelector
	}
	if namespace == "" {
		return c.Dynamic.Resource(gvr).List(ctx, opts)
	}
	return c.Dynamic.Resource(gvr).Namespace(namespace).List(ctx, opts)
}

func (c *Client) UpdateResource(ctx context.Context, gvr schema.GroupVersionResource, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return c.Dynamic.Resource(gvr).Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
}

func (c *Client) DeleteResource(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) error {
	return c.Dynamic.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

func (c *Client) PatchResource(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, patchData []byte) (*unstructured.Unstructured, error) {
	return c.Dynamic.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.MergePatchType, patchData, metav1.PatchOptions{})
}

func (c *Client) GetClusterResource(ctx context.Context, gvr schema.GroupVersionResource, name string) (*unstructured.Unstructured, error) {
	return c.Dynamic.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
}

func ToJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ListPods returns pods in a namespace matching a label selector.
func (c *Client) ListPods(ctx context.Context, namespace, labelSelector string) ([]corev1.Pod, error) {
	opts := metav1.ListOptions{}
	if labelSelector != "" {
		opts.LabelSelector = labelSelector
	}
	list, err := c.Clientset.CoreV1().Pods(namespace).List(ctx, opts)
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// GetPodLogs returns the log output for a pod/container.
func (c *Client) GetPodLogs(ctx context.Context, namespace, podName, container string, tailLines int64, previous bool) (string, error) {
	opts := &corev1.PodLogOptions{
		TailLines: &tailLines,
		Previous:  previous,
	}
	if container != "" {
		opts.Container = container
	}
	req := c.Clientset.CoreV1().Pods(namespace).GetLogs(podName, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()
	data, err := io.ReadAll(stream)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ContainerMetricsUsage holds live CPU/memory usage for a single container.
type ContainerMetricsUsage struct {
	Name   string `json:"name"`
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

// PodMetricsUsage holds live CPU/memory usage for a pod.
type PodMetricsUsage struct {
	CPU        string                  `json:"cpu"`
	Memory     string                  `json:"memory"`
	Containers []ContainerMetricsUsage `json:"containers,omitempty"`
}

// GetPodMetrics queries the metrics.k8s.io API for live resource usage.
func (c *Client) GetPodMetrics(ctx context.Context, namespace, labelSelector string) (map[string]PodMetricsUsage, error) {
	path := fmt.Sprintf("/apis/metrics.k8s.io/v1beta1/namespaces/%s/pods", namespace)
	if labelSelector != "" {
		path += "?labelSelector=" + labelSelector
	}
	raw, err := c.Clientset.Discovery().RESTClient().Get().AbsPath(path).DoRaw(ctx)
	if err != nil {
		return nil, err
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Containers []struct {
				Name  string `json:"name"`
				Usage struct {
					CPU    string `json:"cpu"`
					Memory string `json:"memory"`
				} `json:"usage"`
			} `json:"containers"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}

	metrics := make(map[string]PodMetricsUsage, len(result.Items))
	for _, item := range result.Items {
		usage := PodMetricsUsage{}
		var totalCPUNano int64
		var totalMemBytes int64
		for _, c := range item.Containers {
			usage.Containers = append(usage.Containers, ContainerMetricsUsage{
				Name:   c.Name,
				CPU:    c.Usage.CPU,
				Memory: c.Usage.Memory,
			})
			totalCPUNano += ParseCPUNano(c.Usage.CPU)
			totalMemBytes += ParseMemBytes(c.Usage.Memory)
		}
		usage.CPU = FormatCPUNano(totalCPUNano)
		usage.Memory = FormatMemBytes(totalMemBytes)
		metrics[item.Metadata.Name] = usage
	}
	return metrics, nil
}

func ParseCPUNano(s string) int64 {
	if s == "" {
		return 0
	}
	// metrics-server returns values like "250n" (nanocores) or "1m" (millicores)
	if len(s) > 1 && s[len(s)-1] == 'n' {
		v, _ := strconv.ParseInt(s[:len(s)-1], 10, 64)
		return v
	}
	if len(s) > 1 && s[len(s)-1] == 'm' {
		v, _ := strconv.ParseInt(s[:len(s)-1], 10, 64)
		return v * 1000000
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v * 1000000000
}

func FormatCPUNano(n int64) string {
	if n == 0 {
		return "0"
	}
	if n < 1000000 {
		return fmt.Sprintf("%dn", n)
	}
	return fmt.Sprintf("%dm", n/1000000)
}

func ParseMemBytes(s string) int64 {
	if s == "" {
		return 0
	}
	// metrics-server returns values like "12345Ki"
	if len(s) > 2 && s[len(s)-2:] == "Ki" {
		v, _ := strconv.ParseInt(s[:len(s)-2], 10, 64)
		return v * 1024
	}
	if len(s) > 2 && s[len(s)-2:] == "Mi" {
		v, _ := strconv.ParseInt(s[:len(s)-2], 10, 64)
		return v * 1024 * 1024
	}
	if len(s) > 2 && s[len(s)-2:] == "Gi" {
		v, _ := strconv.ParseInt(s[:len(s)-2], 10, 64)
		return v * 1024 * 1024 * 1024
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func FormatMemBytes(b int64) string {
	if b == 0 {
		return "0"
	}
	if b >= 1024*1024*1024 {
		return fmt.Sprintf("%dGi", b/(1024*1024*1024))
	}
	if b >= 1024*1024 {
		return fmt.Sprintf("%dMi", b/(1024*1024))
	}
	if b >= 1024 {
		return fmt.Sprintf("%dKi", b/1024)
	}
	return fmt.Sprintf("%d", b)
}
