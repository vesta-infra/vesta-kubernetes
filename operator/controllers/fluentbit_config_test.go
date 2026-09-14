package controllers

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

func drain(name, drainType string, mutate func(*vestav1alpha1.VestaLogDrainSpec)) vestav1alpha1.VestaLogDrain {
	spec := vestav1alpha1.VestaLogDrainSpec{Type: drainType}
	if mutate != nil {
		mutate(&spec)
	}
	return vestav1alpha1.VestaLogDrain{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       spec,
	}
}

func httpDrain(name string, mutate func(*vestav1alpha1.VestaLogDrainSpec)) vestav1alpha1.VestaLogDrain {
	return drain(name, "http", func(s *vestav1alpha1.VestaLogDrainSpec) {
		s.HTTP = &vestav1alpha1.HTTPDrain{URI: "https://logs.example.com/ingest"}
		if mutate != nil {
			mutate(s)
		}
	})
}

// Match patterns are the entire routing behaviour: a wrong one silently ships an app's logs
// to somebody else's destination, or silently ships nothing. These pin the exact output.
func TestMatchFor(t *testing.T) {
	cases := []struct {
		name   string
		target DrainTarget
		want   string
	}{
		{
			name:   "platform-wide matches every tag",
			target: DrainTarget{Drain: httpDrain("central", nil)},
			want:   "Match                 vesta.*",
		},
		{
			name: "one namespace",
			target: DrainTarget{
				Drain:      httpDrain("proj", func(s *vestav1alpha1.VestaLogDrainSpec) { s.Project = "shop" }),
				Namespaces: []string{"shop-production"},
			},
			want: "Match                 vesta.shop-production.*",
		},
		{
			name: "several namespaces are enumerated, not globbed",
			target: DrainTarget{
				Drain:      httpDrain("proj", func(s *vestav1alpha1.VestaLogDrainSpec) { s.Project = "shop" }),
				Namespaces: []string{"shop-production", "shop-staging"},
			},
			want: `Match_Regex           ^vesta\.(shop-production|shop-staging)\..*`,
		},
		{
			name: "one app in one namespace",
			target: DrainTarget{
				Drain: httpDrain("api-only", func(s *vestav1alpha1.VestaLogDrainSpec) {
					s.Project, s.Environment, s.App = "shop", "production", "api"
				}),
				Namespaces: []string{"shop-production"},
			},
			want: "Match                 vesta.shop-production.api",
		},
		{
			name: "an app across every namespace",
			target: DrainTarget{
				Drain: httpDrain("api-anywhere", func(s *vestav1alpha1.VestaLogDrainSpec) { s.App = "api" }),
			},
			want: `Match_Regex           ^vesta\.[^.]+\.api$`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.TrimSpace(matchFor(tc.target))
			if got != tc.want {
				t.Errorf("\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// A project name that prefixes another is the case a glob cannot handle: "shop" would
// otherwise capture "shop-extra-prod". This is why namespaces are resolved and enumerated.
func TestProjectPrefixDoesNotCaptureAnotherProject(t *testing.T) {
	got := matchFor(DrainTarget{
		Drain:      httpDrain("shop-logs", func(s *vestav1alpha1.VestaLogDrainSpec) { s.Project = "shop" }),
		Namespaces: []string{"shop-production", "shop-staging"},
	})
	if strings.Contains(got, "shop-*") || strings.Contains(got, "shop-[") {
		t.Errorf("a glob on the project name would capture shop-extra's namespaces: %s", got)
	}
	if !strings.Contains(got, "shop-production") || !strings.Contains(got, "shop-staging") {
		t.Errorf("both namespaces should be listed: %s", got)
	}
	if strings.Contains(got, "shop-extra") {
		t.Errorf("another project leaked in: %s", got)
	}
}

// Opting out has to turn the pattern inside out, because Fluent Bit has no negative Match.
func TestOptOutEnumeratesWhatRemains(t *testing.T) {
	target := DrainTarget{
		Drain:        httpDrain("central", nil),
		IncludedApps: []string{"shop-production/api", "shop-production/web", "utility-prod/vesta-deploy"},
		ExcludedApps: []string{"utility-prod/vesta-deploy"},
	}
	got := matchFor(target)

	if strings.Contains(got, "vesta-deploy") {
		t.Errorf("the opted-out app is still matched: %s", got)
	}
	for _, kept := range []string{"api", "web"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%s should still ship: %s", kept, got)
		}
	}
	if strings.Contains(got, "Match  ") && !strings.Contains(got, "Match_Regex") {
		t.Errorf("an exclusion cannot use a plain Match: %s", got)
	}
}

func TestOptOutByEveryAppMatchesNothing(t *testing.T) {
	// An empty alternation would read as "match anything", which is the opposite of what
	// was asked for -- every app opting out must ship nothing, not everything.
	got := matchFor(DrainTarget{
		Drain:        httpDrain("central", nil),
		IncludedApps: []string{"shop-production/api"},
		ExcludedApps: []string{"shop-production/api"},
	})
	if strings.Contains(got, "(|)") || strings.Contains(got, "()") {
		t.Errorf("empty alternation matches everything: %s", got)
	}
	if !strings.Contains(got, "$^") {
		t.Errorf("expected a pattern that cannot match, got: %s", got)
	}
}

// No credential may appear in the rendered config: the ConfigMap is readable by anyone with
// get on it in the namespace, and it is not the Secret.
func TestNoCredentialReachesTheRenderedConfig(t *testing.T) {
	secret := &vestav1alpha1.DrainSecretRef{Name: "creds", Key: "token"}

	targets := []DrainTarget{
		{Drain: drain("dd", "datadog", func(s *vestav1alpha1.VestaLogDrainSpec) {
			s.Datadog = &vestav1alpha1.DatadogDrain{APIKey: secret, Site: "datadoghq.eu"}
		})},
		{Drain: drain("es", "elasticsearch", func(s *vestav1alpha1.VestaLogDrainSpec) {
			s.Elasticsearch = &vestav1alpha1.ElasticsearchDrain{Host: "es.internal", BasicAuth: secret}
		})},
		{Drain: drain("lk", "loki", func(s *vestav1alpha1.VestaLogDrainSpec) {
			s.Loki = &vestav1alpha1.LokiDrain{Host: "loki.internal", BasicAuth: secret}
		})},
		{Drain: drain("s3", "s3", func(s *vestav1alpha1.VestaLogDrainSpec) {
			s.S3 = &vestav1alpha1.S3Drain{Bucket: "logs", Credentials: secret}
		})},
		{Drain: httpDrain("web", func(s *vestav1alpha1.VestaLogDrainSpec) { s.HTTP.AuthHeader = secret })},
	}

	config, err := RenderFluentBitConfig(targets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The Secret's name and key are the only things that may appear, and only as the
	// environment variable indirection.
	for _, forbidden := range []string{"token", "password", "apikey ", "secret_access_key "} {
		if strings.Contains(strings.ToLower(config), forbidden) &&
			!strings.Contains(config, "${VESTA_DRAIN_") {
			t.Errorf("a credential may have been rendered literally; config contains %q", forbidden)
		}
	}
	for _, expected := range []string{
		"${VESTA_DRAIN_DD_API_KEY}", "${VESTA_DRAIN_ES_USER}",
		"${VESTA_DRAIN_LK_PASSWORD}", "${VESTA_DRAIN_S3_AWS_ACCESS_KEY_ID}",
		"${VESTA_DRAIN_WEB_AUTH}",
	} {
		if !strings.Contains(config, expected) {
			t.Errorf("expected the indirection %s in the config", expected)
		}
	}
}

func TestRenderRequiresTheBodyMatchingItsType(t *testing.T) {
	// A drain whose body does not match its type would otherwise render an OUTPUT with no
	// destination, which Fluent Bit accepts and which silently discards every record.
	for _, d := range []vestav1alpha1.VestaLogDrain{
		drain("a", "http", nil),
		drain("b", "loki", nil),
		drain("c", "datadog", nil),
		drain("d", "s3", nil),
		drain("e", "teleport", nil),
	} {
		if _, err := RenderFluentBitConfig([]DrainTarget{{Drain: d}}); err == nil {
			t.Errorf("drain %s (%s) should not render", d.Name, d.Spec.Type)
		}
	}
}

func TestNoDrainsStillProducesAValidConfig(t *testing.T) {
	config, err := RenderFluentBitConfig(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// A collector with no OUTPUT at all logs warnings on every flush. The null output keeps
	// it quiet and healthy, so "no logs arriving" does not look like a broken collector.
	if !strings.Contains(config, "Name  null") {
		t.Errorf("expected a null output when nothing is configured:\n%s", config)
	}
	if !strings.Contains(config, "[INPUT]") {
		t.Error("the input should still be configured")
	}
}

func TestOutputOrderIsStable(t *testing.T) {
	// The config's checksum rolls the DaemonSet. If rendering the same drains twice
	// produced different bytes, every reconcile would restart the collector on every node.
	targets := []DrainTarget{
		{Drain: httpDrain("zeta", nil)},
		{Drain: httpDrain("alpha", nil)},
		{Drain: httpDrain("mid", nil)},
	}
	first, _ := RenderFluentBitConfig(targets)

	reversed := []DrainTarget{targets[2], targets[0], targets[1]}
	second, _ := RenderFluentBitConfig(reversed)

	if first != second {
		t.Error("render is order-dependent; the DaemonSet would roll on every reconcile")
	}
	if strings.Index(first, "alpha") > strings.Index(first, "zeta") {
		t.Error("outputs should be sorted by name")
	}
}

func TestSplitEndpoint(t *testing.T) {
	cases := []struct {
		uri, path, host, port string
		tls                   bool
	}{
		{"https://logs.example.com/ingest", "/ingest", "logs.example.com", "443", true},
		{"http://logs.internal:8080/v1/logs", "/v1/logs", "logs.internal", "8080", false},
		{"https://logs.example.com", "/", "logs.example.com", "443", true},
		{"http://collector", "/", "collector", "80", false},
	}
	for _, tc := range cases {
		path, host, port, tls := splitEndpoint(tc.uri)
		if path != tc.path || host != tc.host || port != tc.port || tls != tc.tls {
			t.Errorf("%s -> path=%q host=%q port=%q tls=%v, want path=%q host=%q port=%q tls=%v",
				tc.uri, path, host, port, tls, tc.path, tc.host, tc.port, tc.tls)
		}
	}
}

func TestElasticsearch8CompatibilityIsOnByDefault(t *testing.T) {
	// Sending a mapping type to Elasticsearch 8 makes it reject the entire batch, so the
	// default has to be the one that works on current versions.
	config, err := RenderFluentBitConfig([]DrainTarget{
		{Drain: drain("es", "elasticsearch", func(s *vestav1alpha1.VestaLogDrainSpec) {
			s.Elasticsearch = &vestav1alpha1.ElasticsearchDrain{Host: "es.internal"}
		})},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(config, "Suppress_Type_Name    On") {
		t.Errorf("expected Suppress_Type_Name On by default:\n%s", config)
	}
}

func TestRouteScriptDropsNonVestaPods(t *testing.T) {
	script := RenderRouteScript()
	// Vesta's own API logs request detail. Routing every pod on the node would ship that
	// to whatever destination an app owner configured.
	if !strings.Contains(script, "kubernetes.getvesta.sh/app") {
		t.Error("the route should be derived from the Vesta app label")
	}
	if strings.Count(script, "return -1") < 3 {
		t.Error("records without Kubernetes metadata, labels or an app label must be dropped")
	}
}

// OpenObserve keys on "_timestamp"; sending "timestamp" makes it stamp every record with
// its ingestion time instead. That looks correct until a backlog drains and an hour of logs
// all arrive with the same timestamp.
func TestHTTPDateKeyIsConfigurable(t *testing.T) {
	withKey, err := RenderFluentBitConfig([]DrainTarget{
		{Drain: httpDrain("openobserve", func(s *vestav1alpha1.VestaLogDrainSpec) {
			s.HTTP.URI = "https://o2.example.com/api/default/vesta/_json"
			s.HTTP.DateKey = "_timestamp"
		})},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(withKey, "json_date_key         _timestamp") {
		t.Errorf("expected the configured date key:\n%s", withKey)
	}

	// Unset must keep the previous behaviour, or upgrading silently moves the field every
	// existing HTTP drain writes to.
	defaulted, _ := RenderFluentBitConfig([]DrainTarget{{Drain: httpDrain("plain", nil)}})
	if !strings.Contains(defaulted, "json_date_key         timestamp") {
		t.Errorf("the default must not change:\n%s", defaulted)
	}
}

// Exclusions are how a project-wide drain skips one app. The list lives on the drain
// because Fluent Bit has no negative Match: leaving an app out means enumerating the ones
// that remain, which is only computable from the drain's side.
func TestResolveExclusions(t *testing.T) {
	allApps := []string{
		"shop-production/api", "shop-production/web",
		"shop-staging/api", "utility-prod/vesta-deploy",
	}

	t.Run("a bare app name excludes it everywhere in scope", func(t *testing.T) {
		got := resolveExclusions(vestav1alpha1.VestaLogDrainSpec{ExcludeApps: []string{"api"}}, allApps)
		want := []string{"shop-production/api", "shop-staging/api"}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("qualifying with the namespace narrows it to one", func(t *testing.T) {
		// Two projects can run an app of the same name; a bare name would exclude both.
		got := resolveExclusions(vestav1alpha1.VestaLogDrainSpec{
			ExcludeApps: []string{"shop-staging/api"}}, allApps)
		if len(got) != 1 || got[0] != "shop-staging/api" {
			t.Errorf("got %v, want [shop-staging/api]", got)
		}
	})

	t.Run("an exclusion outside the drain's scope is ignored", func(t *testing.T) {
		// Excluding an app a project drain never covered must not silently widen anything.
		got := resolveExclusions(vestav1alpha1.VestaLogDrainSpec{
			Project: "shop", ExcludeApps: []string{"vesta-deploy"}}, allApps)
		if len(got) != 0 {
			t.Errorf("got %v, want none", got)
		}
	})

	t.Run("no exclusions costs nothing", func(t *testing.T) {
		if got := resolveExclusions(vestav1alpha1.VestaLogDrainSpec{}, allApps); got != nil {
			t.Errorf("got %v, want nil so the common case keeps the cheap Match", got)
		}
	})
}
