package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestValidateSlug(t *testing.T) {
	valid := []string{"app", "my-app", "app_1", "production-backend-2"}
	for _, v := range valid {
		if err := ValidateSlug(v); err != nil {
			t.Errorf("expected valid for %s, got: %v", v, err)
		}
	}

	invalid := []string{"", "a", "-invalid", "app;rm -rf /", "app$evil", "a/b", "UPPERCASE_NOT_ALLOWED"}
	for _, inv := range invalid {
		if err := ValidateSlug(inv); err == nil {
			t.Errorf("expected invalid for %s, but passed", inv)
		}
	}
}

func TestValidateHost(t *testing.T) {
	valid := []string{"127.0.0.1", "103.20.12.5", "node1.example.com", "vps.example.com"}
	for _, v := range valid {
		if err := ValidateHost(v); err != nil {
			t.Errorf("expected valid for host %s, got: %v", v, err)
		}
	}

	invalid := []string{"", "; rm -rf /", "-oProxyCommand=calc.exe", "127.0.0.1; evil", "host|cat /etc/passwd"}
	for _, inv := range invalid {
		if err := ValidateHost(inv); err == nil {
			t.Errorf("expected invalid for host %s, but passed", inv)
		}
	}
}

func TestPasswordHashing(t *testing.T) {
	raw := "SuperSecretPassword123!"
	hashed, err := GenerateSaltedHash(raw)
	if err != nil {
		t.Fatalf("hash err: %v", err)
	}

	if !ComparePassword(hashed, raw) {
		t.Errorf("expected password match")
	}

	if ComparePassword(hashed, "WrongPassword") {
		t.Errorf("expected password mismatch for wrong password")
	}
}

func TestGitHubHMACVerification(t *testing.T) {
	secret := "my_webhook_secret_key"
	payload := []byte(`{"ref":"refs/heads/main"}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	validHeader := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !VerifyGitHubHMAC(payload, secret, validHeader) {
		t.Errorf("expected valid HMAC verification")
	}

	if VerifyGitHubHMAC(payload, secret, "sha256=invalidhash123") {
		t.Errorf("expected HMAC verification to fail for invalid hash")
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(3, 50*time.Millisecond)
	ip := "192.168.1.100"

	if !rl.Allow(ip) || !rl.Allow(ip) || !rl.Allow(ip) {
		t.Errorf("first 3 attempts should be allowed")
	}

	if rl.Allow(ip) {
		t.Errorf("4th attempt should be blocked by rate limiter")
	}

	time.Sleep(60 * time.Millisecond)
	if !rl.Allow(ip) {
		t.Errorf("attempt after window should be allowed")
	}
}
