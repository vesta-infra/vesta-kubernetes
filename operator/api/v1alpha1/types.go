package v1alpha1

import (
	"strings"

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

	// SecurityProfile overrides the platform default for this app. Empty inherits it.
	//
	// The override exists because "restricted" is not a setting an instance can safely turn
	// on for everything: one image that writes to its own filesystem would otherwise force
	// the whole instance back down to the weakest profile.
	// +kubebuilder:validation:Enum=legacy;baseline;restricted
	SecurityProfile string `json:"securityProfile,omitempty"`

	// DesiredState is what the operator should be driving the app toward. Status reports
	// what it actually is; this says what it ought to be.
	//
	// The distinction is load-bearing. Sleep and stop used to be expressed by writing
	// status.phase, which cannot work: VestaApp has a status subresource, so a patch to
	// the main resource drops the status stanza silently. Sleep then deadlocked -- the
	// operator zeroed replicas only once the phase was "Sleeping", while the phase became
	// "Sleeping" only once replicas were already zero -- and stop was a no-op outright.
	//
	// Empty means "running", so an app that predates this field keeps its behaviour.
	// +kubebuilder:validation:Enum=running;sleeping;stopped
	DesiredState string `json:"desiredState,omitempty"`

	CustomConfig *CustomConfig `json:"customConfig,omitempty"`
}

// DesiredState values. Empty is equivalent to DesiredStateRunning.
const (
	DesiredStateRunning  = "running"
	DesiredStateSleeping = "sleeping"
	DesiredStateStopped  = "stopped"
)

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

	// Host is the git server, for self-managed GitLab and Bitbucket Data Center. Empty
	// means the provider's SaaS host. It is part of a repository's identity: the same
	// path can exist on gitlab.com and on gitlab.internal and they are not the same
	// repository.
	Host string `json:"host,omitempty"`

	// ConnectionID names the git connection that serves this repository, set when the
	// repository is chosen through the UI. Empty is resolved by matching provider, host
	// and path against the configured connections, which is what keeps apps written
	// before connections existed working.
	ConnectionID string `json:"connectionId,omitempty"`

	// TokenSecret names a Secret in vesta-system holding a "token" key, used instead of a
	// connection's credentials.
	//
	// This field is new here but not new to the codebase: the API has been reading it and
	// the UI collecting it since before it existed on the type. Because the CRD is a
	// structural schema with no preserved unknown fields, the API server pruned it on
	// every write, so the read could never succeed and the input was decorative. Same for
	// commitSHA, which is not restored here -- it was observed state and belongs in
	// status.lastCommitSHA, which already exists.
	TokenSecret string `json:"tokenSecret,omitempty"`
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
	// Enabled marks the app as eligible for scale-to-zero. It is a capability, not an
	// instruction: spec.desiredState is what actually holds an app at zero.
	//
	// The distinction is load-bearing on upgrade. Scale-to-zero did nothing for a long
	// time -- the API wrote status.phase, which a status subresource discards -- so any
	// app whose Sleep button was ever pressed carries enabled=true and has been running
	// normally ever since. Redefining this field as "sleep me when idle" would put every
	// one of them to sleep on upgrade, which is why AutoSleep exists separately.
	Enabled           bool   `json:"enabled"`
	InactivityTimeout string `json:"inactivityTimeout,omitempty"`

	// AutoSleep arms the inactivity sweeper for this app.
	//
	// A pointer so absent is distinguishable from false. Absent means off, which is what
	// every app upgrading from a release where none of this worked must get.
	AutoSleep *bool `json:"autoSleep,omitempty"`

	// WakeOnTraffic routes a sleeping app's ingress through the activator, so the next
	// request starts it again. Absent means off: without it a sleeping app simply stays
	// down until somebody wakes it, which is worse than never sleeping.
	WakeOnTraffic *bool `json:"wakeOnTraffic,omitempty"`

	// MinAwake is the floor between waking and being eligible to sleep again. Without one
	// the request that woke the app is the only traffic in the window, so it sleeps again
	// immediately and flaps.
	MinAwake string `json:"minAwake,omitempty"`

	// NoWakePaths are request paths the activator answers itself while the app is asleep,
	// instead of starting it.
	//
	// This is what makes scale-to-zero survive monitoring. An uptime check polling the
	// public URL every minute would otherwise wake the app, and its own traffic would then
	// keep the request rate above zero -- so the app would never sleep again, and the
	// feature would appear simply not to work.
	//
	// Only consulted while the app is down. Once it is running these are proxied through
	// like anything else, so a health check against a live app still reports on the app.
	//
	// A trailing * matches a prefix. Choose carefully: a path listed here is one the app
	// will never be woken for, so listing "/" disables wake-on-traffic entirely.
	NoWakePaths []string `json:"noWakePaths,omitempty"`
}

// AutoSleepEnabled reports whether the inactivity sweeper should consider this app.
//
// Both flags are required: eligibility alone is not consent, for the upgrade reason above.
func (s *SleepConfig) AutoSleepEnabled() bool {
	if s == nil || !s.Enabled || s.AutoSleep == nil {
		return false
	}
	return *s.AutoSleep
}

// DefaultNoWakePaths are answered by the activator instead of waking a sleeping app.
//
// There has to be a default, or scale-to-zero does not survive contact with monitoring. An
// uptime check or an external load balancer polling the public URL every thirty seconds
// wakes the app every thirty seconds, so it never stays down and the feature saves nothing --
// and the cause is invisible, because from the outside the app simply looks busy.
//
// Deliberately only paths that are overwhelmingly health probes. "/status" and "/ping" are
// left out despite being common: both are real application endpoints often enough that
// answering them from the activator would be worse than the problem being solved -- a request
// the app should have served returns a stub instead.
var DefaultNoWakePaths = []string{
	"/healthz", "/readyz", "/livez",
	"/health", "/healthcheck",
	"/-/healthy", "/-/ready",
}

// NoWakePathList renders the no-wake paths for the Ingress annotation the activator reads.
//
// An unset list gets the defaults above. Configuring one REPLACES them rather than adding to
// them, so an app that needs a different set is not stuck with these as well.
func (s *SleepConfig) NoWakePathList() string {
	if s == nil {
		return ""
	}
	if len(s.NoWakePaths) == 0 {
		return strings.Join(DefaultNoWakePaths, ",")
	}
	return strings.Join(s.NoWakePaths, ",")
}

// WakeOnTrafficEnabled reports whether a sleeping app's ingress should route to the
// activator.
func (s *SleepConfig) WakeOnTrafficEnabled() bool {
	if s == nil || !s.Enabled || s.WakeOnTraffic == nil {
		return false
	}
	return *s.WakeOnTraffic
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
	// Stopped joins the list because spec.desiredState can now hold an app at zero
	// replicas indefinitely. Earlier releases wrote "Stopped" here from the API without
	// it ever being a legal value -- the write was dropped by the status subresource, so
	// nothing rejected it and nothing honoured it either.
	// +kubebuilder:validation:Enum=Pending;Building;Deploying;Running;Degraded;Failed;Sleeping;Stopped;CrashLoopBackOff
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

	// SleepReason says why the app is or is not scaled to zero.
	//
	// Written by the inactivity sweeper on every pass, including when it decides to do
	// nothing. Without it the feature is invisible while idle: somebody who turned on
	// auto-sleep and sees the app still running cannot tell whether it is busy, whether
	// Prometheus is missing, or whether Vesta is simply not looking.
	SleepReason string `json:"sleepReason,omitempty"`
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

	// Quota applies to every environment of this project unless one narrows it.
	Quota *QuotaSpec `json:"quota,omitempty"`

	// NetworkIsolation walls this project's environments off from everything outside them.
	//
	// Set here rather than only platform-wide because isolation is usually wanted for the
	// one project holding something sensitive, and turning it on for the whole instance to
	// get that is a much larger change than the need justifies.
	//
	// The environments of an isolated project do NOT reach each other: each namespace is
	// isolated from everything outside itself, so staging cannot reach production's
	// database any more than another project can.
	NetworkIsolation *NetworkIsolationConfig `json:"networkIsolation,omitempty"`
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

// CostConfig prices the units a workload reserves.
//
// Either give the per-unit rates, or give what a node costs and let Vesta split it. The
// second is preferred and wins when both are present: an administrator knows what a machine
// costs and does not know what a vCPU-hour is worth, and asking the answerable question is
// the difference between a figure somebody trusts and one they ignore.
type CostConfig struct {
	NodeMonthlyCost float64 `json:"nodeMonthlyCost,omitempty"`
	NodeVCPUs       float64 `json:"nodeVCPUs,omitempty"`
	NodeMemoryGiB   float64 `json:"nodeMemoryGiB,omitempty"`

	CPUCoreHour     float64 `json:"cpuCoreHour,omitempty"`
	MemoryGiBHour   float64 `json:"memoryGiBHour,omitempty"`
	StorageGiBMonth float64 `json:"storageGiBMonth,omitempty"`

	Currency string `json:"currency,omitempty"`
}

// QuotaSpec bounds what an environment or project may consume.
type QuotaSpec struct {
	// Enforce is a pointer so absent is distinguishable from false. Absent means
	// report-only: the controller creates no ResourceQuota at all and only computes what
	// one would do.
	//
	// That default is the whole safety story. A ResourceQuota is not applied retroactively
	// to pods that already exist -- it blocks the NEXT admission, which means the failure
	// lands in the middle of somebody's deploy rather than when the quota was set. Working
	// out the answer first, and showing it, turns that into a decision.
	Enforce *bool `json:"enforce,omitempty"`

	RequestsCPU    string `json:"requestsCpu,omitempty"`
	RequestsMemory string `json:"requestsMemory,omitempty"`

	// Limits are opt-in and sharper than they look: a limits.* quota makes every pod
	// without that limit set unadmittable, and most pods do not set one.
	LimitsCPU    string `json:"limitsCpu,omitempty"`
	LimitsMemory string `json:"limitsMemory,omitempty"`

	StorageTotal string `json:"storageTotal,omitempty"`

	MaxPods        *int32 `json:"maxPods,omitempty"`
	MaxDeployments *int32 `json:"maxDeployments,omitempty"`
	MaxPVCs        *int32 `json:"maxPvcs,omitempty"`
	MaxServices    *int32 `json:"maxServices,omitempty"`

	// Defaults for the LimitRange that ships alongside the quota. Once a quota names
	// requests.cpu, Kubernetes requires every new pod to set one; these are what pods that
	// do not get.
	DefaultRequestCPU    string `json:"defaultRequestCpu,omitempty"`
	DefaultRequestMemory string `json:"defaultRequestMemory,omitempty"`
	DefaultLimitCPU      string `json:"defaultLimitCpu,omitempty"`
	DefaultLimitMemory   string `json:"defaultLimitMemory,omitempty"`
}

// Enforced reports whether a quota should actually be applied.
func (q *QuotaSpec) Enforced() bool {
	return q != nil && q.Enforce != nil && *q.Enforce
}

// QuotaStatus reports what a quota is doing, or would do.
type QuotaStatus struct {
	// Enforced is whether a ResourceQuota object exists.
	Enforced bool `json:"enforced,omitempty"`
	// Committed is what the environment's apps add up to at their configured maximum --
	// autoscaling ceilings included, because a quota that fits today's replicas and not
	// tomorrow's blocks a scale-up with no warning.
	Committed map[string]string `json:"committed,omitempty"`
	// Used is what is actually running now.
	Used map[string]string `json:"used,omitempty"`
	// WouldExceed is set when the configured quota is below what is already committed.
	// Applying it then would refuse the next deploy, so it is reported instead.
	WouldExceed bool   `json:"wouldExceed,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// SecurityConfig is the platform-wide hardening posture.
type SecurityConfig struct {
	// Profile is the default pod hardening for every app that does not name its own.
	//
	//   "legacy" (the default) sets no security context, which is what every app running
	//     today already has. Changing this default would alter the behaviour of running
	//     workloads on upgrade, so it does not change.
	//   "baseline" forbids privilege escalation, drops all capabilities and applies the
	//     runtime's default seccomp filter. Safe to turn on across an instance.
	//   "restricted" adds a non-root user and a read-only root filesystem. Not safe to turn
	//     on blindly -- images that write to their own filesystem will break -- so it is
	//     best set per app.
	// +kubebuilder:validation:Enum=legacy;baseline;restricted
	Profile string `json:"profile,omitempty"`

	// DefaultSecretScope is the scope given to a secret created without one.
	//
	// "global" is the default and the backwards-compatible answer. Setting "project" makes
	// new secrets private to their project; it does NOT reclassify existing ones, because
	// silently narrowing access to a credential an app already pulls with would break that
	// app's deploys with nothing to point at.
	// +kubebuilder:validation:Enum=global;project
	DefaultSecretScope string `json:"defaultSecretScope,omitempty"`

	// NetworkIsolation keeps environments from reaching each other.
	NetworkIsolation *NetworkIsolationConfig `json:"networkIsolation,omitempty"`
}

// NetworkIsolationConfig configures per-namespace NetworkPolicy.
//
// NetworkPolicy is enforced by the cluster's network plugin, not by Kubernetes. A cluster
// running a plugin that does not implement it accepts every policy and enforces none, so
// enabling this is not on its own evidence that anything is isolated -- the operator reports
// what it found in the environment's status.
type NetworkIsolationConfig struct {
	// Enabled is a pointer so that a project turning isolation OFF is distinguishable from
	// a project that says nothing about it. With a plain bool the two are the same value
	// and the platform default would always win, which makes an exemption impossible to
	// express. Same reason QuotaSpec.Enforce is a pointer.
	Enabled *bool `json:"enabled,omitempty"`

	// TrustedNamespaces may reach apps. Left empty, a default list covering the usual
	// ingress-controller and monitoring namespaces is used; setting it REPLACES that list
	// rather than adding to it.
	TrustedNamespaces []string `json:"trustedNamespaces,omitempty"`

	// TrustedNamespaceLabels is for clusters that label namespaces by purpose rather than
	// naming them predictably.
	TrustedNamespaceLabels map[string]string `json:"trustedNamespaceLabels,omitempty"`

	// MetricsPort, when set, stays reachable from anywhere. A scraper that cannot be located
	// by namespace is common, and a closed metrics port fails silently -- the dashboard just
	// goes blank.
	MetricsPort int32 `json:"metricsPort,omitempty"`
}

type VestaEnvironmentSpec struct {
	Project         string `json:"project"`
	DisplayName     string `json:"displayName,omitempty"`
	Order           int    `json:"order,omitempty"`
	AutoDeploy      bool   `json:"autoDeploy,omitempty"`
	Branch          string `json:"branch,omitempty"`
	RequireApproval bool   `json:"requireApproval,omitempty"`
	AutoDeployPRs   bool   `json:"autoDeployPRs,omitempty"`

	// Quota bounds what this environment may consume. Empty inherits the project's, then
	// the platform default.
	Quota *QuotaSpec `json:"quota,omitempty"`

	// NetworkIsolation for this environment alone. Empty inherits the project's, then the
	// platform default.
	NetworkIsolation *NetworkIsolationConfig `json:"networkIsolation,omitempty"`
}

// NetworkIsolationStatus reports what isolation is actually doing.
//
// Enabled and Enforced are separate on purpose. NetworkPolicy is enforced by the cluster's
// network plugin, not by Kubernetes, so a cluster running one that does not implement it
// accepts every policy and filters nothing -- and the objects existing is not evidence that
// anything is isolated.
type NetworkIsolationStatus struct {
	Enabled     bool  `json:"enabled,omitempty"`
	PolicyCount int32 `json:"policyCount,omitempty"`
	// Enforced is whether this cluster's CNI is believed to implement NetworkPolicy.
	Enforced bool `json:"enforced,omitempty"`
	// EnforcementKnown distinguishes "we determined it is not enforced" from "we could not
	// tell". Reporting an unknown as "not enforced" would cry wolf on every unrecognised
	// plugin; reporting it as enforced would be the dangerous direction.
	EnforcementKnown bool        `json:"enforcementKnown,omitempty"`
	Note             string      `json:"note,omitempty"`
	TrustedFrom      []string    `json:"trustedFrom,omitempty"`
	LastApplied      metav1.Time `json:"lastApplied,omitempty"`
}

type VestaEnvironmentStatus struct {
	AppCount   int                `json:"appCount,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Quota reports what the environment is consuming and whether a quota is in force.
	Quota *QuotaStatus `json:"quota,omitempty"`

	// NetworkIsolation reports whether the environment is actually isolated, which is not
	// the same question as whether isolation was turned on.
	NetworkIsolation *NetworkIsolationStatus `json:"networkIsolation,omitempty"`
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

	// Cost prices what workloads reserve. Without it Vesta uses a documented default
	// derived from one commodity node, which is an estimate and says so.
	Cost *CostConfig `json:"cost,omitempty"`

	// Security hardens what apps run as, and whether environments can reach each other.
	// Absent, nothing changes: the default profile sets no security context at all, which
	// is what every existing app already runs with.
	Security *SecurityConfig `json:"security,omitempty"`

	// QuotaDefaults apply to every environment that does not set its own. Report-only
	// unless enforce is set, so configuring one here does not silently start refusing
	// deploys across the whole instance.
	QuotaDefaults *QuotaSpec `json:"quotaDefaults,omitempty"`

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
	Type string `json:"type"`

	// Scope decides who may see and use this secret.
	//
	//   "global" (the default) -- any caller the route already admits. Every registry
	//     credential created before this field existed is global, which is why the empty
	//     value has to keep meaning global: anything else would hide working credentials
	//     from the apps and people already using them.
	//   "project" -- only callers with access to Project below.
	//
	// The platform default for NEW secrets is settable at
	// VestaConfig.spec.security.defaultSecretScope, so an instance can make project
	// scoping the norm without reclassifying what already exists.
	// +kubebuilder:validation:Enum=global;project
	Scope string `json:"scope,omitempty"`

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

	// Password is the credential in plain text, and it is deprecated.
	//
	// A VestaSecret is an ordinary namespaced object: anyone with `get vestasecrets` can
	// read it, and it goes into project export bundles as written. Storing a registry
	// password here contradicted the reasoning already applied to basicAuth and
	// DrainSecretRef, both of which keep their credentials in a Kubernetes Secret.
	//
	// Kept readable so credentials written before PasswordSecretRef existed keep working.
	// The operator migrates them: it copies the value into a Secret, points
	// PasswordSecretRef at it, and only then clears this field.
	Password string `json:"password,omitempty"`

	// PasswordSecretRef points at a Kubernetes Secret holding the password. When set it
	// wins over Password, which is what makes the migration a one-way door rather than a
	// state both fields have to be kept consistent in.
	PasswordSecretRef *DrainSecretRef `json:"passwordSecretRef,omitempty"`

	// Flavor names the registry's API dialect, for listing repositories. Tag listing is
	// the same everywhere; enumerating what exists is not, and Docker Hub in particular
	// does not implement the standard catalog endpoint at all.
	//
	// Empty is detected from the host, which is a guess -- Harbor answers on any hostname
	// -- so this overrides it.
	// +kubebuilder:validation:Enum=generic-v2;harbor;dockerhub;ghcr
	Flavor string `json:"flavor,omitempty"`
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

	FrameDeny             bool   `json:"frameDeny,omitempty"`
	ContentTypeNosniff    bool   `json:"contentTypeNosniff,omitempty"`
	BrowserXSSFilter      bool   `json:"browserXssFilter,omitempty"`
	ContentSecurityPolicy string `json:"contentSecurityPolicy,omitempty"`
	ReferrerPolicy        string `json:"referrerPolicy,omitempty"`
	// PermissionsPolicy sets the Permissions-Policy response header, e.g.
	// "geolocation=(), camera=(), microphone=()". Traefik has always supported it; this
	// field was simply missing, so the only way to send the header was to spell it out
	// under customResponseHeaders.
	PermissionsPolicy       string `json:"permissionsPolicy,omitempty"`
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

// ============================================================================
// VestaLogDrain
// ============================================================================

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Scope",type=string,JSONPath=`.status.scope`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Delivered",type=integer,JSONPath=`.status.recordsDelivered`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// +kubebuilder:resource:shortName=vld;drain
type VestaLogDrain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VestaLogDrainSpec   `json:"spec,omitempty"`
	Status VestaLogDrainStatus `json:"status,omitempty"`
}

// VestaLogDrainSpec describes one destination for app logs.
//
// Scope is the attachment: a drain with no Project applies to every app, one with a Project
// to that project's apps, and so on. That differs from VestaMiddleware, where order was
// semantic and attachment had to be an explicit ordered list on the app -- log delivery is
// a set, so a record simply reaches every drain whose scope covers it.
//
// Every field carries omitempty deliberately. controller-gen marks a field without it as
// required in the generated schema, and a newly-required field rejects resources an earlier
// release accepted; that is the 0.7.1 defect, and hack/check-crd-compat.sh guards it.
type VestaLogDrainSpec struct {
	// +kubebuilder:validation:Enum=http;loki;syslog;elasticsearch;datadog;s3;openobserve;forward
	Type string `json:"type"`

	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`

	// Enabled turns the whole drain off without deleting it, keeping its configuration and
	// credentials for when it is turned back on. To exempt individual apps instead, use
	// ExcludeApps.
	Enabled *bool `json:"enabled,omitempty"`

	// Project, App and Environment narrow which apps ship here. All empty is platform-wide.
	Project     string `json:"project,omitempty"`
	App         string `json:"app,omitempty"`
	Environment string `json:"environment,omitempty"`

	// ExcludeApps names apps within the scope that must not ship here, as "<app>" for any
	// project or "<project>/<app>" for one. It is how a project-wide drain skips the one
	// app whose logs are too noisy or too sensitive to send.
	//
	// Exclusion is a list rather than a flag on the app because Fluent Bit has no negative
	// Match: the collector routes by tag, so leaving an app out means enumerating the ones
	// that remain. Keeping that list on the drain is what makes it computable at all.
	ExcludeApps []string `json:"excludeApps,omitempty"`

	HTTP          *HTTPDrain          `json:"http,omitempty"`
	Loki          *LokiDrain          `json:"loki,omitempty"`
	Syslog        *SyslogDrain        `json:"syslog,omitempty"`
	Elasticsearch *ElasticsearchDrain `json:"elasticsearch,omitempty"`
	Datadog       *DatadogDrain       `json:"datadog,omitempty"`
	S3            *S3Drain            `json:"s3,omitempty"`
	OpenObserve   *OpenObserveDrain   `json:"openobserve,omitempty"`
	Forward       *ForwardDrain       `json:"forward,omitempty"`

	Retry *LogRetryPolicy `json:"retry,omitempty"`
}

// DrainSecretRef names a key in a Secret in the release namespace. Credentials are never
// written to a VestaLogDrain: a CRD is readable by anyone holding get on the type, so an
// API key stored there is a leak however it arrived. The operator mounts the Secret into
// the collector and the generated config references it by environment variable.
type DrainSecretRef struct {
	Name string `json:"name"`
	Key  string `json:"key,omitempty"`
}

type HTTPDrain struct {
	URI string `json:"uri"`
	// +kubebuilder:validation:Enum=json;json_stream;json_lines;gelf;msgpack
	Format  string            `json:"format,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// AuthHeader names a Secret key whose value becomes the Authorization header.
	AuthHeader *DrainSecretRef `json:"authHeader,omitempty"`
	TLSVerify  *bool           `json:"tlsVerify,omitempty"`
	Compress   string          `json:"compress,omitempty"`

	// DateKey names the field the event timestamp is written to. Empty sends "timestamp".
	// Destinations that key on a specific field need it set to theirs -- OpenObserve reads
	// "_timestamp" and otherwise stamps every record with its ingestion time, which looks
	// fine until a backlog drains and an hour of logs all share one timestamp.
	DateKey string `json:"dateKey,omitempty"`
}

type LokiDrain struct {
	Host string `json:"host"`
	Port int32  `json:"port,omitempty"`
	// Labels become Loki stream labels. The collector always adds project, environment and
	// app; these are extra.
	Labels    map[string]string `json:"labels,omitempty"`
	TenantID  string            `json:"tenantId,omitempty"`
	BasicAuth *DrainSecretRef   `json:"basicAuth,omitempty"`
	TLS       *bool             `json:"tls,omitempty"`
	TLSVerify *bool             `json:"tlsVerify,omitempty"`
}

type SyslogDrain struct {
	Host string `json:"host"`
	Port int32  `json:"port,omitempty"`
	// +kubebuilder:validation:Enum=tcp;udp;tls
	Mode string `json:"mode,omitempty"`
	// +kubebuilder:validation:Enum=rfc5424;rfc3164
	Format       string `json:"format,omitempty"`
	AppNameKey   string `json:"appNameKey,omitempty"`
	HostnameKey  string `json:"hostnameKey,omitempty"`
	MessageKey   string `json:"messageKey,omitempty"`
	SeverityKey  string `json:"severityKey,omitempty"`
	FacilityKey  string `json:"facilityKey,omitempty"`
	TLSVerify    *bool  `json:"tlsVerify,omitempty"`
	MaxSizeBytes int32  `json:"maxSizeBytes,omitempty"`
}

type ElasticsearchDrain struct {
	Host string `json:"host"`
	Port int32  `json:"port,omitempty"`
	// Index is the target index; IndexPrefix with LogstashFormat produces dated indices.
	Index          string          `json:"index,omitempty"`
	LogstashFormat *bool           `json:"logstashFormat,omitempty"`
	LogstashPrefix string          `json:"logstashPrefix,omitempty"`
	Type           string          `json:"type,omitempty"`
	BasicAuth      *DrainSecretRef `json:"basicAuth,omitempty"`
	CloudID        *DrainSecretRef `json:"cloudId,omitempty"`
	TLS            *bool           `json:"tls,omitempty"`
	TLSVerify      *bool           `json:"tlsVerify,omitempty"`
	// SuppressTypeName is required for Elasticsearch 8 and above, which removed mapping
	// types; sending one makes it reject the whole batch.
	SuppressTypeName *bool `json:"suppressTypeName,omitempty"`
}

type DatadogDrain struct {
	// APIKey is required and always a Secret reference.
	APIKey *DrainSecretRef `json:"apiKey,omitempty"`
	// Site is the Datadog region host, e.g. datadoghq.com or datadoghq.eu. Sending EU data
	// to the US endpoint is silently accepted and lands in the wrong account.
	Site          string            `json:"site,omitempty"`
	Service       string            `json:"service,omitempty"`
	Source        string            `json:"source,omitempty"`
	Tags          map[string]string `json:"tags,omitempty"`
	IncludeTagKey *bool             `json:"includeTagKey,omitempty"`
	Compress      string            `json:"compress,omitempty"`
}

type S3Drain struct {
	Bucket string `json:"bucket"`
	Region string `json:"region,omitempty"`
	// Credentials may be omitted entirely when the node or a service account provides them
	// through IRSA or an instance role.
	Credentials   *DrainSecretRef `json:"credentials,omitempty"`
	Endpoint      string          `json:"endpoint,omitempty"`
	TotalFileSize string          `json:"totalFileSize,omitempty"`
	UploadTimeout string          `json:"uploadTimeout,omitempty"`
	S3KeyFormat   string          `json:"s3KeyFormat,omitempty"`
	UsePutObject  *bool           `json:"usePutObject,omitempty"`
	Compression   string          `json:"compression,omitempty"`
	StorageClass  string          `json:"storageClass,omitempty"`
}

// OpenObserveDrain ships to OpenObserve's JSON ingest API.
//
// This is the http drain with the endpoint shape and timestamp field filled in. Worth a
// type of its own because both are easy to get subtly wrong: the URL is
// /api/<org>/<stream>/_json rather than anything guessable, and OpenObserve keys on
// "_timestamp" -- send "timestamp" and it accepts every record while stamping each with its
// ingestion time, which looks correct until a backlog drains.
type OpenObserveDrain struct {
	// Endpoint is the base URL, e.g. https://openobserve.example.com -- no path.
	Endpoint string `json:"endpoint"`
	// Organization defaults to "default", which is what a single-tenant install uses.
	Organization string `json:"organization,omitempty"`
	// Stream is the destination stream; it is created on first write.
	Stream string `json:"stream,omitempty"`
	// Credentials holds "email:password". OpenObserve authenticates with HTTP basic auth.
	Credentials *DrainSecretRef `json:"credentials,omitempty"`
	TLSVerify   *bool           `json:"tlsVerify,omitempty"`
	Compress    string          `json:"compress,omitempty"`
}

// ForwardDrain ships to another Fluent Bit or Fluentd over the forward protocol.
//
// For clusters that already run an aggregator: Vesta's per-node collectors become
// forwarders, and the aggregator keeps owning where logs ultimately go. That is the right
// shape when routing rules already live there, or when the real destination is reachable
// only from the aggregator.
//
// The aggregator sees records already tagged and enriched -- vesta_project, vesta_namespace
// and vesta_app are set before any output runs -- so its own routing can match on those
// rather than re-deriving them from the Kubernetes metadata.
type ForwardDrain struct {
	Host string `json:"host"`
	Port int32  `json:"port,omitempty"`

	// SharedKey enables Fluent Bit's handshake. Without it the aggregator accepts records
	// from anything that can reach the port.
	SharedKey    *DrainSecretRef `json:"sharedKey,omitempty"`
	SelfHostname string          `json:"selfHostname,omitempty"`

	TLS       *bool `json:"tls,omitempty"`
	TLSVerify *bool `json:"tlsVerify,omitempty"`

	// TimeAsInteger is needed by Fluentd v0.12 and earlier, which cannot read the
	// event-time format newer versions use.
	TimeAsInteger *bool `json:"timeAsInteger,omitempty"`
}

// LogRetryPolicy controls what the collector does with records it cannot deliver.
type LogRetryPolicy struct {
	// Limit is the number of attempts before a batch is dropped. "false" retries forever,
	// which risks the buffer filling and blocking newer records -- that is why it is not
	// the default.
	Limit string `json:"limit,omitempty"`
	// StorageType "filesystem" survives a collector restart; "memory" does not but is
	// faster and is Fluent Bit's default.
	// +kubebuilder:validation:Enum=memory;filesystem
	StorageType string `json:"storageType,omitempty"`
}

type VestaLogDrainStatus struct {
	// Ready is false when the collector reports errors delivering to this drain, or when
	// the spec cannot be rendered at all.
	Ready  bool   `json:"ready,omitempty"`
	Reason string `json:"reason,omitempty"`

	// Scope is the resolved tag pattern the collector matches for this drain, surfaced so
	// that "why is this app not shipping" can be answered without reading the ConfigMap.
	Scope string `json:"scope,omitempty"`

	// RecordsDelivered and Errors come from the collector's own metrics endpoint. Without
	// them a misconfigured drain is indistinguishable from an app that logged nothing, and
	// the first sign of trouble is an empty dashboard during an incident.
	RecordsDelivered   int64  `json:"recordsDelivered,omitempty"`
	Errors             int64  `json:"errors,omitempty"`
	RetriesFailed      int64  `json:"retriesFailed,omitempty"`
	LastDeliveryAt     string `json:"lastDeliveryAt,omitempty"`
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
type VestaLogDrainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VestaLogDrain `json:"items"`
}

// ============================================================================
// VestaAddon
// ============================================================================

// VestaAddon is a managed datastore an app can bind to.
//
// spec.addons existed on VestaApp long before anything reconciled it: the API accepted the
// field, the CRD stored it, and no controller ever read it, so declaring an add-on did
// nothing at all. This is the kind that makes it real.
//
// It is a kind of its own rather than a slice on the app because its lifecycle is not the
// app's. A database outlives the app that first asked for it, its data must survive the
// app being deleted, and two apps may share one.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:resource:shortName=vad;addon
type VestaAddon struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VestaAddonSpec   `json:"spec,omitempty"`
	Status VestaAddonStatus `json:"status,omitempty"`
}

type VestaAddonSpec struct {
	// +kubebuilder:validation:Enum=postgres;mysql;redis;mongodb
	Type string `json:"type"`

	// Provider decides who renders the add-on. "builtin" is a single-replica StatefulSet
	// Vesta manages itself.
	//
	// Reserved rather than speculative: an operator-backed provider (CloudNativePG and the
	// like) changes what is rendered, not what is declared, so having the field from the
	// start means adding one later is not a schema change for every stored object.
	// +kubebuilder:validation:Enum=builtin
	Provider string `json:"provider,omitempty"`

	Project string `json:"project"`
	// Environment limits the add-on to one environment of the project. Empty means every
	// environment gets its own instance, which is what keeps staging data out of
	// production.
	Environment string `json:"environment,omitempty"`

	Version string `json:"version,omitempty"`
	// Size names a PodSizePreset, resolved the same way an app's is.
	Size string `json:"size,omitempty"`

	Storage      string `json:"storage,omitempty"`
	StorageClass string `json:"storageClass,omitempty"`

	// DeletionPolicy decides what happens to the data when this add-on is deleted. Empty
	// means Retain -- losing a database to a mistyped name is not something anyone should
	// have to opt out of.
	// +kubebuilder:validation:Enum=Retain;Delete
	DeletionPolicy string `json:"deletionPolicy,omitempty"`
}

type VestaAddonStatus struct {
	Ready  bool   `json:"ready,omitempty"`
	Reason string `json:"reason,omitempty"`
	Phase  string `json:"phase,omitempty"`

	// SecretName is the Secret holding connection details, present in every namespace the
	// add-on was projected into.
	SecretName string `json:"secretName,omitempty"`
	// Namespaces lists where this add-on currently runs.
	Namespaces []string `json:"namespaces,omitempty"`
	// RetainedPVCs names claims left behind by a deletion under the Retain policy, so the
	// data can be found and reclaimed deliberately.
	RetainedPVCs []string `json:"retainedPVCs,omitempty"`

	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
type VestaAddonList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VestaAddon `json:"items"`
}

// Addon deletion policies.
const (
	AddonRetain = "Retain"
	AddonDelete = "Delete"
)
