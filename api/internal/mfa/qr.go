package mfa

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"net/url"

	"github.com/pquerna/otp"
)

// qrPixels is the rendered size. Large enough that a phone camera locks on from a normal
// viewing distance without the image needing to be scaled up in the browser, which would
// blur the module edges and make it harder to read.
const qrPixels = 256

// QRDataURI renders an otpauth URL as a PNG data URI ready to drop into an <img>.
//
// Rendered here rather than in the browser because the QR libraries available to the UI
// would be a new frontend dependency, while pquerna/otp -- already required for the codes
// themselves -- brings a renderer with it. It also keeps the secret out of one more place:
// the URL still reaches the client, but nothing has to re-encode it there.
func QRDataURI(otpauthURL string) (string, error) {
	// otp.NewKeyFromURL accepts any string at all -- including "" -- and Image renders a
	// QR of whatever it parsed. A silently malformed code is the worst outcome here: the
	// user scans it, their app shows six digits, and every one of them is rejected. Check
	// the shape ourselves rather than trusting the parser to.
	u, err := url.Parse(otpauthURL)
	if err != nil || u.Scheme != "otpauth" || u.Query().Get("secret") == "" {
		return "", fmt.Errorf("mfa: %q is not an otpauth url carrying a secret", otpauthURL)
	}

	key, err := otp.NewKeyFromURL(otpauthURL)
	if err != nil {
		return "", fmt.Errorf("mfa: reading otpauth url: %w", err)
	}

	img, err := key.Image(qrPixels, qrPixels)
	if err != nil {
		return "", fmt.Errorf("mfa: rendering QR code: %w", err)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("mfa: encoding QR code: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
