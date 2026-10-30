// Package access owns identities, space membership and the authorization boundary.
package access

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

var (
	ErrInvalid        = errors.New("invalid access request")
	ErrUnauthorized   = errors.New("authentication required")
	ErrForbidden      = errors.New("access denied")
	ErrNotFound       = errors.New("resource unavailable")
	ErrLimited        = errors.New("access attempt limit reached")
	ErrCapacity       = errors.New("space capability unavailable")
	ErrResponsibility = errors.New("resolve ownership or billing responsibility first")
	ErrReauthenticate = errors.New("recent authentication required")
)

type PasswordCost struct {
	Memory, Iterations uint32
	Parallelism        uint8
	Concurrency        int
}

func (c PasswordCost) valid() bool {
	return c.Memory >= 19456 && c.Memory <= 262144 && c.Iterations >= 2 && c.Iterations <= 10 &&
		c.Parallelism >= 1 && c.Parallelism <= 4 && c.Concurrency >= 1 && c.Concurrency <= 16 &&
		uint64(c.Memory)*uint64(c.Concurrency) <= 1048576
}

type cryptography struct {
	aead  cipher.AEAD
	key   []byte
	cost  PasswordCost
	slots chan struct{}
	deny  map[[32]byte]struct{}
	dummy string
}

func passwordDenylist(additional []string) map[[32]byte]struct{} {
	deny := make(map[[32]byte]struct{}, 10000+len(additional))
	for password := range strings.SplitSeq(commonPasswords, "\n") {
		password = strings.TrimSuffix(password, "\r")
		if password != "" {
			deny[sha256.Sum256([]byte(strings.ToLower(password)))] = struct{}{}
		}
	}
	for _, password := range additional {
		deny[sha256.Sum256([]byte(strings.ToLower(password)))] = struct{}{}
	}
	return deny
}

func newCryptography(key []byte, cost PasswordCost, deny map[[32]byte]struct{}) (*cryptography, error) {
	if len(key) != 32 || !cost.valid() || len(deny) == 0 {
		return nil, ErrInvalid
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	c := &cryptography{aead: aead, key: append([]byte{}, key...), cost: cost, slots: make(chan struct{}, cost.Concurrency), deny: deny}
	c.dummy, err = c.hash(context.Background(), rand.Text())
	return c, err
}

func (c *cryptography) passwordAllowed(password string) bool {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 15 || len(password) > 1024 {
		return false
	}
	_, blocked := c.deny[sha256.Sum256([]byte(strings.ToLower(password)))]
	return !blocked
}

func (c *cryptography) derive(ctx context.Context, password string, salt []byte, cost PasswordCost) ([]byte, error) {
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return nil, ErrLimited
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := argon2.IDKey([]byte(password), salt, cost.Iterations, cost.Memory, cost.Parallelism, 32)
	if err := ctx.Err(); err != nil {
		clear(result)
		return nil, err
	}
	return result, nil
}

func (c *cryptography) hash(ctx context.Context, password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash, err := c.derive(ctx, password, salt, c.cost)
	if err != nil {
		return "", err
	}
	defer clear(hash)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", c.cost.Memory, c.cost.Iterations, c.cost.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func (c *cryptography) verify(ctx context.Context, password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, errors.New("invalid stored password encoding")
	}
	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return false, ErrInvalid
	}
	cost := PasswordCost{Concurrency: 1}
	dst := []*uint32{&cost.Memory, &cost.Iterations}
	for i, key := range []string{"m=", "t=", "p="} {
		value, ok := strings.CutPrefix(fields[i], key)
		if !ok {
			return false, ErrInvalid
		}
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return false, ErrInvalid
		}
		if i < 2 {
			*dst[i] = uint32(n)
		} else {
			if n > 4 {
				return false, ErrInvalid
			}
			cost.Parallelism = uint8(n)
		}
	}
	if !cost.valid() {
		return false, ErrInvalid
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return false, ErrInvalid
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != 32 {
		return false, ErrInvalid
	}
	got, err := c.derive(ctx, password, salt, cost)
	if err != nil {
		return false, err
	}
	defer clear(got)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func verifier(secret string) []byte { h := sha256.Sum256([]byte(secret)); return h[:] }
func (c *cryptography) seal(scope string, plain []byte) []byte {
	//nolint:gosec // NewGCMWithRandomNonce requires nil and generates/prepends a fresh random nonce internally.
	return c.aead.Seal(nil, nil, plain, []byte(scope))
}
func (c *cryptography) open(scope string, sealed []byte) ([]byte, error) {
	return c.aead.Open(nil, nil, sealed, []byte(scope))
}
func (c *cryptography) bucket(scope string) []byte {
	h := hmac.New(sha256.New, c.key)
	_, _ = h.Write([]byte("limit:" + scope))
	return h.Sum(nil)
}
