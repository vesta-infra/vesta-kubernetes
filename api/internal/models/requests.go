package models

// Auth
type SetupRequest struct {
	Username    string `json:"username" binding:"required"`
	Email       string `json:"email" binding:"required"`
	Password    string `json:"password" binding:"required,min=8"`
	DisplayName string `json:"displayName"`
	TeamName    string `json:"teamName" binding:"required"`
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type RegisterRequest struct {
	Username    string `json:"username" binding:"required"`
	Email       string `json:"email" binding:"required"`
	Password    string `json:"password" binding:"required,min=8"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
}

type UpdateProfileRequest struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"currentPassword" binding:"required"`
	NewPassword     string `json:"newPassword" binding:"required,min=8"`
}

type CreateTokenRequest struct {
	Name      string   `json:"name" binding:"required"`
	Scopes    []string `json:"scopes,omitempty"`
	ExpiresIn string   `json:"expiresIn,omitempty"`
}

// Teams
type CreateTeamRequest struct {
	Name        string `json:"name" binding:"required"`
	DisplayName string `json:"displayName"`
}

type UpdateTeamRequest struct {
	DisplayName string `json:"displayName"`
}

type AddTeamMemberRequest struct {
	UserID string `json:"userId" binding:"required"`
	Role   string `json:"role"`
}

// Projects
type CreateProjectRequest struct {
	Name         string                 `json:"name" binding:"required"`
	DisplayName  string                 `json:"displayName"`
	Team         string                 `json:"team" binding:"required"`
	Labels       map[string]string      `json:"labels,omitempty"`
	Annotations  map[string]string      `json:"annotations,omitempty"`
	DefaultGit   map[string]interface{} `json:"defaultGit,omitempty"`
	DefaultBuild map[string]interface{} `json:"defaultBuild,omitempty"`
	DefaultImage map[string]interface{} `json:"defaultImage,omitempty"`
}

// Environments
type CreateEnvironmentRequest struct {
	Name            string `json:"name" binding:"required"`
	DisplayName     string `json:"displayName"`
	Order           int    `json:"order"`
	Branch          string `json:"branch,omitempty"`
	AutoDeploy      bool   `json:"autoDeploy,omitempty"`
	RequireApproval bool   `json:"requireApproval,omitempty"`
	AutoDeployPRs   bool   `json:"autoDeployPRs,omitempty"`
}

// Apps
type AppEnvironmentConfig struct {
	Name      string                 `json:"name" binding:"required"`
	Replicas  *int32                 `json:"replicas,omitempty"`
	Autoscale map[string]interface{} `json:"autoscale,omitempty"`
	Resources map[string]interface{} `json:"resources,omitempty"`
	Ingress   map[string]interface{} `json:"ingress,omitempty"`
}

type CreateAppRequest struct {
	Name         string                   `json:"name" binding:"required"`
	Environments []AppEnvironmentConfig   `json:"environments,omitempty"`
	Git          map[string]interface{}   `json:"git,omitempty"`
	Build        map[string]interface{}   `json:"build,omitempty"`
	Image        map[string]interface{}   `json:"image,omitempty"`
	Runtime      map[string]interface{}   `json:"runtime,omitempty"`
	Service      map[string]interface{}   `json:"service,omitempty"`
	Resources    map[string]interface{}   `json:"resources,omitempty"`
	HealthCheck  map[string]interface{}   `json:"healthCheck,omitempty"`
	Ingress      map[string]interface{}   `json:"ingress,omitempty"`
	Cronjobs     []map[string]interface{} `json:"cronjobs,omitempty"`
	Addons       []map[string]interface{} `json:"addons,omitempty"`
	CustomConfig map[string]interface{}   `json:"customConfig,omitempty"`
}

// Deploy
type DeployRequest struct {
	Tag         string        `json:"tag,omitempty"`
	Type        string        `json:"type,omitempty"`
	Environment string        `json:"environment" binding:"required"`
	Git         *GitDeployRef `json:"git,omitempty"`
	Reason      string        `json:"reason,omitempty"`
	CommitSHA   string        `json:"commitSHA,omitempty"`
}

type GitDeployRef struct {
	Branch    string `json:"branch,omitempty"`
	CommitSHA string `json:"commitSHA,omitempty"`
}

type RollbackRequest struct {
	Version     int    `json:"version" binding:"required"`
	Environment string `json:"environment" binding:"required"`
	Reason      string `json:"reason,omitempty"`
}

type ScaleRequest struct {
	Replicas int `json:"replicas" binding:"required,min=0"`
}

// Secrets
type CreateSecretRequest struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Data         map[string]string `json:"data,omitempty"`
	DockerConfig *DockerConfigReq  `json:"dockerConfig,omitempty"`
	TLS          *TLSConfigReq     `json:"tls,omitempty"`
}

type DockerConfigReq struct {
	Registry string `json:"registry" binding:"required"`
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type TLSConfigReq struct {
	Cert string `json:"cert" binding:"required"`
	Key  string `json:"key" binding:"required"`
}

type NameRef struct {
	Name string `json:"name"`
}

// Notifications
type CreateNotificationChannelRequest struct {
	Name   string                 `json:"name" binding:"required"`
	Type   string                 `json:"type" binding:"required"`
	Config map[string]interface{} `json:"config" binding:"required"`
	Events []string               `json:"events" binding:"required"`
}

type UpdateNotificationChannelRequest struct {
	Name    string                 `json:"name"`
	Config  map[string]interface{} `json:"config,omitempty"`
	Events  []string               `json:"events,omitempty"`
	Enabled *bool                  `json:"enabled,omitempty"`
}

// Builds
type TriggerBuildRequest struct {
	CommitSHA   string `json:"commitSha,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Environment string `json:"environment" binding:"required"`
	Reason      string `json:"reason,omitempty"`
}

type CancelBuildRequest struct {
	Reason string `json:"reason,omitempty"`
}
