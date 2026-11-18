package controllers

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// effectiveServicePorts is the port list the Service is actually built from: an
// environment's ports replace the app-level ones, the same merge reconcileService does.
// The ingress has to resolve against this list, not app.Spec.Service, or an environment
// that renames its ports gets an ingress pointing at a port its Service does not have.
func effectiveServicePorts(app *vestav1alpha1.VestaApp, target targetEnv) []vestav1alpha1.ServicePort {
	if target.Config.Service != nil && len(target.Config.Service.Ports) > 0 {
		return target.Config.Service.Ports
	}
	if app.Spec.Service != nil {
		return app.Spec.Service.Ports
	}
	return nil
}

// defaultIngressPort is the port a domain without a domainPorts entry is routed to:
// the one named "http", otherwise the first. With no ports declared it is 80, which is
// what the legacy runtime.port Service listens on.
func defaultIngressPort(ports []vestav1alpha1.ServicePort) int32 {
	if len(ports) == 0 {
		return 80
	}
	for _, p := range ports {
		if p.Name == "http" {
			return p.Port
		}
	}
	return ports[0].Port
}

// effectiveDomainPorts is the domain → port-name map for an environment. An environment
// map replaces the app-level one rather than merging with it, as Domains does.
func effectiveDomainPorts(app *vestav1alpha1.VestaApp, target targetEnv) map[string]string {
	if target.Config.Ingress != nil && target.Config.Ingress.DomainPorts != nil {
		return target.Config.Ingress.DomainPorts
	}
	if app.Spec.Ingress != nil {
		return app.Spec.Ingress.DomainPorts
	}
	return nil
}

// resolveDomainPorts maps each domain to the Service port number its ingress rule
// should target. A domainPorts entry naming a port the Service does not have is an
// error rather than a silent fall back to the default port: routing a domain meant for
// an admin port to the public one is worse than not reconciling.
func resolveDomainPorts(app *vestav1alpha1.VestaApp, target targetEnv, domains []string) (map[string]int32, error) {
	ports := effectiveServicePorts(app, target)
	def := defaultIngressPort(ports)

	byName := make(map[string]int32, len(ports))
	for _, p := range ports {
		if _, dup := byName[p.Name]; !dup {
			byName[p.Name] = p.Port
		}
	}

	// Domains are matched case-insensitively, as hosts are.
	wanted := map[string]string{}
	for d, name := range effectiveDomainPorts(app, target) {
		wanted[strings.ToLower(strings.TrimSpace(d))] = strings.TrimSpace(name)
	}

	out := make(map[string]int32, len(domains))
	for _, d := range domains {
		name, ok := wanted[strings.ToLower(d)]
		if !ok || name == "" {
			out[d] = def
			continue
		}
		port, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("ingress domainPorts: domain %q names service port %q, which spec.service.ports does not declare", d, name)
		}
		out[d] = port
	}
	return out, nil
}

// hostPortsAnnotationValue renders the domain → port map for the activator, which has
// to know each host's real port while the ingress backend is the activator itself.
// Sorted, so an unchanged map never rewrites the Ingress.
func hostPortsAnnotationValue(ports map[string]int32) string {
	hosts := make([]string, 0, len(ports))
	for h := range ports {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	parts := make([]string, 0, len(hosts))
	for _, h := range hosts {
		parts = append(parts, strings.ToLower(h)+"="+strconv.Itoa(int(ports[h])))
	}
	return strings.Join(parts, ",")
}
