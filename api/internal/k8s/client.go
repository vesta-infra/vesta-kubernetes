package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

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
