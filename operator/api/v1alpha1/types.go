package v1alpha1

import (
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ============================================================================
// VestaApp
// ============================================================================

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.url`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.status.currentImage`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

type VestaApp struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VestaAppSpec   `json:"spec,omitempty"`
	Status VestaAppStatus `json:"status,omitempty"`
}

type VestaAppSpec struct {
	Project      string                 `json:"project"`
	Environments []AppEnvironmentConfig `json:"environments,omitempty"`

	Git   *GitSource   `json:"git,omitempty"`
	Build *BuildConfig `json:"build,omitempty"`
	Image *ImageConfig `json:"image,omitempty"`

	Runtime      RuntimeConfig    `json:"runtime"`
	Service      *ServiceConfig   `json:"service,omitempty"`
	Resources    *ResourceConfig  `json:"resources,omitempty"`
	HealthCheck  *HealthCheckConfig `json:"healthCheck,omitempty"`
	Ingress      *IngressConfig   `json:"ingress,omitempty"`

	Cronjobs []CronjobConfig `json:"cronjobs,omitempty"`
	Addons   []AddonConfig   `json:"addons,omitempty"`
	Sleep    *SleepConfig    `json:"sleep,omitempty"`

	CustomConfig *CustomConfig `json:"customConfig,omitempty"`
}

// AppEnvironmentConfig holds per-environment deployment configuration
type AppEnvironmentConfig struct {
	Name             string                        `json:"name"`
	Replicas         *int32                        `json:"replicas,omitempty"`
	Image            *ImageConfig                  `json:"image,omitempty"`
	Autoscale        *AutoscaleConfig              `json:"autoscale,omitempty"`
	Resources        *ResourceConfig               `json:"resources,omitempty"`
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
}

type GitSource struct {
	Provider         string `json:"provider"`
	Repository       string `json:"repository"`
	Branch           string `json:"branch,omitempty"`
	AutoDeployOnPush bool   `json:"autoDeployOnPush,omitempty"`
}

type BuildConfig struct {
	// +kubebuilder:validation:Enum=runpacks;buildpacks;dockerfile;nixpacks;image
	Strategy   string `json:"strategy"`
	Dockerfile string `json:"dockerfile,omitempty"`
}

type ImageConfig struct {
	Repository       string                        `json:"repository"`
	Tag              string                        `json:"tag,omitempty"`
	PullPolicy       corev1.PullPolicy             `json:"pullPolicy,omitempty"`
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
}

type RuntimeConfig struct {
	Port    int32    `json:"port,omitempty"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`

	Env     []corev1.EnvVar `json:"env,omitempty"`
	Secrets []SecretBinding `json:"secrets,omitempty"`
	Volumes []VolumeMount   `json:"volumes,omitempty"`
}

type SecretBinding struct {
	SecretRef    *SecretRefBinding    `json:"secretRef,omitempty"`
	SecretKeyRef *SecretKeyRefBinding `json:"secretKeyRef,omitempty"`
	SecretMount  *SecretMountBinding  `json:"secretMount,omitempty"`
	Keys         []SecretKeyMapping   `json:"keys,omitempty"`
	// Environments limits this binding to specific environments. Empty means all environments.
	Environments []string `json:"environments,omitempty"`
}

type SecretRefBinding struct {
	Name string `json:"name"`
}

type SecretKeyRefBinding struct {
	Name   string `json:"name"`
	Key    string `json:"key"`
	EnvVar string `json:"envVar"`
}

type SecretMountBinding struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

type SecretKeyMapping struct {
	SecretKey string `json:"secretKey"`
	EnvVar    string `json:"envVar"`
}

type VolumeMount struct {
	Name                  string  `json:"name"`
	MountPath             string  `json:"mountPath"`
	PersistentVolumeClaim *PVCRef `json:"persistentVolumeClaim,omitempty"`
}

type PVCRef struct {
	ClaimName string `json:"claimName"`
	Size      string `json:"size,omitempty"`
}

type ScalingConfig struct {
	Replicas  *int32           `json:"replicas,omitempty"`
	Autoscale *AutoscaleConfig `json:"autoscale,omitempty"`
}

type AutoscaleConfig struct {
	Enabled     bool                                            `json:"enabled"`
	MinReplicas *int32                                          `json:"minReplicas,omitempty"`
	MaxReplicas int32                                           `json:"maxReplicas"`
	Metrics     []MetricSpec                                    `json:"metrics,omitempty"`
	Behavior    *autoscalingv2.HorizontalPodAutoscalerBehavior `json:"behavior,omitempty"`
}

type MetricSpec struct {
	// +kubebuilder:validation:Enum=cpu;memory;custom
	Type                     string `json:"type"`
	Name                     string `json:"name,omitempty"`
	TargetAverageUtilization *int32 `json:"targetAverageUtilization,omitempty"`
	TargetAverageValue       string `json:"targetAverageValue,omitempty"`
}

type ResourceConfig struct {
	Size     string                 `json:"size,omitempty"`
	Requests corev1.ResourceList   `json:"requests,omitempty"`
	Limits   corev1.ResourceList   `json:"limits,omitempty"`
}

type IngressConfig struct {
	Domain        string            `json:"domain"`
	TLS           bool              `json:"tls,omitempty"`
	ClusterIssuer string            `json:"clusterIssuer,omitempty"`
	BasicAuth     bool              `json:"basicAuth,omitempty"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

type HealthCheckConfig struct {
	// +kubebuilder:validation:Enum=http;tcp;exec
	Type    string `json:"type"`
	Path    string `json:"path,omitempty"`
	Port    int32  `json:"port,omitempty"`
	Command string `json:"command,omitempty"`

	InitialDelaySeconds int32 `json:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int32 `json:"periodSeconds,omitempty"`
	TimeoutSeconds      int32 `json:"timeoutSeconds,omitempty"`
	FailureThreshold    int32 `json:"failureThreshold,omitempty"`
	SuccessThreshold    int32 `json:"successThreshold,omitempty"`
}

type CronjobConfig struct {
	Name         string                       `json:"name"`
	Schedule     string                       `json:"schedule"`
	Command      string                       `json:"command"`
	Resources    *ResourceConfig              `json:"resources,omitempty"`
	Environments []CronjobEnvironmentOverride `json:"environments,omitempty"`
}

type CronjobEnvironmentOverride struct {
	Name     string `json:"name"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Schedule string `json:"schedule,omitempty"`
}

type AddonConfig struct {
	Type    string `json:"type"`
	Version string `json:"version,omitempty"`
	Size    string `json:"size,omitempty"`
}

type SleepConfig struct {
	Enabled           bool   `json:"enabled"`
	InactivityTimeout string `json:"inactivityTimeout,omitempty"`
}

type ServiceConfig struct {
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	Type  string        `json:"type,omitempty"`
	Ports []ServicePort `json:"ports,omitempty"`
}

type ServicePort struct {
	Name       string `json:"name"`
	Port       int32  `json:"port"`
	TargetPort int32  `json:"targetPort,omitempty"`
	// +kubebuilder:validation:Enum=TCP;UDP;SCTP
	Protocol string `json:"protocol,omitempty"`
	NodePort int32  `json:"nodePort,omitempty"`
}

type CustomConfig struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`

	// +kubebuilder:pruning:PreserveUnknownFields
	PodSpec *runtime.RawExtension `json:"podSpec,omitempty"`

	// +kubebuilder:pruning:PreserveUnknownFields
	ContainerSpec *runtime.RawExtension `json:"containerSpec,omitempty"`

	// +kubebuilder:pruning:PreserveUnknownFields
	ExtraResources []runtime.RawExtension `json:"extraResources,omitempty"`
}

// --- Status ---

type VestaAppStatus struct {
	// +kubebuilder:validation:Enum=Pending;Building;Deploying;Running;Failed;Sleeping
	Phase       string `json:"phase,omitempty"`
	BuildStatus string `json:"buildStatus,omitempty"`
	URL         string `json:"url,omitempty"`
	CurrentImage string `json:"currentImage,omitempty"`

	LastDeployedAt string `json:"lastDeployedAt,omitempty"`
	LastCommitSHA  string `json:"lastCommitSHA,omitempty"`

	DeploymentHistory []DeploymentRecord `json:"deploymentHistory,omitempty"`

	Scaling    *ScalingStatus     `json:"scaling,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type DeploymentRecord struct {
	Version    int    `json:"version"`
	Image      string `json:"image"`
	CommitSHA  string `json:"commitSHA,omitempty"`
	DeployedAt string `json:"deployedAt"`
	DeployedBy string `json:"deployedBy,omitempty"`
}

type ScalingStatus struct {
	CurrentReplicas  int32 `json:"currentReplicas"`
	DesiredReplicas  int32 `json:"desiredReplicas"`
	AutoscalerActive bool  `json:"autoscalerActive"`
}

// +kubebuilder:object:root=true
type VestaAppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VestaApp `json:"items"`
}

// ============================================================================
// VestaProject
// ============================================================================

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Team",type=string,JSONPath=`.spec.team`
// +kubebuilder:printcolumn:name="Repository",type=string,JSONPath=`.spec.defaultGit.repository`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

type VestaProject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VestaProjectSpec   `json:"spec,omitempty"`
	Status VestaProjectStatus `json:"status,omitempty"`
}

type VestaProjectSpec struct {
	DisplayName      string                        `json:"displayName,omitempty"`
	Team             string                        `json:"team,omitempty"`
	Environments     []ProjectEnvironment          `json:"environments,omitempty"`
	Labels           map[string]string             `json:"labels,omitempty"`
	Annotations      map[string]string             `json:"annotations,omitempty"`
	DefaultGit       *GitSource                    `json:"defaultGit,omitempty"`
	DefaultBuild     *BuildConfig                  `json:"defaultBuild,omitempty"`
	DefaultImage     *ImageConfig                  `json:"defaultImage,omitempty"`
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
	Notifications    *NotificationConfig           `json:"notifications,omitempty"`
}

type ProjectEnvironment struct {
	Name            string `json:"name"`
	DisplayName     string `json:"displayName,omitempty"`
	Branch          string `json:"branch,omitempty"`
	Order           int    `json:"order,omitempty"`
	AutoDeploy      bool   `json:"autoDeploy,omitempty"`
	RequireApproval bool   `json:"requireApproval,omitempty"`
	AutoDeployPRs   bool   `json:"autoDeployPRs,omitempty"`
}

type NotificationConfig struct {
	Slack   *SlackNotification   `json:"slack,omitempty"`
	Discord *DiscordNotification `json:"discord,omitempty"`
	Webhook *WebhookNotification `json:"webhook,omitempty"`
}

type SlackNotification struct {
	WebhookURL string   `json:"webhookUrl"`
	Events     []string `json:"events,omitempty"`
}

type DiscordNotification struct {
	WebhookURL string   `json:"webhookUrl"`
	Events     []string `json:"events,omitempty"`
}

type WebhookNotification struct {
	URL    string   `json:"url"`
	Events []string `json:"events,omitempty"`
}

type VestaProjectStatus struct {
	EnvironmentCount int                `json:"environmentCount,omitempty"`
	AppCount         int                `json:"appCount,omitempty"`
	Conditions       []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
type VestaProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VestaProject `json:"items"`
}

// ============================================================================
// VestaEnvironment
// ============================================================================

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=`.spec.project`
// +kubebuilder:printcolumn:name="Order",type=integer,JSONPath=`.spec.order`
// +kubebuilder:printcolumn:name="Branch",type=string,JSONPath=`.spec.branch`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

type VestaEnvironment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VestaEnvironmentSpec   `json:"spec,omitempty"`
	Status VestaEnvironmentStatus `json:"status,omitempty"`
}

type VestaEnvironmentSpec struct {
	Project         string `json:"project"`
	DisplayName     string `json:"displayName,omitempty"`
	Order           int    `json:"order,omitempty"`
	AutoDeploy      bool   `json:"autoDeploy,omitempty"`
	Branch          string `json:"branch,omitempty"`
	RequireApproval bool   `json:"requireApproval,omitempty"`
	AutoDeployPRs   bool   `json:"autoDeployPRs,omitempty"`
}

type VestaEnvironmentStatus struct {
	AppCount   int                `json:"appCount,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
type VestaEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VestaEnvironment `json:"items"`
}

// ============================================================================
// VestaConfig
// ============================================================================

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster

type VestaConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec VestaConfigSpec `json:"spec,omitempty"`
}

type VestaConfigSpec struct {
	Domain        string                 `json:"domain"`
	ClusterIssuer string                 `json:"clusterIssuer,omitempty"`
	Registry      *RegistryConfig        `json:"registry,omitempty"`

	PodSizeList       []PodSizePreset        `json:"podSizeList,omitempty"`
	AutoscaleDefaults *AutoscaleDefaults     `json:"autoscaleDefaults,omitempty"`
	Buildpacks        []BuildpackConfig      `json:"buildpacks,omitempty"`
	ExternalSecrets   *ExternalSecretsConfig `json:"externalSecrets,omitempty"`
	Auth              *AuthConfig            `json:"auth,omitempty"`
	Templates         *TemplatesConfig       `json:"templates,omitempty"`
	PrometheusURL     string                 `json:"prometheusUrl,omitempty"`
}

type RegistryConfig struct {
	Build                  BuildRegistryConfig          `json:"build,omitempty"`
	GlobalImagePullSecrets []corev1.LocalObjectReference `json:"globalImagePullSecrets,omitempty"`
}

type BuildRegistryConfig struct {
	URL         string         `json:"url"`
	Credentials *CredentialRef `json:"credentials,omitempty"`
}

type CredentialRef struct {
	SecretRef string `json:"secretRef"`
}

type PodSizePreset struct {
	Name     string              `json:"name"`
	Requests corev1.ResourceList `json:"requests,omitempty"`
	Limits   corev1.ResourceList `json:"limits,omitempty"`
}

type AutoscaleDefaults struct {
	MinReplicas  *int32 `json:"minReplicas,omitempty"`
	MaxReplicas  *int32 `json:"maxReplicas,omitempty"`
	TargetCPU    *int32 `json:"targetCPU,omitempty"`
	TargetMemory *int32 `json:"targetMemory,omitempty"`
}

type BuildpackConfig struct {
	Name         string `json:"name"`
	FetchImage   string `json:"fetchImage,omitempty"`
	BuildCommand string `json:"buildCommand,omitempty"`
	RunCommand   string `json:"runCommand,omitempty"`
}

type ExternalSecretsConfig struct {
	Enabled  bool         `json:"enabled"`
	Provider string       `json:"provider,omitempty"`
	Vault    *VaultConfig `json:"vault,omitempty"`
}

type VaultConfig struct {
	Server string     `json:"server"`
	Path   string     `json:"path,omitempty"`
	Auth   *VaultAuth `json:"auth,omitempty"`
}

type VaultAuth struct {
	Method string `json:"method"`
	Role   string `json:"role"`
}

type AuthConfig struct {
	Local     *LocalAuthConfig `json:"local,omitempty"`
	OAuth2    *OAuth2Config    `json:"oauth2,omitempty"`
	APITokens *APITokenConfig `json:"apiTokens,omitempty"`
}

type LocalAuthConfig struct {
	Enabled bool `json:"enabled"`
}

type OAuth2Config struct {
	Enabled   bool             `json:"enabled"`
	Providers []OAuth2Provider `json:"providers,omitempty"`
}

type OAuth2Provider struct {
	Name         string   `json:"name"`
	ClientID     string   `json:"clientId"`
	ClientSecret string   `json:"clientSecret"`
	AllowedOrgs  []string `json:"allowedOrgs,omitempty"`
}

type APITokenConfig struct {
	Enabled          bool   `json:"enabled"`
	MaxTokensPerUser int    `json:"maxTokensPerUser,omitempty"`
	DefaultExpiry    string `json:"defaultExpiry,omitempty"`
}

type TemplatesConfig struct {
	CatalogURL string `json:"catalogUrl,omitempty"`
}

// +kubebuilder:object:root=true
type VestaConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VestaConfig `json:"items"`
}

// ============================================================================
// VestaSecret
// ============================================================================

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Synced",type=boolean,JSONPath=`.status.synced`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

type VestaSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VestaSecretSpec   `json:"spec,omitempty"`
	Status VestaSecretStatus `json:"status,omitempty"`
}

type VestaSecretSpec struct {
	// +kubebuilder:validation:Enum=Opaque;kubernetes.io/dockerconfigjson;kubernetes.io/tls
	Type        string `json:"type"`
	Project     string `json:"project,omitempty"`
	App         string `json:"app,omitempty"`
	Environment string `json:"environment,omitempty"`

	Data         map[string]string   `json:"data,omitempty"`
	DockerConfig *DockerSecretConfig `json:"dockerConfig,omitempty"`
	TLS          *TLSSecretConfig    `json:"tls,omitempty"`

	ExternalSecret *ExternalSecretRef `json:"externalSecret,omitempty"`
}

type DockerSecretConfig struct {
	Registry string `json:"registry"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type TLSSecretConfig struct {
	Cert string `json:"cert"`
	Key  string `json:"key"`
}

type ExternalSecretRef struct {
	Provider string               `json:"provider"`
	Path     string               `json:"path"`
	Keys     []ExternalKeyMapping `json:"keys,omitempty"`
}

type ExternalKeyMapping struct {
	RemoteKey string `json:"remoteKey"`
	LocalKey  string `json:"localKey"`
}

type VestaSecretStatus struct {
	Synced       bool   `json:"synced,omitempty"`
	LastSyncedAt string `json:"lastSyncedAt,omitempty"`
	SecretName   string `json:"secretName,omitempty"`
}

// +kubebuilder:object:root=true
type VestaSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VestaSecret `json:"items"`
}
