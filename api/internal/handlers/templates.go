package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

type appTemplate struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	Icon        string            `json:"icon"`
	Image       string            `json:"image"`
	Tag         string            `json:"tag"`
	Port        int               `json:"port"`
	EnvVars     map[string]string `json:"envVars,omitempty"`
	HealthPath  string            `json:"healthPath,omitempty"`
	DataPath    string            `json:"dataPath,omitempty"`
	Command     string            `json:"command,omitempty"`
}

var builtinTemplates = []appTemplate{
	{ID: "nginx", Name: "Nginx", Description: "High-performance web server and reverse proxy", Category: "web", Icon: "🌐", Image: "nginx", Tag: "alpine", Port: 80, HealthPath: "/"},
	{ID: "node", Name: "Node.js", Description: "JavaScript runtime for server-side applications", Category: "runtime", Icon: "🟩", Image: "node", Tag: "20-alpine", Port: 3000},
	{ID: "python", Name: "Python (Flask)", Description: "Lightweight Python web framework", Category: "runtime", Icon: "🐍", Image: "python", Tag: "3.12-slim", Port: 5000},
	{ID: "go", Name: "Go", Description: "Go application with minimal Alpine base", Category: "runtime", Icon: "🔵", Image: "golang", Tag: "1.22-alpine", Port: 8080},
	{ID: "postgres", Name: "PostgreSQL", Description: "Advanced open-source relational database", Category: "database", Icon: "🐘", Image: "postgres", Tag: "16-alpine", Port: 5432, EnvVars: map[string]string{"POSTGRES_DB": "app", "POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": "changeme"}, DataPath: "/var/lib/postgresql/data"},
	{ID: "redis", Name: "Redis", Description: "In-memory data store for caching and messaging", Category: "database", Icon: "🔴", Image: "redis", Tag: "7-alpine", Port: 6379, DataPath: "/data"},
	{ID: "mongo", Name: "MongoDB", Description: "Document-oriented NoSQL database", Category: "database", Icon: "🍃", Image: "mongo", Tag: "7", Port: 27017, DataPath: "/data/db"},
	{ID: "mysql", Name: "MySQL", Description: "Popular open-source relational database", Category: "database", Icon: "🐬", Image: "mysql", Tag: "8", Port: 3306, EnvVars: map[string]string{"MYSQL_ROOT_PASSWORD": "changeme", "MYSQL_DATABASE": "app"}, DataPath: "/var/lib/mysql"},
	{ID: "rabbitmq", Name: "RabbitMQ", Description: "Message broker with management UI", Category: "messaging", Icon: "🐰", Image: "rabbitmq", Tag: "3-management-alpine", Port: 15672, DataPath: "/var/lib/rabbitmq"},
	{ID: "minio", Name: "MinIO", Description: "S3-compatible object storage", Category: "storage", Icon: "📦", Image: "minio/minio", Tag: "latest", Port: 9000, EnvVars: map[string]string{"MINIO_ROOT_USER": "minioadmin", "MINIO_ROOT_PASSWORD": "minioadmin"}, DataPath: "/data", Command: "server /data"},
}

func (h *Handler) ListTemplates(c *gin.Context) {
	category := c.Query("category")
	search := c.Query("search")

	filtered := make([]appTemplate, 0)
	for _, t := range builtinTemplates {
		if category != "" && t.Category != category {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(t.Name), strings.ToLower(search)) && !strings.Contains(strings.ToLower(t.Description), strings.ToLower(search)) {
			continue
		}
		filtered = append(filtered, t)
	}

	c.JSON(http.StatusOK, gin.H{"items": filtered, "total": len(filtered)})
}

func (h *Handler) DeployTemplate(c *gin.Context) {
	templateId := c.Param("id")
	var req struct {
		Project      string                 `json:"project" binding:"required"`
		Environments []string               `json:"environments,omitempty"`
		Name         string                 `json:"name,omitempty"`
		StorageSize  string                 `json:"storageSize,omitempty"`
		Overrides    map[string]interface{} `json:"overrides,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	// Find the template
	var tmpl *appTemplate
	for _, t := range builtinTemplates {
		if t.ID == templateId {
			tmpl = &t
			break
		}
	}
	if tmpl == nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "template not found"})
		return
	}

	appName := req.Name
	if appName == "" {
		appName = tmpl.ID
	}

	// Build the app spec
	envs := make([]interface{}, 0)
	for _, e := range req.Environments {
		envs = append(envs, map[string]interface{}{"name": e, "replicas": 1})
	}

	runtime := map[string]interface{}{
		"port": tmpl.Port,
	}

	// Inject env vars from template
	if len(tmpl.EnvVars) > 0 {
		envVarList := make([]map[string]interface{}, 0, len(tmpl.EnvVars))
		for k, v := range tmpl.EnvVars {
			envVarList = append(envVarList, map[string]interface{}{
				"name":  k,
				"value": v,
			})
		}
		runtime["env"] = envVarList
	}

	// Add command if specified
	if tmpl.Command != "" {
		runtime["command"] = tmpl.Command
	}

	// Create PVC and add volume mount if template needs persistent storage
	if tmpl.DataPath != "" {
		pvcName := appName + "-data"
		if err := ensurePVC(c.Request.Context(), h.K8s, pvcName, vestaSystemNS); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to create PVC: %v", err)})
			return
		}
		runtime["volumes"] = []map[string]interface{}{
			{
				"name":      "data",
				"mountPath": tmpl.DataPath,
				"persistentVolumeClaim": map[string]interface{}{
					"claimName": pvcName,
				},
			},
		}
	}

	spec := map[string]interface{}{
		"project": req.Project,
		"image": map[string]interface{}{
			"repository": tmpl.Image,
			"tag":        tmpl.Tag,
		},
		"runtime": runtime,
	}
	if len(envs) > 0 {
		spec["environments"] = envs
	}
	if tmpl.HealthPath != "" {
		spec["healthCheck"] = map[string]interface{}{
			"type": "http",
			"path": tmpl.HealthPath,
		}
	}

	obj := map[string]interface{}{
		"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
		"kind":       "VestaApp",
		"metadata": map[string]interface{}{
			"name":      appName,
			"namespace": vestaSystemNS,
			"labels": map[string]interface{}{
				"kubernetes.getvesta.sh/project":  req.Project,
				"kubernetes.getvesta.sh/template": tmpl.ID,
			},
		},
		"spec": spec,
	}

	created, err := h.K8s.CreateResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, obj)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to create app from template: %v", err)})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":       created.GetName(),
		"name":     appName,
		"template": tmpl.ID,
		"project":  req.Project,
	})
}

func ensurePVC(ctx context.Context, k *k8s.Client, name, namespace string) error {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
	}
	_, err := k.Clientset.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil && strings.Contains(err.Error(), "already exists") {
		return nil
	}
	return err
}
