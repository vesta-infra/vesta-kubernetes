package handlers

import (
	"fmt"
)

// ClusterIssuerAnnotation is the cert-manager annotation naming an Ingress' issuer.
const ClusterIssuerAnnotation = "cert-manager.io/cluster-issuer"

// errIssuerAnnotation is the message shown when someone sets the issuer through the raw
// annotation map instead of the provider selector.
const errIssuerAnnotation = "set the SSL provider with the certificate provider selector rather than the %s annotation; " +
	"to drive TLS entirely by annotation, set ingress.tlsMode to \"custom-annotations\""

// validateIngressCertAnnotations rejects a request that sets cert-manager.io/cluster-issuer
// through the free-form annotation map.
//
// Before the provider selector existed, that annotation was the only way to choose an
// issuer, and it won because the operator merged user annotations after its own stamp. The
// operator now stamps last, so an annotation left in place would be silently ignored —
// worse than an error. Rejecting the write keeps the two from ever disagreeing again.
//
// Existing values are grandfathered: the key is rejected only when it is being added or
// changed, so an untouched legacy app still saves and can be migrated at leisure.
func validateIngressCertAnnotations(patch map[string]interface{}, existing map[string]interface{}) error {
	patchIngress, _ := patch["ingress"].(map[string]interface{})
	existingIngress, _ := existing["ingress"].(map[string]interface{})

	if err := checkIssuerAnnotation(patchIngress, existingIngress, "ingress"); err != nil {
		return err
	}

	patchEnvs, _ := patch["environments"].([]interface{})
	existingEnvs, _ := existing["environments"].([]interface{})
	existingByName := map[string]map[string]interface{}{}
	for _, raw := range existingEnvs {
		if env, ok := raw.(map[string]interface{}); ok {
			if ing, ok := env["ingress"].(map[string]interface{}); ok {
				existingByName[getNestedString(env, "name")] = ing
			}
		}
	}

	for _, raw := range patchEnvs {
		env, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name := getNestedString(env, "name")
		envIngress, _ := env["ingress"].(map[string]interface{})
		if err := checkIssuerAnnotation(envIngress, existingByName[name], fmt.Sprintf("environment %q ingress", name)); err != nil {
			return err
		}
	}

	return nil
}

func checkIssuerAnnotation(incoming, existing map[string]interface{}, where string) error {
	if incoming == nil {
		return nil
	}
	annotations, _ := incoming["annotations"].(map[string]interface{})
	value, present := annotations[ClusterIssuerAnnotation]
	if !present {
		return nil
	}

	// Unchanged from what is already stored — grandfathered.
	var previous interface{}
	if existing != nil {
		if prevAnnotations, ok := existing["annotations"].(map[string]interface{}); ok {
			previous = prevAnnotations[ClusterIssuerAnnotation]
		}
	}
	if previous != nil && previous == value {
		return nil
	}

	return fmt.Errorf("%s: "+errIssuerAnnotation, where, ClusterIssuerAnnotation)
}
