package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// verifySignature reports whether h carries a valid HMAC-SHA256 signature
// of body under secret.
//
// GitHub sends "X-Hub-Signature-256: sha256=<hex>". Many other senders use
// "X-Signature", with or without the "sha256=" prefix, so that is accepted
// as a fallback when the GitHub header is absent.
func verifySignature(secret []byte, h http.Header, body []byte) bool {
	sig := h.Get("X-Hub-Signature-256")
	if sig == "" {
		sig = h.Get("X-Signature")
	}
	sig = strings.TrimPrefix(strings.TrimSpace(sig), "sha256=")
	got, err := hex.DecodeString(sig)
	if err != nil || len(got) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
