package access

import (
	_ "embed"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

//go:embed common-passwords.txt
var commonPasswords string

// LoadPolicy performs no network requests and does not load .env. An absent key
// is permitted for migration/contract commands, but New rejects it at runtime.
func LoadPolicy(get func(string) string, environment string) (Policy, error) {
	var key []byte
	if encoded := get("FACETS_ACCESS_KEY"); encoded != "" {
		var err error
		key, err = hex.DecodeString(encoded)
		if err != nil || len(key) != 32 {
			return Policy{}, errors.New("FACETS_ACCESS_KEY must encode 32 random bytes")
		}
	}
	p := DefaultPolicy(key)
	if environment == "production" {
		p.DevelopmentProfile = ""
	}
	if name := get("FACETS_DEVELOPMENT_PROFILE"); name != "" {
		if environment == "production" {
			return Policy{}, errors.New("development entitlements are forbidden in production")
		}
		p.DevelopmentProfile = name
	}
	for name, dst := range map[string]*time.Duration{
		"FACETS_SESSION_IDLE": &p.SessionIdle, "FACETS_SESSION_ABSOLUTE": &p.SessionAbsolute,
		"FACETS_STAFF_IDLE": &p.StaffIdle, "FACETS_STAFF_ABSOLUTE": &p.StaffAbsolute,
		"FACETS_IDENTITY_TOKEN_TTL": &p.TokenTTL, "FACETS_INVITATION_TTL": &p.InvitationTTL,
		"FACETS_RESEND_DELAY": &p.ResendDelay, "FACETS_REAUTH_TTL": &p.ReauthenticationTTL,
		"FACETS_ATTEMPT_WINDOW": &p.AttemptWindow, "FACETS_DEVELOPMENT_VALIDITY": &p.DevelopmentValidity,
		"FACETS_PUBLIC_RESPONSE_FLOOR":     &p.PublicResponseFloor,
		"FACETS_SESSION_ACTIVITY_INTERVAL": &p.ActivityInterval,
	} {
		if v := get(name); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return Policy{}, errors.New("invalid access duration: " + name)
			}
			*dst = d
		}
	}
	for name, dst := range map[string]*int{
		"FACETS_IDENTITY_ATTEMPTS": &p.IdentityAttempts, "FACETS_ORIGIN_ATTEMPTS": &p.OriginAttempts,
		"FACETS_PASSWORD_CONCURRENCY": &p.Password.Concurrency,
	} {
		if v := get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return Policy{}, errors.New("invalid access limit: " + name)
			}
			*dst = n
		}
	}
	if v := get("FACETS_DEVELOPMENT_MEMBERS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return Policy{}, errors.New("invalid development member limit")
		}
		p.DevelopmentMembers = n
	}
	if v := get("FACETS_MAX_SESSIONS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			return Policy{}, errors.New("invalid session limit")
		}
		p.MaxSessions = int32(n)
	}
	for name, dst := range map[string]*uint32{"FACETS_PASSWORD_MEMORY_KIB": &p.Password.Memory, "FACETS_PASSWORD_ITERATIONS": &p.Password.Iterations} {
		if v := get(name); v != "" {
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				return Policy{}, errors.New("invalid password cost: " + name)
			}
			*dst = uint32(n)
		}
	}
	if err := p.validate(); err != nil {
		return Policy{}, errors.New("invalid access policy bounds")
	}
	return p, nil
}
