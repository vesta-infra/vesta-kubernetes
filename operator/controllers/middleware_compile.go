package controllers

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// middlewareProjectionPrefix names the Traefik Middleware objects the operator projects
// from a VestaMiddleware. The prefix keeps them from colliding with the two objects the
// controller has always created for itself -- "<app>-https-redirect" and the per-env
// redirect named after its Ingress -- which share these namespaces and would otherwise be
// silently overwritten by a VestaMiddleware that happened to pick the same name.
const middlewareProjectionPrefix = "vmw-"

// projectedMiddlewareName is the name of the Traefik Middleware projected from the named
// VestaMiddleware. It is deterministic so that the garbage collector can recognise its own
// output without needing to have recorded it.
func projectedMiddlewareName(vestaMiddlewareName string) string {
	return middlewareProjectionPrefix + vestaMiddlewareName
}

// traefikMiddlewareRef is the value Traefik expects in a router.middlewares annotation:
// namespace and object name joined by a dash, suffixed with the provider.
func traefikMiddlewareRef(namespace, name string) string {
	return fmt.Sprintf("%s-%s@kubernetescrd", namespace, name)
}

// compileMiddleware turns a VestaMiddleware spec into the spec of a Traefik Middleware.
//
// It is deliberately strict about the typed field matching Type. Accepting a rateLimit
// body under type "headers" would produce a Middleware that Traefik loads and that does
// nothing -- the failure would surface as an absent rate limit under load, long after the
// change, with nothing anywhere saying why. Refusing here puts the reason in the resource's
// status instead.
func compileMiddleware(spec vestav1alpha1.VestaMiddlewareSpec) (map[string]interface{}, error) {
	// out wraps the compiled body under the Traefik key for the type. Helper keeps the
	// switch below to one line per type.
	out := func(key string, body interface{}) (map[string]interface{}, error) {
		encoded, err := toMap(body)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{key: encoded}, nil
	}

	switch spec.Type {
	case "rateLimit":
		if spec.RateLimit == nil {
			return nil, missingBody("rateLimit")
		}
		return out("rateLimit", spec.RateLimit)

	case "basicAuth":
		if spec.BasicAuth == nil {
			return nil, missingBody("basicAuth")
		}
		if spec.BasicAuth.SecretName == "" {
			return nil, fmt.Errorf("basicAuth requires secretName: credentials are read from a Secret, never stored on this resource")
		}
		// Traefik reads htpasswd lines from the Secret itself; the key is fixed to "users"
		// by Traefik's own CRD, so SecretKey is accepted for forward compatibility but is
		// not part of the emitted spec.
		body := map[string]interface{}{"secret": spec.BasicAuth.SecretName}
		if spec.BasicAuth.Realm != "" {
			body["realm"] = spec.BasicAuth.Realm
		}
		if spec.BasicAuth.RemoveHeader {
			body["removeHeader"] = true
		}
		return map[string]interface{}{"basicAuth": body}, nil

	case "ipAllowList":
		if spec.IPAllowList == nil {
			return nil, missingBody("ipAllowList")
		}
		if len(spec.IPAllowList.SourceRange) == 0 {
			// An empty allowList rejects every request. That is a plausible thing to want
			// and an extremely implausible thing to have meant by leaving the field blank.
			return nil, fmt.Errorf("ipAllowList requires at least one sourceRange: an empty list rejects all traffic")
		}
		return out("ipAllowList", spec.IPAllowList)

	case "headers":
		if spec.Headers == nil {
			return nil, missingBody("headers")
		}
		return out("headers", spec.Headers)

	case "stripPrefix":
		if spec.StripPrefix == nil {
			return nil, missingBody("stripPrefix")
		}
		if len(spec.StripPrefix.Prefixes) == 0 {
			return nil, fmt.Errorf("stripPrefix requires at least one prefix")
		}
		return out("stripPrefix", spec.StripPrefix)

	case "compress":
		if spec.Compress == nil {
			// Compress is the one type whose zero value is meaningful: Traefik's defaults
			// compress sensibly, so an empty body is a complete configuration.
			return map[string]interface{}{"compress": map[string]interface{}{}}, nil
		}
		return out("compress", spec.Compress)

	case "retry":
		if spec.Retry == nil {
			return nil, missingBody("retry")
		}
		if spec.Retry.Attempts <= 0 {
			return nil, fmt.Errorf("retry requires attempts greater than zero")
		}
		return out("retry", spec.Retry)

	case "circuitBreaker":
		if spec.CircuitBreaker == nil {
			return nil, missingBody("circuitBreaker")
		}
		if spec.CircuitBreaker.Expression == "" {
			return nil, fmt.Errorf("circuitBreaker requires an expression")
		}
		return out("circuitBreaker", spec.CircuitBreaker)

	case "buffering":
		if spec.Buffering == nil {
			return nil, missingBody("buffering")
		}
		return out("buffering", spec.Buffering)

	case "raw":
		if spec.Raw == nil || len(spec.Raw.Raw) == 0 {
			return nil, missingBody("raw")
		}
		var decoded map[string]interface{}
		if err := json.Unmarshal(spec.Raw.Raw, &decoded); err != nil {
			return nil, fmt.Errorf("raw must be a JSON object: %w", err)
		}
		// Traefik's Middleware spec is a one-of: exactly one key names the middleware
		// type. Two keys is not "both apply", it is undefined, so reject it here where
		// the author can still see the message.
		if len(decoded) != 1 {
			return nil, fmt.Errorf("raw must contain exactly one middleware type, found %d: %s",
				len(decoded), sortedKeys(decoded))
		}
		return decoded, nil
	}

	return nil, fmt.Errorf("unknown middleware type %q", spec.Type)
}

func missingBody(field string) error {
	return fmt.Errorf("type is %q but the %s block is empty", field, field)
}

// toMap round-trips through JSON so the struct's json tags -- and their omitempty -- decide
// the emitted shape. Writing the map by hand would mean a second place that has to agree
// with the tags about what an unset field looks like, and Traefik treats an explicit zero
// differently from an absent field for several of these.
func toMap(v interface{}) (map[string]interface{}, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolveAppMiddlewares returns the middleware names applying to one environment.
// A nil per-environment list inherits the app-level list; a non-nil one replaces it,
// including when it is empty -- that is how an environment opts out entirely.
func resolveAppMiddlewares(app *vestav1alpha1.VestaApp, env vestav1alpha1.AppEnvironmentConfig) []string {
	if env.Ingress != nil && env.Ingress.Middlewares != nil {
		return *env.Ingress.Middlewares
	}
	if app.Spec.Ingress != nil {
		return app.Spec.Ingress.Middlewares
	}
	return nil
}

// composeMiddlewareAnnotation builds the whole router.middlewares value.
//
// It replaces an earlier string append that bolted the HTTPS-redirect reference onto
// whatever was already in the annotation. That produced duplicates whenever the existing
// value already named it, and it put the redirect last.
//
// Order is the entire semantic content of this annotation -- Traefik runs middlewares in
// the order listed -- so this function fixes one:
//
//  1. platform middlewares (the HTTPS and domain redirects) first, because a request about
//     to be 301'd should not spend rate-limit budget or be asked for a password;
//  2. then the app's own middlewares, in the order the app declared them;
//  3. then anything the user typed into the raw annotations editor, which is preserved
//     rather than overwritten;
//  4. deduplicated throughout, keeping the first occurrence, since a middleware listed
//     twice runs twice.
func composeMiddlewareAnnotation(platform, app, existing []string) string {
	seen := make(map[string]bool, len(platform)+len(app)+len(existing))
	ordered := make([]string, 0, len(platform)+len(app)+len(existing))

	for _, group := range [][]string{platform, app, existing} {
		for _, ref := range group {
			ref = strings.TrimSpace(ref)
			if ref == "" || seen[ref] {
				continue
			}
			seen[ref] = true
			ordered = append(ordered, ref)
		}
	}
	return strings.Join(ordered, ",")
}

// isVestaManagedMiddlewareRef reports whether a router.middlewares entry is one Vesta
// writes for itself: the app's HTTPS redirect, its domain-redirect middleware, or a
// projection of a VestaMiddleware.
//
// It exists so the composer can rebuild its own references from scratch on every reconcile
// while leaving alone anything a user added by hand. Without the distinction the two
// choices are both wrong: carry every existing reference forward and a withdrawn middleware
// is referenced forever, or overwrite the annotation wholesale and a hand-added reference
// silently disappears.
func isVestaManagedMiddlewareRef(ref, namespace, appName string) bool {
	ref = strings.TrimSpace(ref)
	prefix := namespace + "-"
	if !strings.HasPrefix(ref, prefix) {
		// A reference into another namespace is never one of ours: everything Vesta
		// projects lands in the namespace of the Ingress that names it.
		return false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(ref, prefix), "@kubernetescrd")

	return name == appName+"-https-redirect" ||
		name == appName ||
		strings.HasPrefix(name, middlewareProjectionPrefix)
}
