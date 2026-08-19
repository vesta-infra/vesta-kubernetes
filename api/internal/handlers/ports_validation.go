package handlers

import (
	"fmt"
	"regexp"
)

// Kubernetes rejects a Deployment or Service whose ports repeat a name, and the
// rejection happens in the operator's reconcile loop -- long after the API call
// succeeded. That leaves an app permanently un-reconcilable with the reason
// buried in controller logs, so the same rules are enforced here, at the door,
// where the caller still gets a usable error.
//
// Rules mirror Kubernetes' own IANA_SVC_NAME validation: at most 15 characters,
// lowercase alphanumeric and '-', no leading/trailing '-', at least one letter.
var portNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
var portNameHasLetter = regexp.MustCompile(`[a-z]`)

func validatePortName(name string) error {
	switch {
	case len(name) > 15:
		return fmt.Errorf("must be 15 characters or fewer")
	case !portNamePattern.MatchString(name):
		return fmt.Errorf("must be lowercase alphanumeric or '-', and start and end with alphanumeric")
	case !portNameHasLetter.MatchString(name):
		return fmt.Errorf("must contain at least one letter")
	}
	return nil
}

// validateServicePorts checks a service block from a create/update payload.
// field names the block being checked so the message points at the right place
// (for example "service" or "environments[1].config.service").
func validateServicePorts(service map[string]interface{}, field string) error {
	if service == nil {
		return nil
	}
	rawPorts, ok := service["ports"].([]interface{})
	if !ok || len(rawPorts) == 0 {
		return nil
	}

	seenNames := map[string]int{}
	seenPorts := map[string]int{}

	for i, raw := range rawPorts {
		port, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s.ports[%d]: must be an object", field, i)
		}

		name, _ := port["name"].(string)
		if name == "" {
			// A single unnamed port is legal; more than one must be named so the
			// entries stay distinguishable.
			if len(rawPorts) > 1 {
				return fmt.Errorf("%s.ports[%d].name: required when more than one port is defined", field, i)
			}
		} else {
			if err := validatePortName(name); err != nil {
				return fmt.Errorf("%s.ports[%d].name %q: %v", field, i, name, err)
			}
			if prev, dup := seenNames[name]; dup {
				return fmt.Errorf("%s.ports[%d].name: duplicate port name %q (already used by ports[%d]); each port needs its own name", field, i, name, prev)
			}
			seenNames[name] = i
		}

		number, err := portNumber(port["port"])
		if err != nil {
			return fmt.Errorf("%s.ports[%d].port: %v", field, i, err)
		}

		protocol, _ := port["protocol"].(string)
		if protocol == "" {
			protocol = "TCP"
		}
		switch protocol {
		case "TCP", "UDP", "SCTP":
		default:
			return fmt.Errorf("%s.ports[%d].protocol: must be TCP, UDP, or SCTP", field, i)
		}

		key := fmt.Sprintf("%d/%s", number, protocol)
		if prev, dup := seenPorts[key]; dup {
			return fmt.Errorf("%s.ports[%d].port: %d/%s is already exposed by ports[%d]", field, i, number, protocol, prev)
		}
		seenPorts[key] = i

		if target, present := port["targetPort"]; present && target != nil {
			if _, err := portNumber(target); err != nil {
				return fmt.Errorf("%s.ports[%d].targetPort: %v", field, i, err)
			}
		}
		if node, present := port["nodePort"]; present && node != nil {
			n, err := portNumber(node)
			if err != nil {
				return fmt.Errorf("%s.ports[%d].nodePort: %v", field, i, err)
			}
			if n < 30000 || n > 32767 {
				return fmt.Errorf("%s.ports[%d].nodePort: must be between 30000 and 32767", field, i)
			}
		}
	}

	return nil
}

// portNumber accepts the numeric shapes JSON decoding can produce.
func portNumber(v interface{}) (int64, error) {
	var n int64
	switch t := v.(type) {
	case float64:
		n = int64(t)
	case int64:
		n = t
	case int:
		n = int64(t)
	case nil:
		return 0, fmt.Errorf("required")
	default:
		return 0, fmt.Errorf("must be a number")
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("must be between 1 and 65535")
	}
	return n, nil
}

// validateAppServiceConfig checks every service block an app payload can carry:
// the app-level one and any per-environment override.
func validateAppServiceConfig(service map[string]interface{}, environments []interface{}) error {
	if err := validateServicePorts(service, "service"); err != nil {
		return err
	}
	for i, rawEnv := range environments {
		env, ok := rawEnv.(map[string]interface{})
		if !ok {
			continue
		}
		envService, _ := env["service"].(map[string]interface{})
		if envService == nil {
			if config, ok := env["config"].(map[string]interface{}); ok {
				envService, _ = config["service"].(map[string]interface{})
			}
		}
		label := fmt.Sprintf("environments[%d].service", i)
		if name, ok := env["name"].(string); ok && name != "" {
			label = fmt.Sprintf("environments[%q].service", name)
		}
		if err := validateServicePorts(envService, label); err != nil {
			return err
		}
	}
	return nil
}
