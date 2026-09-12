package cmd

import (
	"strings"
	"testing"
)

func args(action string, version, database, ingress string, postgres bool) string {
	return strings.Join(helmArgs(action, "vesta-system", "vesta", version, database, ingress, postgres), " ")
}

// --reuse-values carries the old values forward wholesale and silently drops new chart
// defaults, which is how an upgrade ends up running new images against the previous
// release's configuration. --reset-then-reuse-values is the one that does not.
func TestUpgradeUsesResetThenReuseValues(t *testing.T) {
	got := args("upgrade", "0.7.0", "", "", false)
	if !strings.Contains(got, "--reset-then-reuse-values") {
		t.Errorf("upgrade must use --reset-then-reuse-values, got: %s", got)
	}
	if strings.Contains(got, "--reuse-values ") || strings.HasSuffix(got, "--reuse-values") {
		t.Errorf("upgrade must not use bare --reuse-values, got: %s", got)
	}
}

// Install has no previous release to carry values from, so re-applying them there would
// be meaningless rather than dangerous.
func TestInstallDoesNotReuseValues(t *testing.T) {
	if got := args("install", "", "", "", true); strings.Contains(got, "reuse-values") {
		t.Errorf("install must not pass a reuse-values flag, got: %s", got)
	}
}

// Re-specifying configuration on upgrade is how an upgrade quietly changes settings
// nobody meant to change, so the upgrade path must not emit --set at all.
func TestUpgradeCarriesNoConfiguration(t *testing.T) {
	got := args("upgrade", "0.7.0", "postgres://u:p@h/db", "traefik", true)
	if strings.Contains(got, "--set") {
		t.Errorf("upgrade must not re-specify configuration, got: %s", got)
	}
}

func TestInstallWiresDatabaseAndIngress(t *testing.T) {
	got := args("install", "", "postgres://u:p@h/db", "traefik", false)
	if !strings.Contains(got, "--set-string api.database.url=postgres://u:p@h/db") {
		t.Errorf("database url not wired: %s", got)
	}
	if !strings.Contains(got, "--set-string config.ingressClassName=traefik") {
		t.Errorf("ingress class not wired: %s", got)
	}
	// A connection string can contain characters helm's --set parses as structure, so it
	// has to go through --set-string.
	if strings.Contains(got, "--set api.database.url") {
		t.Error("a connection string must be passed with --set-string, not --set")
	}
}

// An explicit database wins: deploying a bundled Postgres alongside one the user named
// would leave an unused StatefulSet and a PVC nobody asked for.
func TestExplicitDatabaseBeatsBundledPostgres(t *testing.T) {
	got := args("install", "", "postgres://u:p@h/db", "", true)
	if strings.Contains(got, "postgres.enabled=true") {
		t.Errorf("an explicit database must suppress the bundled one, got: %s", got)
	}
}

// `vesta upgrade` on a cluster that never had Vesta should install it, which is what
// someone re-running it in CI expects.
func TestUpgradeIsIdempotentFromNothing(t *testing.T) {
	if got := args("upgrade", "", "", "", false); !strings.Contains(got, "upgrade --install") {
		t.Errorf("upgrade must pass --install, got: %s", got)
	}
}

// The chart reference is a constant. Accepting one from the caller would turn
// `vesta install` into "run any helm chart against my cluster".
func TestChartReferenceIsFixed(t *testing.T) {
	if !strings.HasPrefix(chartRef, "oci://ghcr.io/vesta-infra/") {
		t.Errorf("chartRef = %q, want the official registry", chartRef)
	}
	if !strings.Contains(args("install", "", "", "", true), chartRef) {
		t.Error("install must use the fixed chart reference")
	}
}
