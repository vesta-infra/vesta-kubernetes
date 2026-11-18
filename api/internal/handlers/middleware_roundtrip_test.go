package handlers

import "testing"

// What the UI renders back into the textarea must parse again. If it does not, editing an
// existing raw middleware fails on save -- the round trip, not either half, is the feature.
func TestUIRenderedYAMLParsesBack(t *testing.T) {
	for _, rendered := range []string{
		"forwardAuth:\n  address: http://coraza.credpal-prod.svc.cluster.local:9080\n  trustForwardHeader: true\n  authRequestHeaders:\n    - X-Real-Ip\n    - Cookie",
		"rateLimit:\n  average: 100\n  period: 1m",
		"compress: {}",
		"headers:\n  customRequestHeaders:\n    X-Custom: \"true\"",
		"stripPrefix:\n  prefixes:\n    - /api",
	} {
		got, err := parseRawMiddleware(rendered)
		if err != nil {
			t.Errorf("rendered YAML did not parse back:\n%s\nerror: %v", rendered, err)
			continue
		}
		if len(got) != 1 {
			t.Errorf("expected one middleware type, got %d from:\n%s", len(got), rendered)
		}
	}
}
