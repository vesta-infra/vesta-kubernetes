package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
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
	ExtraPorts  []extraPort       `json:"extraPorts,omitempty"`
	EnvVars     map[string]string `json:"envVars,omitempty"`
	HealthPath  string            `json:"healthPath,omitempty"`
	DataPath    string            `json:"dataPath,omitempty"`
	Command     string            `json:"command,omitempty"`
}

type extraPort struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

var builtinTemplates = []appTemplate{
	{ID: "nginx", Name: "Nginx", Description: "High-performance web server and reverse proxy", Category: "web", Icon: "🌐", Image: "nginx", Tag: "alpine", Port: 80, HealthPath: "/"},
	{ID: "node", Name: "Node.js", Description: "JavaScript runtime for server-side applications", Category: "runtime", Icon: "🟩", Image: "node", Tag: "20-alpine", Port: 3000},
	{ID: "python", Name: "Python (Flask)", Description: "Lightweight Python web framework", Category: "runtime", Icon: "🐍", Image: "python", Tag: "3.12-slim", Port: 5000},
	{ID: "go", Name: "Go", Description: "Go application with minimal Alpine base", Category: "runtime", Icon: "🔵", Image: "golang", Tag: "1.22-alpine", Port: 8080},
	{ID: "postgres", Name: "PostgreSQL", Description: "Advanced open-source relational database", Category: "database", Icon: "🐘", Image: "postgres", Tag: "16-alpine", Port: 5432, EnvVars: map[string]string{"POSTGRES_DB": "app", "POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": "changeme"}, DataPath: "/var/lib/postgresql/data"},
	{ID: "redis", Name: "Redis", Description: "In-memory data store for caching and messaging", Category: "database", Icon: "🔴", Image: "redis", Tag: "7-alpine", Port: 6379, EnvVars: map[string]string{"REDIS_PASSWORD": "changeme"}, DataPath: "/data", Command: "redis-server --requirepass $(REDIS_PASSWORD)"},
	{ID: "mongo", Name: "MongoDB", Description: "Document-oriented NoSQL database", Category: "database", Icon: "🍃", Image: "mongo", Tag: "7", Port: 27017, EnvVars: map[string]string{"MONGO_INITDB_ROOT_USERNAME": "admin", "MONGO_INITDB_ROOT_PASSWORD": "changeme"}, DataPath: "/data/db"},
	{ID: "mysql", Name: "MySQL", Description: "Popular open-source relational database", Category: "database", Icon: "🐬", Image: "mysql", Tag: "8", Port: 3306, EnvVars: map[string]string{"MYSQL_ROOT_PASSWORD": "changeme", "MYSQL_DATABASE": "app"}, DataPath: "/var/lib/mysql"},
	{ID: "rabbitmq", Name: "RabbitMQ", Description: "Message broker with management UI", Category: "messaging", Icon: "🐰", Image: "rabbitmq", Tag: "3-management-alpine", Port: 5672, ExtraPorts: []extraPort{{Name: "management", Port: 15672}}, EnvVars: map[string]string{"RABBITMQ_DEFAULT_USER": "admin", "RABBITMQ_DEFAULT_PASS": "changeme"}, DataPath: "/var/lib/rabbitmq"},
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

	// Separate env vars into sensitive (passwords/tokens) and plain values
	var plainEnvVars []map[string]interface{}
	secretData := map[string]string{}
	for k, v := range tmpl.EnvVars {
		if isSensitiveEnvVar(tmpl.ID, k) {
			secretData[k] = generatePassword()
		} else {
			plainEnvVars = append(plainEnvVars, map[string]interface{}{
				"name":  k,
				"value": v,
			})
		}
	}

	if len(plainEnvVars) > 0 {
		runtime["env"] = plainEnvVars
	}

	// Create VestaSecret for each environment with generated credentials
	if len(secretData) > 0 {
		secretName := appName + "-secrets"
		environments := req.Environments
		if len(environments) == 0 {
			environments = []string{"default"}
		}
		for _, env := range environments {
			namespace := fmt.Sprintf("%s-%s", req.Project, env)
			secretSpec := map[string]interface{}{
				"type":        "Opaque",
				"project":     req.Project,
				"app":         appName,
				"environment": env,
			}
			data := make(map[string]interface{}, len(secretData))
			for k, v := range secretData {
				data[k] = v
			}
			secretSpec["data"] = data

			secretObj := map[string]interface{}{
				"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
				"kind":       "VestaSecret",
				"metadata": map[string]interface{}{
					"name":      secretName,
					"namespace": namespace,
					"labels": map[string]interface{}{
						"kubernetes.getvesta.sh/project":     req.Project,
						"kubernetes.getvesta.sh/app":         appName,
						"kubernetes.getvesta.sh/environment": env,
						"kubernetes.getvesta.sh/template":    tmpl.ID,
					},
				},
				"spec": secretSpec,
			}
			if _, err := h.K8s.CreateResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, secretObj); err != nil {
				if !strings.Contains(err.Error(), "already exists") {
					c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to create secret for env %s: %v", env, err)})
					return
				}
			}
		}
		// Bind secret to app so the operator injects it via envFrom
		runtime["secrets"] = []map[string]interface{}{
			{"secretRef": map[string]interface{}{"name": secretName}},
		}
	}

	// Add command if specified
	if tmpl.Command != "" {
		runtime["command"] = tmpl.Command
	}

	// Add volume mount if template needs persistent storage (operator creates the PVC)
	if tmpl.DataPath != "" {
		pvcName := appName + "-data"
		storageSize := req.StorageSize
		if storageSize == "" {
			storageSize = "1Gi"
		}
		runtime["volumes"] = []map[string]interface{}{
			{
				"name":      "data",
				"mountPath": tmpl.DataPath,
				"persistentVolumeClaim": map[string]interface{}{
					"claimName": pvcName,
					"size":      storageSize,
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

	// Wire multi-port service when the template declares extra ports
	if len(tmpl.ExtraPorts) > 0 {
		ports := []map[string]interface{}{
			{"name": "default", "port": tmpl.Port},
		}
		for _, ep := range tmpl.ExtraPorts {
			ports = append(ports, map[string]interface{}{"name": ep.Name, "port": ep.Port})
		}
		spec["service"] = map[string]interface{}{"ports": ports}
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

func isSensitiveEnvVar(templateID, key string) bool {
	if templateID == "rabbitmq" && (key == "RABBITMQ_DEFAULT_USER" || key == "RABBITMQ_DEFAULT_PASS") {
		return true
	}

	upper := strings.ToUpper(key)
	return strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "PASS")
}

func generatePassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "changeme-" + hex.EncodeToString(b[:4])
	}
	return hex.EncodeToString(b)
}
