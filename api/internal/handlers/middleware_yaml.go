package handlers

import (
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

// parseRawMiddleware turns pasted text into a Traefik middleware spec.
//
// It accepts what people actually have in front of them: a whole Middleware manifest
// copied from Traefik's documentation or from `kubectl get middleware -o yaml`, or just
// the spec body, in YAML or in JSON. Requiring one exact shape would mean everyone hand-
// editing a manifest down to its spec before pasting it, which is both annoying and a
// good way to drop a field.
//
// JSON needs no special case: sigs.k8s.io/yaml parses YAML by converting it to JSON, and
// JSON is already valid YAML.
func parseRawMiddleware(raw string) (map[string]interface{}, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("the configuration is empty")
	}

	// A leading document separator is normal in copied YAML and harmless to drop.
	raw = strings.TrimPrefix(raw, "---\n")

	// Multiple documents cannot become one middleware, and silently taking the first
	// would discard the rest without saying so.
	if strings.Contains(raw, "\n---\n") {
		return nil, fmt.Errorf("paste a single middleware, not a multi-document YAML file")
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("could not parse as YAML or JSON: %w", err)
	}
	if len(parsed) == 0 {
		return nil, fmt.Errorf("the configuration is empty")
	}

	// A full manifest: take its spec, and check it is the right kind before doing so. An
	// IngressRoute pasted here would otherwise be accepted and produce a Middleware whose
	// body Traefik cannot interpret.
	if kind, ok := parsed["kind"].(string); ok {
		if !strings.EqualFold(kind, "Middleware") {
			return nil, fmt.Errorf("that is a %s, not a Middleware", kind)
		}
		spec, ok := parsed["spec"].(map[string]interface{})
		if !ok || len(spec) == 0 {
			return nil, fmt.Errorf("the Middleware has no spec")
		}
		return spec, nil
	}

	// Otherwise it is the spec itself. Catch the near-miss where someone pasted a manifest
	// with the kind line trimmed off, which would otherwise fail later with a confusing
	// message about apiVersion not being a middleware type.
	if _, hasAPIVersion := parsed["apiVersion"]; hasAPIVersion {
		if spec, ok := parsed["spec"].(map[string]interface{}); ok && len(spec) > 0 {
			return spec, nil
		}
	}

	return parsed, nil
}
