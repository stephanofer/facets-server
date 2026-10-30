package access

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestCryptography(t *testing.T) {
	p, err := LoadPolicy(func(string) string { return "" }, "test")
	if err != nil {
		t.Fatal(err)
	}
	deny := passwordDenylist(p.PasswordDenylist)
	c, err := newCryptography([]byte(strings.Repeat("k", 32)), p.Password, deny)
	if err != nil {
		t.Fatal(err)
	}
	for password := range strings.SplitSeq(commonPasswords, "\n") {
		if c.passwordAllowed(strings.TrimSuffix(password, "\r")) {
			t.Fatal("bundled common password accepted")
		}
	}
	for _, tc := range []struct {
		name, password string
		allowed        bool
	}{
		{name: "short", password: "tiny", allowed: false},
		{name: "known compromised phrase", password: "correct horse battery staple", allowed: false},
		{name: "long common password", password: "123456789012345", allowed: false},
		{name: "long unicode phrase", password: "A personal phrase with 雨 and many words", allowed: true}, //nolint:gosec // Synthetic UTF-8 password-policy fixture, never an operational credential.
		{name: "oversized", password: strings.Repeat("a", 1025), allowed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if c.passwordAllowed(tc.password) != tc.allowed {
				t.Fatal("password policy mismatch")
			}
		})
	}
	hash, err := c.hash(t.Context(), "A unique synthetic test phrase")
	if err != nil {
		t.Fatal(err)
	}
	other, err := c.hash(t.Context(), "A unique synthetic test phrase")
	if err != nil {
		t.Fatal(err)
	}
	if hash == other {
		t.Fatal("password salts repeated")
	}
	for _, password := range []string{"A unique synthetic test phrase", "wrong"} {
		ok, err := c.verify(t.Context(), password, hash)
		if err != nil || ok != (password != "wrong") {
			t.Fatal("hash verification", err)
		}
	}
	if _, err := c.verify(t.Context(), "x", "$argon2id$v=19$m=4294967295,t=2,p=1$abc$def"); err == nil {
		t.Fatal("unbounded stored hash accepted")
	}
	encrypted := c.seal("mfa:one", []byte("synthetic secret"))
	if _, err := c.open("mfa:other", encrypted); err == nil {
		t.Fatal("ciphertext moved to another identity")
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err := c.open("mfa:one", encrypted); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.hash(ctx, "ignored"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	c.slots <- struct{}{}
	c.slots <- struct{}{}
	if _, err := c.hash(t.Context(), "ignored"); !errors.Is(err, ErrLimited) {
		t.Fatal("unbounded KDF admission", err)
	}
	<-c.slots
	<-c.slots
}

func TestTOTPReplayWindow(t *testing.T) {
	t.Parallel()
	secret := "JBSWY3DPEHPK3PXP" //nolint:gosec // Public deterministic TOTP fixture, not an enrolled factor.
	now := time.Unix(1800000015, 0)
	for _, offset := range []int64{-2, -1, 0, 1, 2} {
		code, err := totp.GenerateCode(secret, now.Add(time.Duration(offset)*30*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		step, err := totpStep(secret, code, now, -1)
		allowed := offset >= -1 && offset <= 1
		if (err == nil) != allowed {
			t.Fatalf("offset %d acceptance mismatch", offset)
		}
		if err == nil {
			if _, err := totpStep(secret, code, now, step); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("accepted TOTP reused")
			}
		}
	}
}

func TestResourcePermission(t *testing.T) {
	t.Parallel()
	for _, role := range []string{"owner", "admin", "member", "reader", "staff", ""} {
		t.Run(role, func(t *testing.T) {
			if ResourcePermission(role, "read", false, true, true) {
				t.Fatal("administration disclosed private data")
			}
			if ResourcePermission(role, "operate", true, true, true) != (role == "owner" || role == "admin" || role == "member") {
				t.Fatal("reader/staff operation policy")
			}
			if ResourcePermission(role, "administer", true, true, false) {
				t.Fatal("operation granted account administration")
			}
		})
	}
}

func TestStaffEnrollmentSecret(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, secret string
		valid        bool
	}{
		{name: "empty"}, {name: "short", secret: strings.Repeat("A", 16)},
		{name: "generated-length base32", secret: strings.Repeat("A", 32), valid: true},
		{name: "malformed", secret: strings.Repeat("!", 32)},
		{name: "line break", secret: strings.Repeat("A", 32) + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if validEnrollmentSecret(tc.secret) != tc.valid {
				t.Fatal("staff enrollment secret bounds")
			}
		})
	}
}

func TestPolicy(t *testing.T) {
	t.Parallel()
	digest := sha256.Sum256([]byte(commonPasswords))
	if hex.EncodeToString(digest[:]) != "68782d6a4a19a4768d5f15dd66bd534e7a33055cc755411e33f16d18c50fdcce" {
		t.Fatal("review common-password corpus source/checksum on update")
	}
	for _, tc := range []struct {
		name, environment string
		env               map[string]string
		invalid           bool
	}{
		{name: "defaults for migration", environment: "test", env: map[string]string{}},
		{name: "invalid key", environment: "test", env: map[string]string{"FACETS_ACCESS_KEY": "not-a-key"}, invalid: true},
		{name: "development in production", environment: "production", env: map[string]string{"FACETS_DEVELOPMENT_PROFILE": "free"}, invalid: true},
		{name: "bad duration", environment: "test", env: map[string]string{"FACETS_SESSION_IDLE": "forever"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := LoadPolicy(func(k string) string { return tc.env[k] }, tc.environment)
			if (err != nil) != tc.invalid {
				t.Fatal(err)
			}
			if tc.environment == "production" && err == nil && p.DevelopmentProfile != "" {
				t.Fatal("production silently assigned development entitlements")
			}
		})
	}
}
