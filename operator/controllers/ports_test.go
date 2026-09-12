package controllers

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// The gateway app stored two ports both named "http", which made every reconcile
// fail with: containers[0].ports[1].name: Duplicate value: "http".
func TestBuildContainerPortsDeduplicatesNames(t *testing.T) {
	ports := []vestav1alpha1.ServicePort{
		{Name: "http", Port: 80, TargetPort: 3000},
		{Name: "http", Port: 8080, TargetPort: 4000},
	}

	got := buildContainerPorts(ports)
	if len(got) != 2 {
		t.Fatalf("expected 2 container ports, got %d: %+v", len(got), got)
	}
	if got[0].Name == got[1].Name {
		t.Fatalf("names still collide: %q and %q", got[0].Name, got[1].Name)
	}
	if got[0].Name != "http" {
		t.Errorf("first port should keep its name, got %q", got[0].Name)
	}
	if got[0].ContainerPort != 3000 || got[1].ContainerPort != 4000 {
		t.Errorf("target ports not preserved: %+v", got)
	}
}

func TestBuildContainerPortsCollapsesSameTarget(t *testing.T) {
	// 80 and 443 both forwarding to 3000 is one container port, not two.
	ports := []vestav1alpha1.ServicePort{
		{Name: "http", Port: 80, TargetPort: 3000},
		{Name: "https", Port: 443, TargetPort: 3000},
	}
	got := buildContainerPorts(ports)
	if len(got) != 1 {
		t.Fatalf("expected 1 container port, got %d: %+v", len(got), got)
	}
	if got[0].ContainerPort != 3000 || got[0].Name != "http" {
		t.Errorf("unexpected port: %+v", got[0])
	}
}

func TestBuildServicePortsDeduplicates(t *testing.T) {
	ports := []vestav1alpha1.ServicePort{
		{Name: "http", Port: 80, TargetPort: 3000},
		{Name: "http", Port: 8080, TargetPort: 3000},
		{Name: "http", Port: 80, TargetPort: 9999}, // same port/protocol -> dropped
	}
	got := buildServicePorts(ports)
	if len(got) != 2 {
		t.Fatalf("expected 2 service ports, got %d: %+v", len(got), got)
	}
	seen := map[string]bool{}
	for _, p := range got {
		if seen[p.Name] {
			t.Fatalf("duplicate service port name %q in %+v", p.Name, got)
		}
		seen[p.Name] = true
	}
	if got[0].Port != 80 || got[1].Port != 8080 {
		t.Errorf("unexpected ports: %+v", got)
	}
}

func TestSanitizePortName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http", "http"},
		{"HTTP", "http"},
		{"my_port", "my-port"},
		{"grpc-internal-gateway", "grpc-internal-g"}, // truncated to the 15 char cap
		{"grpc-internal--x", "grpc-internal"},        // trailing dash after truncation is trimmed
		{"", "p-3000"},
		{"8080", "p-3000"}, // needs at least one letter
	}
	for _, c := range cases {
		if got := sanitizePortName(c.in, 3000); got != c.want {
			t.Errorf("sanitizePortName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUniquePortNameRespectsLengthCap(t *testing.T) {
	used := map[string]bool{}
	first := uniquePortName("grpc-internal-x", 1000, used)
	second := uniquePortName("grpc-internal-x", 2000, used)
	if first == second {
		t.Fatalf("names collide: %q", first)
	}
	for _, n := range []string{first, second} {
		if len(n) > 15 {
			t.Errorf("name %q exceeds the 15 character limit", n)
		}
	}
}

func TestBuildContainerPortsSkipsUnusablePorts(t *testing.T) {
	got := buildContainerPorts([]vestav1alpha1.ServicePort{{Name: "bad"}})
	if len(got) != 0 {
		t.Fatalf("expected no ports, got %+v", got)
	}
}

func TestBuildContainerPortsKeepsProtocolSeparation(t *testing.T) {
	got := buildContainerPorts([]vestav1alpha1.ServicePort{
		{Name: "dns", Port: 53, TargetPort: 53, Protocol: "TCP"},
		{Name: "dns", Port: 53, TargetPort: 53, Protocol: "UDP"},
	})
	if len(got) != 2 {
		t.Fatalf("TCP and UDP on the same number are distinct ports, got %+v", got)
	}
	if got[0].Protocol != corev1.ProtocolTCP || got[1].Protocol != corev1.ProtocolUDP {
		t.Errorf("protocols not preserved: %+v", got)
	}
	if got[0].Name == got[1].Name {
		t.Errorf("names still collide: %+v", got)
	}
}
