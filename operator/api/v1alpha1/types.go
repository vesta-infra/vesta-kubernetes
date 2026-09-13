package v1alpha1

import (
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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

// +kubebuilder:resource:shortName=va;vapp
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

	Runtime     RuntimeConfig      `json:"runtime"`
	Service     *ServiceConfig     `json:"service,omitempty"`
	Resources   *ResourceConfig    `json:"resources,omitempty"`
	HealthCheck *HealthCheckConfig `json:"healthCheck,omitempty"`
	Ingress     *IngressConfig     `json:"ingress,omitempty"`

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
	Ingress          *IngressOverride              `json:"ingress,omitempty"`
	Service          *ServiceOverride              `json:"service,omitempty"`
}

// IngressOverride allows per-environment domain and TLS configuration.
type IngressOverride struct {
	Domain  string   `json:"domain,omitempty"`
	Domains []string `json:"domains,omitempty"`
	TLS     *bool    `json:"tls,omitempty"`

	// ClusterIssuer selects the certificate provider for this environment only, so
	// staging can issue from a staging ACME endpoint while production does not.
	// Empty inherits the app-level issuer.
	ClusterIssuer string `json:"clusterIssuer,omitempty"`

	// TLSSecretName points the Ingress at a certificate the user supplied rather than
	// one cert-manager issues. Empty uses the generated "<app>-<env>-tls" name.
	TLSSecretName string `json:"tlsSecretName,omitempty"`

	// TLSMode is empty for normal issuer resolution. See IngressConfig.TLSMode.
	// +kubebuilder:validation:Enum=manual;custom-annotations
	TLSMode string `json:"tlsMode,omitempty"`

	// Middlewares replaces the app-level list for this environment. It is a pointer so
	// that an empty list is distinguishable from an absent one: nil inherits the
	// app-level middlewares, [] applies none, which is how an environment opts out of a
	// platform-wide allowList without the app having to stop declaring it.
	Middlewares *[]string `json:"middlewares,omitempty"`

	Annotations     map[string]string `json:"annotations,omitempty"`
	RedirectDomains []string          `json:"redirectDomains,omitempty"`
	RedirectTarget  string            `json:"redirectTarget,omitempty"`
}

// ServiceOverride allows per-environment service type and port configuration.
type ServiceOverride struct {
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	Type  string        `json:"type,omitempty"`
	Ports []ServicePort `json:"ports,omitempty"`
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
	// Repository is optional because this struct is reused for per-environment overrides,
	// where setting only a tag and inheriting the repository is the normal case. Without
	// omitempty, controller-gen marks it required and the API server rejects every such
	// override -- which the hand-maintained CRDs never did, so existing VestaApps in the
	// wild depend on it staying optional.
	Repository       string                        `json:"repository,omitempty"`
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
	Enabled     bool                                           `json:"enabled"`
	MinReplicas *int32                                         `json:"minReplicas,omitempty"`
	MaxReplicas int32                                          `json:"maxReplicas"`
	Metrics     []MetricSpec                                   `json:"metrics,omitempty"`
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
	Size     string              `json:"size,omitempty"`
	Requests corev1.ResourceList `json:"requests,omitempty"`
	Limits   corev1.ResourceList `json:"limits,omitempty"`
}

type IngressConfig struct {
	Domain string `json:"domain"`
	TLS    bool   `json:"tls,omitempty"`

	// ClusterIssuer names the cert-manager ClusterIssuer to issue this app's certificate.
	// Empty falls back to VestaConfig.spec.clusterIssuer.
	ClusterIssuer string `json:"clusterIssuer,omitempty"`

	// TLSSecretName points the Ingress at a certificate the user supplied rather than one
	// cert-manager issues. Empty uses the generated "<app>-<env>-tls" name.
	TLSSecretName string `json:"tlsSecretName,omitempty"`

	// TLSMode selects how the certificate is obtained. Empty resolves a ClusterIssuer
	// normally. "manual" uses TLSSecretName and stamps no cert-manager annotation.
	// "custom-annotations" is the escape hatch: the operator stamps nothing at all and
	// whatever the user put in Annotations is left in sole charge — which is what makes it
	// safe for an explicit issuer selection to win over a stale annotation everywhere else.
	// +kubebuilder:validation:Enum=manual;custom-annotations
	TLSMode string `json:"tlsMode,omitempty"`

	IngressClassName string `json:"ingressClassName,omitempty"`

	// BasicAuth is a no-op and has never been read by the operator. Use a VestaMiddleware
	// of type "basicAuth" and list it in Middlewares instead. The field remains so that
	// upgrading does not prune it from resources that already set it.
	// Deprecated: superseded by VestaMiddleware.
	BasicAuth bool `json:"basicAuth,omitempty"`

	// HTTPSRedirect overrides VestaConfig.spec.httpsRedirect for this app. Empty inherits.
	// +kubebuilder:validation:Enum=middleware;none
	HTTPSRedirect string `json:"httpsRedirect,omitempty"`

	// Middlewares names VestaMiddleware resources to apply to every environment's ingress,
	// in order. Traefik applies middlewares in the order listed and the order is
	// semantic -- an allowList before an auth check rejects strangers without prompting
	// for a password, the reverse prompts first -- so this list is ordered, not a set.
	Middlewares []string `json:"middlewares,omitempty"`

	Annotations     map[string]string `json:"annotations,omitempty"`
	RedirectDomains []string          `json:"redirectDomains,omitempty"`
	RedirectTarget  string            `json:"redirectTarget,omitempty"`
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
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	// Enabled defaults to true. When false the CronJob is kept in the cluster
	// but suspended, so it stops firing without losing its history.
	Enabled       *bool                        `json:"enabled,omitempty"`
	Resources     *ResourceConfig              `json:"resources,omitempty"`
	RestartPolicy string                       `json:"restartPolicy,omitempty"`
	BackoffLimit  *int32                       `json:"backoffLimit,omitempty"`
	Environments  []CronjobEnvironmentOverride `json:"environments,omitempty"`
}

type CronjobEnvironmentOverride struct {
	Name string `json:"name"`
	// Enabled overrides the cronjob-level enabled flag for this environment.
	// When false the CronJob is suspended in this environment only.
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
	// +kubebuilder:validation:Enum=Pending;Building;Deploying;Running;Degraded;Failed;Sleeping;CrashLoopBackOff
	Phase string `json:"phase,omitempty"`

	// Reason is a short CamelCase token naming why the app is in its current
	// phase (ImagePullBackOff, CrashLoopBackOff, Unschedulable, InvalidSpec, ...).
	// Empty while the app is healthy.
	Reason string `json:"reason,omitempty"`

	// Message is a human-readable explanation of Reason, including the specific
	// pod, container, and underlying error where known. Without this a failed app
	// showed only "Failed" and the cause lived in operator logs.
	Message string `json:"message,omitempty"`

	BuildStatus  string `json:"buildStatus,omitempty"`
	URL          string `json:"url,omitempty"`
	CurrentImage string `json:"currentImage,omitempty"`

	LastDeployedAt string `json:"lastDeployedAt,omitempty"`
	LastCommitSHA  string `json:"lastCommitSHA,omitempty"`

	DeploymentHistory []DeploymentRecord `json:"deploymentHistory,omitempty"`

	Scaling    *ScalingStatus     `json:"scaling,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type DeploymentRecord struct {
	Version     int    `json:"version"`
	Image       string `json:"image"`
	Environment string `json:"environment,omitempty"`
	CommitSHA   string `json:"commitSHA,omitempty"`
	DeployedAt  string `json:"deployedAt"`
	DeployedBy  string `json:"deployedBy,omitempty"`
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

// +kubebuilder:resource:shortName=vprj;vproj
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

// +kubebuilder:resource:shortName=ve;venv
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
// +kubebuilder:resource:scope=Cluster,shortName=vc;vcfg

type VestaConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec VestaConfigSpec `json:"spec,omitempty"`
}

type VestaConfigSpec struct {
	Domain           string          `json:"domain"`
	DomainTemplate   string          `json:"domainTemplate,omitempty"`
	ClusterIssuer    string          `json:"clusterIssuer,omitempty"`
	IngressClassName string          `json:"ingressClassName,omitempty"`
	Registry         *RegistryConfig `json:"registry,omitempty"`

	PodSizeList       []PodSizePreset        `json:"podSizeList,omitempty"`
	AutoscaleDefaults *AutoscaleDefaults     `json:"autoscaleDefaults,omitempty"`
	Buildpacks        []BuildpackConfig      `json:"buildpacks,omitempty"`
	ExternalSecrets   *ExternalSecretsConfig `json:"externalSecrets,omitempty"`
	Auth              *AuthConfig            `json:"auth,omitempty"`
	Templates         *TemplatesConfig       `json:"templates,omitempty"`
	PrometheusURL     string                 `json:"prometheusUrl,omitempty"`

	// HTTPSRedirect decides how an app's HTTP traffic is redirected to HTTPS.
	//
	//   "middleware" (default) stamps a Traefik redirectScheme Middleware per app and
	//     references it from the Ingress annotation.
	//   "none" stamps nothing, for clusters whose ingress controller already redirects at
	//     the entrypoint -- the standard Traefik chart does this with
	//     entryPoints.web.http.redirections, which runs before any router or middleware is
	//     consulted, making the per-app middleware pure redundancy.
	//
	// The distinction matters beyond tidiness: Traefik drops an entire router when a
	// referenced middleware cannot be resolved, so a redundant middleware is not a no-op,
	// it is an extra way for the route to fail.
	// +kubebuilder:validation:Enum=middleware;none
	HTTPSRedirect string `json:"httpsRedirect,omitempty"`
}

type RegistryConfig struct {
	Build                  BuildRegistryConfig           `json:"build,omitempty"`
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
	APITokens *APITokenConfig  `json:"apiTokens,omitempty"`
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

// +kubebuilder:resource:shortName=vs;vsec
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

	// Data keys become keys of the generated Kubernetes Secret and must satisfy the same
	// rules the API server enforces there. Checking length here turns the common mistake —
	// a value pasted into the key field — into a rejected write instead of a VestaSecret
	// the operator can never sync. The character-set rule is deliberately not a CEL rule:
	// matches() over an unbounded map inflates the estimated cost and can get the whole
	// CRD rejected at install time. That check lives in the API handlers and the operator.
	// +kubebuilder:validation:XValidation:rule="self.all(k, size(k) <= 253)",message="secret key must be no more than 253 characters (a key this long is usually a value entered in the key field)"
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

	// InvalidKeys lists data keys the operator skipped because Kubernetes cannot store
	// them. Entries are truncated — an over-long key is usually a pasted secret value.
	InvalidKeys []string `json:"invalidKeys,omitempty"`
}

// +kubebuilder:object:root=true
type VestaSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VestaSecret `json:"items"`
}

// ============================================================================
// VestaMiddleware
// ============================================================================

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Applied",type=integer,JSONPath=`.status.appliedCount`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// +kubebuilder:resource:shortName=vmw;vmid
type VestaMiddleware struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VestaMiddlewareSpec   `json:"spec,omitempty"`
	Status VestaMiddlewareStatus `json:"status,omitempty"`
}

// VestaMiddlewareSpec describes one reusable ingress middleware. Exactly one of the typed
// config fields must be set, and it must be the one Type names -- the operator refuses to
// guess, because a middleware that silently does nothing is worse than one that reports
// itself broken.
//
// Every field below carries omitempty deliberately. controller-gen marks any field without
// it as required in the generated schema, and a newly-required field rejects resources an
// earlier release accepted; that is the 0.7.1 defect, and hack/check-crd-compat.sh exists
// to catch its recurrence.
type VestaMiddlewareSpec struct {
	// +kubebuilder:validation:Enum=rateLimit;basicAuth;ipAllowList;headers;stripPrefix;compress;retry;circuitBreaker;buffering;raw
	Type string `json:"type"`

	// DisplayName is shown in the UI. The resource name stays the stable identifier that
	// apps reference, so renaming for presentation cannot break an attachment.
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`

	// Project, App and Environment narrow who may reference this middleware. They do not
	// auto-attach it: attachment is always an explicit, ordered list on the app, because
	// the order middlewares run in changes what they do.
	Project     string `json:"project,omitempty"`
	App         string `json:"app,omitempty"`
	Environment string `json:"environment,omitempty"`

	RateLimit      *RateLimitMiddleware      `json:"rateLimit,omitempty"`
	BasicAuth      *BasicAuthMiddleware      `json:"basicAuth,omitempty"`
	IPAllowList    *IPAllowListMiddleware    `json:"ipAllowList,omitempty"`
	Headers        *HeadersMiddleware        `json:"headers,omitempty"`
	StripPrefix    *StripPrefixMiddleware    `json:"stripPrefix,omitempty"`
	Compress       *CompressMiddleware       `json:"compress,omitempty"`
	Retry          *RetryMiddleware          `json:"retry,omitempty"`
	CircuitBreaker *CircuitBreakerMiddleware `json:"circuitBreaker,omitempty"`
	Buffering      *BufferingMiddleware      `json:"buffering,omitempty"`

	// Raw is the escape hatch: its contents become the Traefik Middleware spec verbatim,
	// for plugins and middleware types Vesta ships no form for. Vesta validates only that
	// it is a JSON object with exactly one key; what that key means is Traefik's business.
	// +kubebuilder:pruning:PreserveUnknownFields
	Raw *apiextensionsv1.JSON `json:"raw,omitempty"`
}

// RateLimitMiddleware limits requests per source over a sliding period.
type RateLimitMiddleware struct {
	// Average is the sustained requests per Period allowed from one source.
	Average int64 `json:"average,omitempty"`
	// Burst is how far above Average a short spike may go before requests are rejected.
	Burst int64 `json:"burst,omitempty"`
	// Period is a Go duration such as "1s" or "1m". Empty means Traefik's default of 1s.
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$`
	Period string `json:"period,omitempty"`

	// SourceCriterion decides what counts as one client. Empty uses the request's remote
	// address, which behind a load balancer is the balancer -- set requestHeaderName to
	// X-Forwarded-For there, or every client shares a single bucket.
	SourceCriterion *RateLimitSourceCriterion `json:"sourceCriterion,omitempty"`
}

type RateLimitSourceCriterion struct {
	RequestHeaderName string `json:"requestHeaderName,omitempty"`
	RequestHost       bool   `json:"requestHost,omitempty"`
	// IPDepth is the position from the right of X-Forwarded-For to trust. 0 disables it.
	IPDepth int `json:"ipDepth,omitempty"`
}

// BasicAuthMiddleware guards a route with HTTP basic auth. Credentials are never stored
// in this resource: SecretName points at a Kubernetes Secret holding htpasswd-format
// users, because a CRD is readable by anyone holding get on the type.
type BasicAuthMiddleware struct {
	// SecretName is a Secret holding htpasswd lines under the key "users".
	SecretName string `json:"secretName,omitempty"`
	SecretKey  string `json:"secretKey,omitempty"`
	Realm      string `json:"realm,omitempty"`

	// ManagedSecret marks SecretName as a Secret Vesta owns, built by hashing credentials
	// entered through the API and held in vesta-system. The operator copies it into every
	// namespace this middleware is projected into, because Traefik resolves a basicAuth
	// secret in the Middleware's own namespace and a shared middleware has many.
	//
	// A Secret the user created is left alone: it is expected to exist already in the app
	// namespace, and copying over it would overwrite credentials Vesta did not issue.
	ManagedSecret bool `json:"managedSecret,omitempty"`

	// Users lists the usernames held in the Secret. Passwords are bcrypt-hashed before
	// they leave the API and are never stored here -- this is only so the UI can show who
	// has access without decrypting anything.
	Users []string `json:"users,omitempty"`
	// RemoveHeader drops the Authorization header before proxying to the app.
	RemoveHeader bool `json:"removeHeader,omitempty"`
}

// IPAllowListMiddleware rejects requests from outside the listed CIDRs.
type IPAllowListMiddleware struct {
	// SourceRange holds CIDRs or bare addresses, e.g. "10.0.0.0/8", "203.0.113.7".
	SourceRange []string `json:"sourceRange,omitempty"`
	// IPStrategy decides which address in X-Forwarded-For to test. Without it the check
	// runs against the direct peer, which behind a load balancer allows everyone or no one.
	IPStrategy *IPStrategy `json:"ipStrategy,omitempty"`
}

type IPStrategy struct {
	Depth       int      `json:"depth,omitempty"`
	ExcludedIPs []string `json:"excludedIPs,omitempty"`
}

// HeadersMiddleware sets request and response headers, including CORS.
type HeadersMiddleware struct {
	CustomRequestHeaders  map[string]string `json:"customRequestHeaders,omitempty"`
	CustomResponseHeaders map[string]string `json:"customResponseHeaders,omitempty"`

	AccessControlAllowMethods     []string `json:"accessControlAllowMethods,omitempty"`
	AccessControlAllowHeaders     []string `json:"accessControlAllowHeaders,omitempty"`
	AccessControlAllowOriginList  []string `json:"accessControlAllowOriginList,omitempty"`
	AccessControlAllowCredentials bool     `json:"accessControlAllowCredentials,omitempty"`
	AccessControlExposeHeaders    []string `json:"accessControlExposeHeaders,omitempty"`
	AccessControlMaxAge           int64    `json:"accessControlMaxAge,omitempty"`
	AddVaryHeader                 bool     `json:"addVaryHeader,omitempty"`

	FrameDeny               bool   `json:"frameDeny,omitempty"`
	ContentTypeNosniff      bool   `json:"contentTypeNosniff,omitempty"`
	BrowserXSSFilter        bool   `json:"browserXssFilter,omitempty"`
	ContentSecurityPolicy   string `json:"contentSecurityPolicy,omitempty"`
	ReferrerPolicy          string `json:"referrerPolicy,omitempty"`
	StsSeconds              int64  `json:"stsSeconds,omitempty"`
	StsIncludeSubdomains    bool   `json:"stsIncludeSubdomains,omitempty"`
	StsPreload              bool   `json:"stsPreload,omitempty"`
	ForceSTSHeader          bool   `json:"forceSTSHeader,omitempty"`
	CustomFrameOptionsValue string `json:"customFrameOptionsValue,omitempty"`
}

// StripPrefixMiddleware removes path prefixes before the request reaches the app.
type StripPrefixMiddleware struct {
	Prefixes   []string `json:"prefixes,omitempty"`
	ForceSlash bool     `json:"forceSlash,omitempty"`
}

// CompressMiddleware gzip/brotli-compresses responses.
type CompressMiddleware struct {
	ExcludedContentTypes []string `json:"excludedContentTypes,omitempty"`
	// MinResponseBodyBytes skips compression for bodies smaller than this, where the
	// CPU cost outweighs the saving.
	MinResponseBodyBytes int64 `json:"minResponseBodyBytes,omitempty"`
}

// RetryMiddleware retries a request when the backend closes the connection before
// responding. It never retries a response that arrived, so a 500 is not retried.
type RetryMiddleware struct {
	Attempts int64 `json:"attempts,omitempty"`
	// InitialInterval is a Go duration; the wait doubles between attempts.
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$`
	InitialInterval string `json:"initialInterval,omitempty"`
}

// CircuitBreakerMiddleware stops sending traffic to a failing backend.
type CircuitBreakerMiddleware struct {
	// Expression is Traefik's circuit-breaker expression, e.g.
	// "NetworkErrorRatio() > 0.5" or "ResponseCodeRatio(500, 600, 0, 600) > 0.25".
	Expression string `json:"expression,omitempty"`
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$`
	CheckPeriod string `json:"checkPeriod,omitempty"`
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$`
	FallbackDuration string `json:"fallbackDuration,omitempty"`
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$`
	RecoveryDuration string `json:"recoveryDuration,omitempty"`
}

// BufferingMiddleware caps request and response body sizes, and is what most people
// reach for when they want the Traefik equivalent of nginx's client_max_body_size.
type BufferingMiddleware struct {
	MaxRequestBodyBytes  int64  `json:"maxRequestBodyBytes,omitempty"`
	MemRequestBodyBytes  int64  `json:"memRequestBodyBytes,omitempty"`
	MaxResponseBodyBytes int64  `json:"maxResponseBodyBytes,omitempty"`
	MemResponseBodyBytes int64  `json:"memResponseBodyBytes,omitempty"`
	RetryExpression      string `json:"retryExpression,omitempty"`
}

type VestaMiddlewareStatus struct {
	// Ready is false whenever the operator could not project this middleware anywhere it
	// was asked to -- an unsupported ingress class, a missing Traefik CRD, or a spec that
	// does not match its Type.
	Ready bool `json:"ready,omitempty"`
	// Reason explains a false Ready in terms the UI can show without interpretation.
	Reason string `json:"reason,omitempty"`

	// AppliedNamespaces lists namespaces currently holding a projection of this
	// middleware. It is what the GC pass diffs against, and what the UI counts.
	AppliedNamespaces  []string `json:"appliedNamespaces,omitempty"`
	AppliedCount       int      `json:"appliedCount,omitempty"`
	ObservedGeneration int64    `json:"observedGeneration,omitempty"`
	LastSyncedAt       string   `json:"lastSyncedAt,omitempty"`
}

// +kubebuilder:object:root=true
type VestaMiddlewareList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VestaMiddleware `json:"items"`
}
