package controllers

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// Rendering an add-on into Kubernetes objects.
//
// Kept separate from the controller and free of any client, because the interesting parts --
// which image, which port, what the connection details are called, where the data lives --
// are decisions, and decisions should be checkable without a cluster.

const addonLabel = "kubernetes.getvesta.sh/addon"

// engine describes one datastore.
type engine struct {
	image       string
	port        int32
	dataPath    string
	subPath     string
	defaultDB   string
	defaultUser string
	scheme      string
	// env builds the container environment from the generated credentials.
	env func(c AddonCredentials) []corev1.EnvVar
	// args overrides the container command line, for engines configured by flag.
	args func(c AddonCredentials) []string
	// probe is the command that reports the engine ready.
	probe []string
}

// engines is the whole supported set. Adding one is a new entry and nothing else.
var engines = map[string]engine{
	"postgres": {
		image: "postgres:%s", port: 5432,
		dataPath: "/var/lib/postgresql/data", subPath: "pgdata",
		defaultDB: "app", defaultUser: "vesta", scheme: "postgres",
		env: func(c AddonCredentials) []corev1.EnvVar {
			return []corev1.EnvVar{
				{Name: "POSTGRES_USER", Value: c.Username},
				{Name: "POSTGRES_PASSWORD", Value: c.Password},
				{Name: "POSTGRES_DB", Value: c.Database},
				// Postgres refuses a data directory that is not empty, and a PVC always
				// has a lost+found. A subdirectory is the documented way around it.
				{Name: "PGDATA", Value: "/var/lib/postgresql/data/pgdata"},
			}
		},
		probe: []string{"pg_isready", "-U", "vesta"},
	},
	"mysql": {
		image: "mysql:%s", port: 3306,
		dataPath: "/var/lib/mysql", defaultDB: "app", defaultUser: "vesta", scheme: "mysql",
		env: func(c AddonCredentials) []corev1.EnvVar {
			return []corev1.EnvVar{
				{Name: "MYSQL_ROOT_PASSWORD", Value: c.Password},
				{Name: "MYSQL_DATABASE", Value: c.Database},
				{Name: "MYSQL_USER", Value: c.Username},
				{Name: "MYSQL_PASSWORD", Value: c.Password},
			}
		},
		probe: []string{"mysqladmin", "ping", "-h", "127.0.0.1"},
	},
	"redis": {
		image: "redis:%s", port: 6379,
		dataPath: "/data", defaultUser: "default", scheme: "redis",
		env: func(c AddonCredentials) []corev1.EnvVar { return nil },
		// Redis takes its password on the command line rather than from the environment.
		args: func(c AddonCredentials) []string {
			return []string{"redis-server", "--requirepass", c.Password, "--appendonly", "yes"}
		},
		probe: []string{"redis-cli", "ping"},
	},
	"mongodb": {
		image: "mongo:%s", port: 27017,
		dataPath: "/data/db", defaultDB: "app", defaultUser: "vesta", scheme: "mongodb",
		env: func(c AddonCredentials) []corev1.EnvVar {
			return []corev1.EnvVar{
				{Name: "MONGO_INITDB_ROOT_USERNAME", Value: c.Username},
				{Name: "MONGO_INITDB_ROOT_PASSWORD", Value: c.Password},
				{Name: "MONGO_INITDB_DATABASE", Value: c.Database},
			}
		},
		probe: []string{"mongosh", "--eval", "db.adminCommand('ping')"},
	},
}

// defaultVersions are the image tags used when none is given. Pinned rather than "latest":
// a StatefulSet that silently changes major version on restart is a data-loss incident.
var defaultVersions = map[string]string{
	"postgres": "16",
	"mysql":    "8",
	"redis":    "7",
	"mongodb":  "7",
}

// AddonCredentials is what an app needs in order to connect.
type AddonCredentials struct {
	Host     string
	Port     int32
	Username string
	Password string
	Database string
	URL      string
}

// SupportedAddonTypes lists the engines, sorted, for error messages and the UI.
func SupportedAddonTypes() []string {
	out := make([]string, 0, len(engines))
	for k := range engines {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// AddonImage returns the container image for a type and version.
func AddonImage(addonType, version string) (string, error) {
	e, ok := engines[addonType]
	if !ok {
		return "", fmt.Errorf("unsupported addon type %q; supported: %s",
			addonType, strings.Join(SupportedAddonTypes(), ", "))
	}
	if version == "" {
		version = defaultVersions[addonType]
	}
	return fmt.Sprintf(e.image, version), nil
}

// generatePassword returns a URL-safe secret.
func generatePassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// base64url without padding: safe inside a connection URL without escaping, which
	// matters because the URL is what most apps actually read.
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "="), nil
}

// BuildCredentials produces connection details for an add-on.
//
// existing is whatever is already stored, and its password always wins. Regenerating one on
// a reconcile would rotate the secret under a running database that still has the old one --
// the app would start failing to connect with no change to anything it can see.
func BuildCredentials(addon *vestav1alpha1.VestaAddon, namespace string, existing map[string][]byte) (AddonCredentials, error) {
	e, ok := engines[addon.Spec.Type]
	if !ok {
		return AddonCredentials{}, fmt.Errorf("unsupported addon type %q", addon.Spec.Type)
	}

	c := AddonCredentials{
		Host:     fmt.Sprintf("%s.%s.svc.cluster.local", addon.Name, namespace),
		Port:     e.port,
		Username: e.defaultUser,
		Database: e.defaultDB,
	}
	if c.Database == "" {
		c.Database = addon.Name
	}

	if pw, ok := existing["PASSWORD"]; ok && len(pw) > 0 {
		c.Password = string(pw)
	} else {
		pw, err := generatePassword()
		if err != nil {
			return AddonCredentials{}, err
		}
		c.Password = pw
	}

	c.URL = connectionURL(e, c)
	return c, nil
}

func connectionURL(e engine, c AddonCredentials) string {
	switch e.scheme {
	case "redis":
		// Redis has no user, only a password.
		return fmt.Sprintf("redis://:%s@%s:%d", c.Password, c.Host, c.Port)
	case "mongodb":
		return fmt.Sprintf("mongodb://%s:%s@%s:%d/%s?authSource=admin",
			c.Username, c.Password, c.Host, c.Port, c.Database)
	default:
		return fmt.Sprintf("%s://%s:%s@%s:%d/%s",
			e.scheme, c.Username, c.Password, c.Host, c.Port, c.Database)
	}
}

// CredentialData renders credentials as Secret keys.
//
// Prefixed by engine so two add-ons of different types can be injected into one app without
// colliding, plus an unprefixed DATABASE_URL, which is what most frameworks read.
func CredentialData(addonType string, c AddonCredentials) map[string]string {
	prefix := strings.ToUpper(addonType) + "_"
	data := map[string]string{
		"HOST":              c.Host,
		"PORT":              fmt.Sprintf("%d", c.Port),
		"USERNAME":          c.Username,
		"PASSWORD":          c.Password,
		"DATABASE":          c.Database,
		"URL":               c.URL,
		prefix + "HOST":     c.Host,
		prefix + "PORT":     fmt.Sprintf("%d", c.Port),
		prefix + "USER":     c.Username,
		prefix + "PASSWORD": c.Password,
		prefix + "DB":       c.Database,
		prefix + "URL":      c.URL,
		"DATABASE_URL":      c.URL,
	}
	return data
}

// AddonSecretName is where an add-on's connection details live.
func AddonSecretName(addonName string) string { return addonName + "-credentials" }

// BuildAddonStatefulSet renders the workload.
func BuildAddonStatefulSet(addon *vestav1alpha1.VestaAddon, namespace string,
	c AddonCredentials, requests, limits corev1.ResourceList) (*appsv1.StatefulSet, error) {

	e, ok := engines[addon.Spec.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported addon type %q", addon.Spec.Type)
	}
	image, err := AddonImage(addon.Spec.Type, addon.Spec.Version)
	if err != nil {
		return nil, err
	}

	storage := addon.Spec.Storage
	if storage == "" {
		storage = "10Gi"
	}
	size, err := resource.ParseQuantity(storage)
	if err != nil {
		return nil, fmt.Errorf("storage %q is not a quantity: %w", storage, err)
	}

	labels := map[string]string{
		"app.kubernetes.io/name":       addon.Name,
		"app.kubernetes.io/managed-by": "vesta-operator",
		"app.kubernetes.io/component":  "addon",
		addonLabel:                     addon.Name,
	}

	container := corev1.Container{
		Name:  addon.Spec.Type,
		Image: image,
		Ports: []corev1.ContainerPort{{Name: addon.Spec.Type, ContainerPort: e.port}},
		Env:   e.env(c),
		VolumeMounts: []corev1.VolumeMount{{
			Name: "data", MountPath: e.dataPath, SubPath: e.subPath,
		}},
		Resources: corev1.ResourceRequirements{Requests: requests, Limits: limits},
	}
	if e.args != nil {
		container.Command = e.args(c)
	}
	if len(e.probe) > 0 {
		container.ReadinessProbe = &corev1.Probe{
			ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: e.probe}},
			InitialDelaySeconds: 10,
			PeriodSeconds:       10,
		}
	}

	replicas := int32(1)
	claim := corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data", Labels: labels},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: size}},
		},
	}
	if addon.Spec.StorageClass != "" {
		sc := addon.Spec.StorageClass
		claim.Spec.StorageClassName = &sc
	}

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: addon.Name, Namespace: namespace, Labels: labels},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: addon.Name,
			Replicas:    &replicas,
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{addonLabel: addon.Name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{container}},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{claim},
		},
	}, nil
}

// BuildAddonService renders the headless Service the StatefulSet is addressed through.
func BuildAddonService(addon *vestav1alpha1.VestaAddon, namespace string) (*corev1.Service, error) {
	e, ok := engines[addon.Spec.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported addon type %q", addon.Spec.Type)
	}

	labels := map[string]string{
		"app.kubernetes.io/managed-by": "vesta-operator",
		"app.kubernetes.io/component":  "addon",
		addonLabel:                     addon.Name,
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: addon.Name, Namespace: namespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			// Headless: a StatefulSet member is addressed directly, and there is exactly
			// one, so load balancing would add a hop and nothing else.
			ClusterIP: corev1.ClusterIPNone,
			Selector:  map[string]string{addonLabel: addon.Name},
			Ports: []corev1.ServicePort{{
				Name: addon.Spec.Type, Port: e.port, TargetPort: intstr.FromInt32(e.port),
			}},
		},
	}, nil
}

// RetainOnDelete reports whether an add-on's data survives its deletion.
//
// Empty means Retain. A database is not a thing to lose by default, and somebody who means
// to destroy it can say so.
func RetainOnDelete(addon *vestav1alpha1.VestaAddon) bool {
	return addon.Spec.DeletionPolicy != vestav1alpha1.AddonDelete
}
