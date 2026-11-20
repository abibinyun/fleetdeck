package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	slugRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,62}$`)
	hostRegex = regexp.MustCompile(`^[a-zA-Z0-9.-]+$`)
)

// ValidateSlug ensures application/container names are safe and match Docker & DNS standards
func ValidateSlug(name string) error {
	name = strings.TrimSpace(name)
	if !slugRegex.MatchString(name) {
		return fmt.Errorf("invalid name: must be 2-63 chars, lowercase alphanumeric, dashes or underscores only")
	}
	return nil
}

// ValidateHost ensures remote host string is safe from command injection
func ValidateHost(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("host cannot be empty")
	}
	// Check if valid IP
	if ip := net.ParseIP(host); ip != nil {
		return nil
	}
	// Check if valid hostname
	if !hostRegex.MatchString(host) || strings.HasPrefix(host, "-") {
		return fmt.Errorf("invalid host format: only valid IP address or domain allowed")
	}
	return nil
}

// VerifyGitHubHMAC validates X-Hub-Signature-256 header using SHA-256
func VerifyGitHubHMAC(payload []byte, secret string, signatureHeader string) bool {
	if secret == "" {
		return true // Webhook secret verification disabled if no secret is set
	}
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}
	actualMAC, err := hex.DecodeString(strings.TrimPrefix(signatureHeader, "sha256="))
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := mac.Sum(nil)

	return subtle.ConstantTimeCompare(actualMAC, expectedMAC) == 1
}

// GenerateSaltedHash creates a constant-time SHA-256 password hash with salt
func GenerateSaltedHash(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	saltHex := hex.EncodeToString(salt)

	h := sha256.New()
	h.Write([]byte(saltHex + ":" + password))
	hashHex := hex.EncodeToString(h.Sum(nil))

	return fmt.Sprintf("%s$%s", saltHex, hashHex), nil
}

// ComparePassword checks a password against a stored salted hash
func ComparePassword(storedHash, password string) bool {
	parts := strings.Split(storedHash, "$")
	if len(parts) != 2 {
		// Fallback for legacy plaintext password
		return subtle.ConstantTimeCompare([]byte(storedHash), []byte(password)) == 1
	}
	saltHex := parts[0]
	expectedHash := parts[1]

	h := sha256.New()
	h.Write([]byte(saltHex + ":" + password))
	actualHash := hex.EncodeToString(h.Sum(nil))

	return subtle.ConstantTimeCompare([]byte(actualHash), []byte(expectedHash)) == 1
}

// RateLimiter provides in-memory IP rate limiting for sensitive endpoints
type RateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	limit    int
	window   time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		attempts: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
}

func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Filter out expired attempts
	var valid []time.Time
	for _, t := range rl.attempts[ip] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= rl.limit {
		rl.attempts[ip] = valid
		return false
	}

	rl.attempts[ip] = append(valid, now)
	return true
}
