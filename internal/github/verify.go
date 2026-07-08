package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"strings"
)

// ErrBadSignature is returned when the payload's signature does not match.
var ErrBadSignature = errors.New("github: signature verification failed")

// SignatureHeader is the header GitHub uses for the SHA-256 HMAC.
const SignatureHeader = "X-Hub-Signature-256"

// VerifySignature checks the X-Hub-Signature-256 header against the raw request
// body using the shared webhook secret. It uses a constant-time comparison to
// avoid leaking timing information.
//
// header must be of the form "sha256=<hex>". body is the exact bytes received
// (the signature is over the raw body, so verify before any re-encoding).
func VerifySignature(secret string, body []byte, header string) error {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return ErrBadSignature
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return ErrBadSignature
	}

	mac := hmacNew(secret)
	mac.Write(body)
	got := mac.Sum(nil)

	if !hmac.Equal(got, want) {
		return ErrBadSignature
	}
	return nil
}

func hmacNew(secret string) hash.Hash {
	return hmac.New(sha256.New, []byte(secret))
}
