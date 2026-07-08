package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	secret := "s3cr3t"
	body := []byte(`{"action":"opened"}`)

	tests := []struct {
		name    string
		header  string
		wantErr bool
	}{
		{"valid", sign(secret, body), false},
		{"wrong secret", sign("nope", body), true},
		{"tampered body", sign(secret, []byte("other")), true},
		{"missing prefix", hex.EncodeToString([]byte("x")), true},
		{"empty", "", true},
		{"not hex", "sha256=zzzz", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifySignature(secret, body, tt.header)
			if (err != nil) != tt.wantErr {
				t.Fatalf("VerifySignature() err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}
