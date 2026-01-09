package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
)

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	const secret = "It's a Secret to Everybody"
	const body = "Hello, World!"
	// Example from GitHub's webhook documentation.
	const github = "757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if got := sign(secret, body); got != github {
		t.Fatalf("sign() = %s, want %s", got, github)
	}

	tests := []struct {
		name   string
		header map[string]string
		want   bool
	}{
		{"github header", map[string]string{"X-Hub-Signature-256": "sha256=" + github}, true},
		{"upper case hex", map[string]string{"X-Hub-Signature-256": "sha256=" + strings.ToUpper(github)}, true},
		{"x-signature with prefix", map[string]string{"X-Signature": "sha256=" + github}, true},
		{"x-signature bare hex", map[string]string{"X-Signature": github}, true},
		{"github header wins", map[string]string{"X-Hub-Signature-256": "sha256=" + sign("wrong", body), "X-Signature": github}, false},
		{"wrong secret", map[string]string{"X-Hub-Signature-256": "sha256=" + sign("wrong", body)}, false},
		{"truncated", map[string]string{"X-Hub-Signature-256": "sha256=" + github[:40]}, false},
		{"not hex", map[string]string{"X-Hub-Signature-256": "sha256=zz" + github[2:]}, false},
		{"sha1 header only", map[string]string{"X-Hub-Signature": "sha1=0123456789abcdef0123456789abcdef01234567"}, false},
		{"missing", nil, false},
	}
	for _, tt := range tests {
		h := http.Header{}
		for k, v := range tt.header {
			h.Set(k, v)
		}
		if got := verifySignature([]byte(secret), h, []byte(body)); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestVerifySignatureBodyTampered(t *testing.T) {
	h := http.Header{}
	h.Set("X-Hub-Signature-256", "sha256="+sign("s3cret", `{"ref":"refs/heads/main"}`))
	if !verifySignature([]byte("s3cret"), h, []byte(`{"ref":"refs/heads/main"}`)) {
		t.Fatal("valid signature rejected")
	}
	if verifySignature([]byte("s3cret"), h, []byte(`{"ref":"refs/heads/evil"}`)) {
		t.Fatal("signature accepted for a different body")
	}
}
