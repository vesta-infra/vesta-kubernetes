package controllers

import (
	"strings"
	"testing"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

func drainSpec(project string, projects []string, env, app string) vestav1alpha1.VestaLogDrainSpec {
	return vestav1alpha1.VestaLogDrainSpec{
		Project: project, Projects: projects, Environment: env, App: app,
	}
}

// A drain usually belongs to a team rather than to one project. Covering four of them meant
// four drains pointed at the same destination, each with its own credentials to rotate and
// its own status to read.
func TestDrainCoversSeveralProjects(t *testing.T) {
	spec := drainSpec("", []string{"shop", "billing"}, "", "")

	for _, pair := range []string{"shop-production/web", "billing-staging/api"} {
		if !scopeCovers(spec, pair) {
			t.Errorf("%s is not covered but its project is in scope", pair)
		}
	}
	if scopeCovers(spec, "warehouse-production/web") {
		t.Error("an app outside every named project is covered")
	}
}

// The single field still works, and adding to the list does not mean rewriting it: both are
// honoured together, so an existing drain keeps shipping exactly what it did.
func TestSingularAndPluralAreUnioned(t *testing.T) {
	spec := drainSpec("shop", []string{"billing"}, "", "")

	got := scopeProjects(spec)
	if len(got) != 2 {
		t.Fatalf("scopeProjects = %v, want both", got)
	}
	for _, pair := range []string{"shop-production/web", "billing-production/api"} {
		if !scopeCovers(spec, pair) {
			t.Errorf("%s is not covered", pair)
		}
	}
}

// Naming the same project twice must not produce it twice: the list drives namespace
// enumeration, and a duplicate would have Fluent Bit matching the same tag more than once.
func TestProjectsAreDeduplicated(t *testing.T) {
	got := scopeProjects(drainSpec("shop", []string{"shop", "billing", "shop"}, "", ""))
	if len(got) != 2 {
		t.Errorf("scopeProjects = %v, want shop and billing once each", got)
	}
}

// No project anywhere is still platform-wide, which is what an existing drain with nothing
// set relies on.
func TestNoProjectIsStillEverything(t *testing.T) {
	spec := drainSpec("", nil, "", "")
	if len(scopeProjects(spec)) != 0 {
		t.Error("an unscoped drain reported projects")
	}
	if !scopeCovers(spec, "anything-production/web") {
		t.Error("an unscoped drain stopped covering everything")
	}
}

// The environment narrows every project in the list rather than only the first.
func TestEnvironmentAppliesToEveryProject(t *testing.T) {
	spec := drainSpec("", []string{"shop", "billing"}, "production", "")

	for _, pair := range []string{"shop-production/web", "billing-production/api"} {
		if !scopeCovers(spec, pair) {
			t.Errorf("%s is not covered", pair)
		}
	}
	for _, pair := range []string{"shop-staging/web", "billing-staging/api"} {
		if scopeCovers(spec, pair) {
			t.Errorf("%s is covered but the drain names only production", pair)
		}
	}
}

// The prefix check must not let a project be matched by one whose name starts the same way.
func TestSimilarProjectNamesDoNotCollide(t *testing.T) {
	spec := drainSpec("", []string{"shop"}, "", "")
	if scopeCovers(spec, "shopfront-production/web") {
		t.Error("shopfront matched a drain scoped to shop")
	}
	if !scopeCovers(spec, "shop-production/web") {
		t.Error("shop-production did not match a drain scoped to shop")
	}
}

// Status names the projects rather than counting them: "3 projects" is the start of a
// question instead of the end of one.
func TestScopeDescriptionNamesThem(t *testing.T) {
	got := describeScope(drainSpec("", []string{"shop", "billing"}, "", ""))
	for _, want := range []string{"shop", "billing"} {
		if !strings.Contains(got, want) {
			t.Errorf("describeScope = %q, missing %q", got, want)
		}
	}
	if single := describeScope(drainSpec("shop", nil, "", "")); single != "project shop" {
		t.Errorf("a one-project drain reads %q", single)
	}
}
