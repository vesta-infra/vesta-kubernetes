package handlers

import "testing"

func ports(p ...map[string]interface{}) map[string]interface{} {
	list := make([]interface{}, len(p))
	for i, v := range p {
		list[i] = v
	}
	return map[string]interface{}{"ports": list}
}

func TestValidateServicePortsRejectsDuplicateNames(t *testing.T) {
	// The exact payload that wedged the gateway app's reconcile loop.
	svc := ports(
		map[string]interface{}{"name": "http", "port": float64(80), "targetPort": float64(3000)},
		map[string]interface{}{"name": "http", "port": float64(8080), "targetPort": float64(4000)},
	)
	err := validateServicePorts(svc, "service")
	if err == nil {
		t.Fatal("expected duplicate port names to be rejected")
	}
	t.Log(err)
}

func TestValidateServicePortsAcceptsDistinctNames(t *testing.T) {
	svc := ports(
		map[string]interface{}{"name": "http", "port": float64(80), "targetPort": float64(3000)},
		map[string]interface{}{"name": "grpc", "port": float64(8080), "targetPort": float64(4000)},
	)
	if err := validateServicePorts(svc, "service"); err != nil {
		t.Fatalf("expected valid ports to pass, got %v", err)
	}
}

func TestValidateServicePortsRequiresNameWhenMultiple(t *testing.T) {
	svc := ports(
		map[string]interface{}{"port": float64(80)},
		map[string]interface{}{"port": float64(8080)},
	)
	if err := validateServicePorts(svc, "service"); err == nil {
		t.Fatal("expected unnamed ports to be rejected when more than one is defined")
	}

	single := ports(map[string]interface{}{"port": float64(80)})
	if err := validateServicePorts(single, "service"); err != nil {
		t.Fatalf("a single unnamed port should be allowed, got %v", err)
	}
}

func TestValidateServicePortsRejectsDuplicatePortNumbers(t *testing.T) {
	svc := ports(
		map[string]interface{}{"name": "http", "port": float64(80)},
		map[string]interface{}{"name": "http-alt", "port": float64(80)},
	)
	if err := validateServicePorts(svc, "service"); err == nil {
		t.Fatal("expected the same port/protocol pair to be rejected")
	}

	dns := ports(
		map[string]interface{}{"name": "dns-tcp", "port": float64(53), "protocol": "TCP"},
		map[string]interface{}{"name": "dns-udp", "port": float64(53), "protocol": "UDP"},
	)
	if err := validateServicePorts(dns, "service"); err != nil {
		t.Fatalf("TCP and UDP on one number should be allowed, got %v", err)
	}
}

func TestValidatePortName(t *testing.T) {
	valid := []string{"http", "grpc-web", "p-8080", "a"}
	for _, n := range valid {
		if err := validatePortName(n); err != nil {
			t.Errorf("%q should be valid: %v", n, err)
		}
	}
	invalid := []string{"HTTP", "my_port", "-http", "http-", "8080", "this-name-is-too-long"}
	for _, n := range invalid {
		if err := validatePortName(n); err == nil {
			t.Errorf("%q should be rejected", n)
		}
	}
}

func TestValidateServicePortsChecksRanges(t *testing.T) {
	if err := validateServicePorts(ports(map[string]interface{}{"name": "http", "port": float64(0)}), "service"); err == nil {
		t.Error("port 0 should be rejected")
	}
	if err := validateServicePorts(ports(map[string]interface{}{"name": "http", "port": float64(70000)}), "service"); err == nil {
		t.Error("port above 65535 should be rejected")
	}
	if err := validateServicePorts(ports(map[string]interface{}{"name": "http", "port": float64(80), "nodePort": float64(8080)}), "service"); err == nil {
		t.Error("nodePort outside 30000-32767 should be rejected")
	}
	if err := validateServicePorts(ports(map[string]interface{}{"name": "http", "port": float64(80), "protocol": "HTTP"}), "service"); err == nil {
		t.Error("an unknown protocol should be rejected")
	}
}

func TestValidateAppServiceConfigChecksEnvironmentOverrides(t *testing.T) {
	envs := []interface{}{
		map[string]interface{}{
			"name": "staging",
			"config": map[string]interface{}{
				"service": ports(
					map[string]interface{}{"name": "http", "port": float64(80)},
					map[string]interface{}{"name": "http", "port": float64(8080)},
				),
			},
		},
	}
	err := validateAppServiceConfig(nil, envs)
	if err == nil {
		t.Fatal("expected a per-environment service override to be validated")
	}
	t.Log(err)
}

func TestValidateServicePortsAllowsEmptyService(t *testing.T) {
	if err := validateServicePorts(nil, "service"); err != nil {
		t.Errorf("nil service should pass: %v", err)
	}
	if err := validateServicePorts(map[string]interface{}{}, "service"); err != nil {
		t.Errorf("service without ports should pass: %v", err)
	}
}
