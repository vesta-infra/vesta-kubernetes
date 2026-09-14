package controllers

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

func addon(t string) *vestav1alpha1.VestaAddon {
	return &vestav1alpha1.VestaAddon{
		ObjectMeta: metav1.ObjectMeta{Name: "db"},
		Spec:       vestav1alpha1.VestaAddonSpec{Type: t, Project: "acme"},
	}
}

// The worst bug available in this feature.
//
// A reconcile runs every time anything changes. If it generated a fresh password each time,
// the Secret would rotate under a running database that still has the old one -- and the app
// would start failing to connect with nothing visibly changed. The stored password always
// wins.
func TestPasswordIsNeverRegenerated(t *testing.T) {
	a := addon("postgres")

	first, err := BuildCredentials(a, "acme-production", nil)
	if err != nil {
		t.Fatalf("BuildCredentials: %v", err)
	}
	if first.Password == "" {
		t.Fatal("no password was generated")
	}

	existing := map[string][]byte{"PASSWORD": []byte(first.Password)}
	for i := 0; i < 5; i++ {
		again, err := BuildCredentials(a, "acme-production", existing)
		if err != nil {
			t.Fatalf("BuildCredentials: %v", err)
		}
		if again.Password != first.Password {
			t.Fatalf("password changed on reconcile %d: %q became %q", i, first.Password, again.Password)
		}
		if again.URL != first.URL {
			t.Errorf("connection URL changed even though the password did not")
		}
	}
}

// An empty stored password is not a password. Treating it as one would leave a database
// unreachable with no way to recover short of deleting it.
func TestEmptyStoredPasswordIsRegenerated(t *testing.T) {
	a := addon("postgres")
	c, err := BuildCredentials(a, "acme-production", map[string][]byte{"PASSWORD": []byte("")})
	if err != nil {
		t.Fatalf("BuildCredentials: %v", err)
	}
	if c.Password == "" {
		t.Error("an empty stored password was kept")
	}
}

// Passwords end up inside connection URLs, which are parsed by every client library. A
// character that needs escaping there produces a URL that silently connects to the wrong
// place, or fails to parse at all.
func TestPasswordsAreURLSafe(t *testing.T) {
	for i := 0; i < 50; i++ {
		pw, err := generatePassword()
		if err != nil {
			t.Fatalf("generatePassword: %v", err)
		}
		if len(pw) < 20 {
			t.Errorf("password %q is too short to be worth much", pw)
		}
		if strings.ContainsAny(pw, ":/?#[]@!$&'()*+,;=% \"\\") {
			t.Errorf("password %q contains a character that must be escaped in a URL", pw)
		}
	}
}

// Every engine has to produce a usable connection URL, and they do not agree on the shape:
// Redis has no username, Mongo needs an authSource.
func TestConnectionURLs(t *testing.T) {
	cases := map[string][]string{
		"postgres": {"postgres://", "vesta:", "/app"},
		"mysql":    {"mysql://", "vesta:", "/app"},
		"redis":    {"redis://:"},
		"mongodb":  {"mongodb://", "authSource=admin"},
	}

	for engine, wants := range cases {
		t.Run(engine, func(t *testing.T) {
			c, err := BuildCredentials(addon(engine), "acme-production", nil)
			if err != nil {
				t.Fatalf("BuildCredentials: %v", err)
			}
			for _, want := range wants {
				if !strings.Contains(c.URL, want) {
					t.Errorf("URL %q does not contain %q", c.URL, want)
				}
			}
			if !strings.Contains(c.URL, c.Password) {
				t.Error("the URL does not carry the password, so it cannot be used to connect")
			}
		})
	}

	// Redis authenticates with a password alone; including a username makes the URL
	// invalid for most clients.
	c, _ := BuildCredentials(addon("redis"), "ns", nil)
	if strings.Contains(c.URL, "default:") {
		t.Errorf("the redis URL %q carries a username", c.URL)
	}
}

// Two add-ons of different types must be injectable into one app without one silently
// overwriting the other's connection details.
func TestCredentialKeysArePrefixed(t *testing.T) {
	pg, _ := BuildCredentials(addon("postgres"), "ns", nil)
	rd, _ := BuildCredentials(addon("redis"), "ns", nil)

	pgData := CredentialData("postgres", pg)
	rdData := CredentialData("redis", rd)

	if pgData["POSTGRES_URL"] == "" || rdData["REDIS_URL"] == "" {
		t.Fatal("prefixed keys are missing")
	}
	if pgData["POSTGRES_URL"] == rdData["REDIS_URL"] {
		t.Error("two engines produced the same prefixed URL")
	}
	// DATABASE_URL is what most frameworks read, so it is present unprefixed too -- and
	// that one genuinely does collide, which is why the prefixed keys exist.
	if pgData["DATABASE_URL"] != pg.URL {
		t.Error("DATABASE_URL does not match the connection URL")
	}
}

// A version that drifts on restart is a data-loss incident: a StatefulSet that comes back as
// a new major version may refuse the existing data directory, or migrate it irreversibly.
func TestVersionsArePinnedNotLatest(t *testing.T) {
	for _, engine := range SupportedAddonTypes() {
		image, err := AddonImage(engine, "")
		if err != nil {
			t.Fatalf("AddonImage(%q): %v", engine, err)
		}
		if strings.HasSuffix(image, ":latest") || !strings.Contains(image, ":") {
			t.Errorf("%s defaults to %q; a default of latest changes major version on restart", engine, image)
		}
	}

	if got, _ := AddonImage("postgres", "15"); got != "postgres:15" {
		t.Errorf("AddonImage(postgres, 15) = %q", got)
	}
	if _, err := AddonImage("cassandra", ""); err == nil {
		t.Error("an unsupported engine was accepted")
	}
}

// Losing a database to a mistyped name is not something anyone should have to opt out of.
func TestDeletionRetainsByDefault(t *testing.T) {
	if !RetainOnDelete(addon("postgres")) {
		t.Error("an add-on with no deletion policy did not retain its data")
	}

	keep := addon("postgres")
	keep.Spec.DeletionPolicy = vestav1alpha1.AddonRetain
	if !RetainOnDelete(keep) {
		t.Error("an explicit Retain did not retain")
	}

	drop := addon("postgres")
	drop.Spec.DeletionPolicy = vestav1alpha1.AddonDelete
	if RetainOnDelete(drop) {
		t.Error("an explicit Delete retained anyway")
	}
}

// Postgres refuses to initialise into a directory that is not empty, and a freshly
// provisioned PVC always contains lost+found. Without the subdirectory the pod crash-loops
// on first start with a message nobody reads as "mount layout".
func TestPostgresUsesASubdirectory(t *testing.T) {
	a := addon("postgres")
	c, _ := BuildCredentials(a, "ns", nil)

	sts, err := BuildAddonStatefulSet(a, "ns", c, nil, nil)
	if err != nil {
		t.Fatalf("BuildAddonStatefulSet: %v", err)
	}

	mount := sts.Spec.Template.Spec.Containers[0].VolumeMounts[0]
	if mount.SubPath == "" {
		t.Error("postgres mounts the volume root; initdb will refuse it because of lost+found")
	}

	var pgdata string
	for _, e := range sts.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "PGDATA" {
			pgdata = e.Value
		}
	}
	if !strings.HasPrefix(pgdata, mount.MountPath) || pgdata == mount.MountPath {
		t.Errorf("PGDATA %q is not a subdirectory of the mount %q", pgdata, mount.MountPath)
	}
}

func TestStatefulSetShape(t *testing.T) {
	a := addon("postgres")
	a.Spec.Storage = "20Gi"
	c, _ := BuildCredentials(a, "acme-production", nil)

	sts, err := BuildAddonStatefulSet(a, "acme-production", c, nil, nil)
	if err != nil {
		t.Fatalf("BuildAddonStatefulSet: %v", err)
	}

	if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 1 {
		t.Error("a builtin add-on is single-replica; more than one would need replication it does not set up")
	}
	if len(sts.Spec.VolumeClaimTemplates) != 1 {
		t.Fatal("no volume claim template, so the data would live in the pod")
	}
	if got := sts.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != "20Gi" {
		t.Errorf("storage = %s, want 20Gi", got.String())
	}
	if sts.Spec.ServiceName != a.Name {
		t.Errorf("ServiceName = %q, want the add-on name so DNS resolves", sts.Spec.ServiceName)
	}

	// Bad storage must be reported, not rendered into an object the API server rejects
	// with a message about quantities.
	bad := addon("postgres")
	bad.Spec.Storage = "twenty gigs"
	if _, err := BuildAddonStatefulSet(bad, "ns", c, nil, nil); err == nil {
		t.Error("an unparseable storage size was accepted")
	}
}

func TestServiceIsHeadlessAndMatchesTheWorkload(t *testing.T) {
	a := addon("redis")
	c, _ := BuildCredentials(a, "ns", nil)

	svc, err := BuildAddonService(a, "ns")
	if err != nil {
		t.Fatalf("BuildAddonService: %v", err)
	}
	sts, _ := BuildAddonStatefulSet(a, "ns", c, nil, nil)

	if svc.Spec.ClusterIP != corev1.ClusterIPNone {
		t.Error("the service is not headless")
	}
	// A selector that does not match the pods is a service that resolves to nothing, and
	// the only symptom is a connection timeout from the app.
	for k, v := range svc.Spec.Selector {
		if sts.Spec.Template.Labels[k] != v {
			t.Errorf("service selector %s=%s does not match the pod labels", k, v)
		}
	}
	if svc.Spec.Ports[0].Port != sts.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort {
		t.Error("the service port does not match the container port")
	}
}

// The host in the credentials must be the Service that actually exists, or the URL points
// at nothing.
func TestCredentialHostMatchesTheService(t *testing.T) {
	a := addon("postgres")
	c, _ := BuildCredentials(a, "acme-production", nil)
	svc, _ := BuildAddonService(a, "acme-production")

	if !strings.HasPrefix(c.Host, svc.Name+".acme-production.svc") {
		t.Errorf("credential host %q does not name the service %q in its namespace", c.Host, svc.Name)
	}
}
