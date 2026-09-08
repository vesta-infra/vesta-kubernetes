package mfa

import "testing"

func TestResolveRelyingParty(t *testing.T) {
	cases := []struct {
		name       string
		origin     string
		host       string
		tls        bool
		allowlist  []string
		wantID     string
		wantOrigin string
		wantErr    bool
	}{
		{
			name: "https origin", origin: "https://vesta.example.com", host: "vesta.example.com",
			wantID: "vesta.example.com", wantOrigin: "https://vesta.example.com",
		},
		{
			name:   "origin with port keeps it in origin but not in id",
			origin: "https://vesta.example.com:8443", host: "vesta.example.com:8443",
			wantID: "vesta.example.com", wantOrigin: "https://vesta.example.com:8443",
		},
		{
			name: "localhost over http is allowed", origin: "http://localhost:3000", host: "localhost:8090",
			wantID: "localhost", wantOrigin: "http://localhost:3000",
		},
		{
			name: "127.0.0.1 is a secure context", origin: "http://127.0.0.1:3000", host: "127.0.0.1:3000",
			wantID: "127.0.0.1", wantOrigin: "http://127.0.0.1:3000",
		},
		{
			name: "uppercase is normalised", origin: "https://Vesta.Example.COM", host: "vesta.example.com",
			wantID: "vesta.example.com", wantOrigin: "https://vesta.example.com",
		},
		{
			name: "falls back to host when no origin", origin: "", host: "vesta.example.com", tls: true,
			wantID: "vesta.example.com", wantOrigin: "https://vesta.example.com",
		},
		{name: "plain http on a LAN name is not a secure context", origin: "http://vesta.local:3000", host: "vesta.local:3000", wantErr: true},
		{name: "plain http on a LAN IP is not a secure context", origin: "http://192.168.1.20:3000", host: "192.168.1.20:3000", wantErr: true},
		{name: "public IP literal rejected", origin: "https://203.0.113.5", host: "203.0.113.5", wantErr: true},
		{name: "empty label", origin: "https://vesta..example.com", host: "", wantErr: true},
		{name: "trailing dot", origin: "https://vesta.example.com.", host: "", wantErr: true},
		{name: "no origin and no host", origin: "", host: "", wantErr: true},
		{name: "non-http scheme", origin: "ftp://vesta.example.com", host: "", wantErr: true},
		{name: "garbage origin", origin: "://nonsense", host: "", wantErr: true},
		{
			name: "allowlist hit on full origin", origin: "https://ui.example.com", host: "api.example.com",
			allowlist: []string{"https://ui.example.com"},
			wantID:    "ui.example.com", wantOrigin: "https://ui.example.com",
		},
		{
			name: "allowlist hit on bare host", origin: "https://ui.example.com", host: "api.example.com",
			allowlist: []string{"ui.example.com"},
			wantID:    "ui.example.com", wantOrigin: "https://ui.example.com",
		},
		{
			name: "allowlist miss", origin: "https://evil.example", host: "api.example.com",
			allowlist: []string{"ui.example.com"}, wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rp, err := ResolveRelyingParty(c.origin, c.host, c.tls, c.allowlist)
			if (err != nil) != c.wantErr {
				t.Fatalf("ResolveRelyingParty(%q, %q) error = %v, wantErr %v", c.origin, c.host, err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if rp.ID != c.wantID {
				t.Errorf("ID = %q, want %q", rp.ID, c.wantID)
			}
			if rp.Origin != c.wantOrigin {
				t.Errorf("Origin = %q, want %q", rp.Origin, c.wantOrigin)
			}
		})
	}
}

// nginx does not set X-Forwarded-Host, so it reaches the API exactly as a client typed
// it. Only Origin and Host may influence the relying party.
func TestResolveRelyingPartyIgnoresForwardedHost(t *testing.T) {
	// The forwarded header is simply not a parameter, which is the guarantee. This test
	// documents the intent so a future signature change has to confront it.
	rp, err := ResolveRelyingParty("https://vesta.example.com", "vesta.example.com", true, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rp.ID != "vesta.example.com" {
		t.Errorf("ID = %q, want the Origin's host", rp.ID)
	}
}

// The Vite dev proxy sets changeOrigin, so the API sees Host: localhost:8090 while the
// browser is on localhost:3000. Preferring Origin is what keeps passkeys working locally.
func TestResolveRelyingPartyPrefersOriginOverHost(t *testing.T) {
	rp, err := ResolveRelyingParty("http://localhost:3000", "localhost:8090", false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rp.Origin != "http://localhost:3000" {
		t.Errorf("Origin = %q, want the browser's origin, not the API's host", rp.Origin)
	}
}

func TestResolveRelyingPartyRejectsOverLongHost(t *testing.T) {
	long := ""
	for len(long) < 260 {
		long += "a"
	}
	if _, err := ResolveRelyingParty("https://"+long, "", true, nil); err == nil {
		t.Error("expected an over-long host to be rejected")
	}
}
