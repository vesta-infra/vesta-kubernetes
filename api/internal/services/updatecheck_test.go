package services

import "testing"

// String comparison puts "0.10.0" before "0.9.0", which would hide exactly the upgrade a
// user most wants to see. The comparison has to be numeric, field by field.
func TestIsNewerVersionComparesNumerically(t *testing.T) {
	if !IsNewerVersion("0.9.0", "0.10.0") {
		t.Error("0.10.0 must be newer than 0.9.0; a lexicographic compare gets this wrong")
	}
	if IsNewerVersion("0.10.0", "0.9.0") {
		t.Error("0.9.0 must not be newer than 0.10.0")
	}
}

func TestIsNewerVersionAcrossFields(t *testing.T) {
	for _, c := range []struct {
		current, candidate string
		want               bool
	}{
		{"1.0.0", "2.0.0", true},
		{"1.2.3", "1.3.0", true},
		{"1.2.3", "1.2.4", true},
		{"1.2.3", "1.2.3", false},
		{"2.0.0", "1.9.9", false},
		{"1.2.3", "1.2.2", false},
	} {
		if got := IsNewerVersion(c.current, c.candidate); got != c.want {
			t.Errorf("IsNewerVersion(%q, %q) = %v, want %v", c.current, c.candidate, got, c.want)
		}
	}
}

// Release tags carry a leading v; the settings table and image tags do not.
func TestIsNewerVersionIgnoresLeadingV(t *testing.T) {
	if !IsNewerVersion("v1.2.3", "v1.2.4") {
		t.Error("a leading v must not change the ordering")
	}
	if !IsNewerVersion("1.2.3", "v1.2.4") {
		t.Error("mixed v-prefixes must still compare")
	}
}

// A development build has no place in the release ordering. Reporting it as older than
// every release would show a permanent "update available" on every developer's machine.
func TestIsNewerVersionRejectsUnparseable(t *testing.T) {
	for _, c := range [][2]string{
		{"dev", "1.0.0"},
		{"1.0.0", "dev"},
		{"", "1.0.0"},
		{"main", "1.0.0"},
		{"1.0", "1.0.1"},
	} {
		if IsNewerVersion(c[0], c[1]) {
			t.Errorf("IsNewerVersion(%q, %q) = true, want false for an unparseable version", c[0], c[1])
		}
	}
}

// Pre-release suffixes order on their numeric core rather than being rejected outright.
func TestIsNewerVersionHandlesPreReleaseSuffix(t *testing.T) {
	if !IsNewerVersion("1.2.3", "1.2.4-rc.1") {
		t.Error("1.2.4-rc.1 has a newer core than 1.2.3")
	}
}
