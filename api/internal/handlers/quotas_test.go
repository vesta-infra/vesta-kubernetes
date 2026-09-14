package handlers

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The quota status the UI renders is produced by the operator, in another module, and reaches
// the browser as untyped JSON passed straight through this handler. Nothing in the compiler
// connects the two -- so a renamed field is not a build error, it is a banner that silently
// never renders.
//
// That is exactly what happened: the UI read `status.exceeds` while the operator writes
// `wouldExceed`, so the warning that a quota is below what is already committed -- the single
// most important thing this feature has to say -- was invisible.
func TestQuotaStatusFieldNamesAgreeAcrossModules(t *testing.T) {
	goSrc, err := os.ReadFile("../../../operator/api/v1alpha1/types.go")
	if err != nil {
		t.Skipf("operator source not available: %v", err)
	}
	tsSrc, err := os.ReadFile("../../../ui/src/lib/api.ts")
	if err != nil {
		t.Skipf("ui source not available: %v", err)
	}

	block := between(string(goSrc), "type QuotaStatus struct {", "\n}")
	if block == "" {
		t.Fatal("could not find QuotaStatus in the operator types")
	}

	tags := regexp.MustCompile(`json:"([a-zA-Z]+)`)
	ts := string(tsSrc)

	var found int
	for _, m := range tags.FindAllStringSubmatch(block, -1) {
		name := m[1]
		found++
		// Matched as a declared property rather than as a substring: "used" and "reason"
		// occur all over ordinary prose, and a guard that they appear *somewhere* in a
		// 1200-line file would pass no matter what the UI actually reads.
		declared := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\??:`)
		if !declared.MatchString(ts) {
			t.Errorf("QuotaStatus.%s is written by the operator but is not declared in the UI's api.ts; "+
				"if the UI reads a different spelling, that field silently renders as undefined", name)
		}
	}
	if found == 0 {
		t.Fatal("parsed no json tags out of QuotaStatus; this guard is not testing anything")
	}

	// Negative control: prove the matcher rejects a name the UI does not declare, so a
	// passing run means something.
	absent := regexp.MustCompile(`(?m)^\s*quotaFieldThatDoesNotExist\??:`)
	if absent.MatchString(ts) {
		t.Fatal("the negative control matched; this guard proves nothing")
	}
}

func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	s = s[i+len(start):]
	j := strings.Index(s, end)
	if j < 0 {
		return ""
	}
	return s[:j]
}
