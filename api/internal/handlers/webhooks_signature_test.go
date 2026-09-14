package handlers

import (
	"errors"
	"testing"

	"kubernetes.getvesta.sh/api/internal/git"
)

// POST /api/v1/webhooks/... is unauthenticated and can trigger builds and deploys, so this
// function is the whole of its access control once the provider has had its say.
//
// The row that matters most is "no signature, not connection-scoped, unsigned not allowed".
// That case used to be accepted unconditionally -- an absent signature header skipped
// verification entirely -- which meant anyone able to reach the endpoint and guess a
// repository string could deploy.
func TestWebhookPolicy(t *testing.T) {
	invalid := errors.New("invalid signature")

	cases := []struct {
		name             string
		verifyErr        error
		connectionScoped bool
		allowUnsigned    bool
		want             webhookVerdict
	}{
		{"valid signature", nil, false, false, webhookAccept},
		{"valid signature on a scoped url", nil, true, false, webhookAccept},

		// No flag and no URL shape relaxes a signature that is present and wrong.
		{"invalid signature", invalid, false, false, webhookReject},
		{"invalid signature while unsigned is tolerated", invalid, false, true, webhookReject},
		{"invalid signature on a scoped url", invalid, true, true, webhookReject},

		// The hole.
		{"missing signature, closed", git.ErrNoSignature, false, false, webhookReject},
		{"missing signature, still open", git.ErrNoSignature, false, true, webhookAcceptUnsigned},

		// Bitbucket Cloud offers no webhook secret at all, so the unguessable connection id
		// in the path is the only credential available.
		{"missing signature on a scoped url", git.ErrNoSignature, true, false, webhookAccept},
		{"missing signature on a scoped url, open", git.ErrNoSignature, true, true, webhookAccept},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := webhookPolicy(tc.verifyErr, tc.connectionScoped, tc.allowUnsigned)
			if got != tc.want {
				t.Errorf("webhookPolicy(%v, scoped=%v, allowUnsigned=%v) = %v (%q), want %v",
					tc.verifyErr, tc.connectionScoped, tc.allowUnsigned, got, reason, tc.want)
			}
			if got == webhookReject && reason == "" {
				t.Error("a rejection carried no reason; it is recorded on the delivery and returned to the caller")
			}
		})
	}
}

// A wrapped sentinel must still be recognised, or a provider that adds context to its
// "no signature" result silently turns every unsigned delivery into a hard rejection --
// or worse, a provider wrapping an *invalid* signature error as the sentinel would let one
// through.
func TestWebhookPolicyUnwrapsTheSentinel(t *testing.T) {
	wrapped := errors.Join(errors.New("gitlab"), git.ErrNoSignature)

	if got, _ := webhookPolicy(wrapped, false, true); got != webhookAcceptUnsigned {
		t.Errorf("a wrapped ErrNoSignature was %v, want %v", got, webhookAcceptUnsigned)
	}
	if got, _ := webhookPolicy(wrapped, false, false); got != webhookReject {
		t.Errorf("a wrapped ErrNoSignature with unsigned closed was %v, want %v", got, webhookReject)
	}
}
