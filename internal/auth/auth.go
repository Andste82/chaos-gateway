package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// Scope is what a principal may do (plan §2.15 token scopes). `full` includes `overlays`, which
// includes `read`; `service` is only for the internal API.
type Scope string

// The scopes.
const (
	ScopeRead     Scope = "read"
	ScopeOverlays Scope = "overlays"
	ScopeFull     Scope = "full"
	ScopeService  Scope = "service"
	// ScopeNone is the requirement of operations that need no authentication.
	ScopeNone Scope = "none"
)

// Allows reports whether a principal with scope s may do what needs the scope need.
func (s Scope) Allows(need Scope) bool {
	rank := map[Scope]int{ScopeRead: 1, ScopeOverlays: 2, ScopeFull: 3}
	switch {
	case need == ScopeNone:
		return true
	case need == ScopeService || s == ScopeService:
		return s == need
	}
	return rank[s] >= rank[need] && rank[s] > 0
}

// Valid reports whether s is a scope a user may give a token.
func (s Scope) Valid() bool { return s == ScopeRead || s == ScopeOverlays || s == ScopeFull }

// TokenPrefix starts every API token, so a leaked one is recognizable.
const TokenPrefix = "cgw_"

// Token is an API token as it is listed: never its value.
type Token struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scope      Scope      `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type tokenRecord struct {
	Token
	Hash string `json:"hash"`
}

// fileState is the content of auth.json.
type fileState struct {
	SchemaVersion int `json:"schema_version"`
	// PasswordHash is the encoded argon2id hash of the admin password; empty before setup.
	PasswordHash string `json:"password_hash,omitempty"`
	// Epoch counts password changes and resets; a session of an older epoch has ended.
	Epoch int64 `json:"epoch"`
	// SetupDone is true once the first-start setup has finished.
	SetupDone bool `json:"setup_done"`
	// SetupTokenHash is the hash of the one-time setup token while setup is open.
	SetupTokenHash string        `json:"setup_token_hash,omitempty"`
	Tokens         []tokenRecord `json:"tokens,omitempty"`
	// ServiceTokenHash is the hash of the token the service containers use for /internal.
	ServiceTokenHash string `json:"service_token_hash,omitempty"`
}

// Errors of the store.
var (
	ErrBadCredentials = errors.New("auth: wrong password")
	ErrNoSuchToken    = errors.New("auth: no such token")
	ErrSetupDone      = errors.New("auth: setup is completed")
)

// RateLimited is returned by Login when too many attempts failed.
type RateLimited struct{ RetryAfter time.Duration }

func (e *RateLimited) Error() string {
	return fmt.Sprintf("auth: too many login attempts, retry in %s", e.RetryAfter.Round(time.Second))
}

// Option configures a Store.
type Option func(*Store)

// WithClock sets the clock (tests).
func WithClock(c clock.Clock) Option { return func(s *Store) { s.clk = c } }

// WithHashParams sets the argon2id cost: tests use a cheap one.
func WithHashParams(p HashParams) Option { return func(s *Store) { s.params = p } }

// WithSessionLifetime sets how long a session lasts (default 12 hours).
func WithSessionLifetime(d time.Duration) Option { return func(s *Store) { s.lifetime = d } }

// Store is the authentication state. It is safe for concurrent use.
type Store struct {
	dir      string
	path     string
	clk      clock.Clock
	params   HashParams
	lifetime time.Duration

	loginMu  sync.Mutex
	mu       sync.Mutex
	st       fileState
	mtime    time.Time
	size     int64
	sessions map[string]*Session
	lastUsed map[string]time.Time
	limiter  *limiter
}

// Open opens or creates the store in dir (mode 0700).
func Open(dir string, opts ...Option) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, path: filepath.Join(dir, "auth.json"), clk: &clock.Real{}, params: DefaultHashParams, lifetime: 12 * time.Hour,
		sessions: map[string]*Session{}, lastUsed: map[string]time.Time{}}
	for _, o := range opts {
		o(s)
	}
	s.limiter = newLimiter(s.clk)
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.st = fileState{SchemaVersion: 1}
		return nil
	}
	if err != nil {
		return err
	}
	var st fileState
	if err := json.Unmarshal(raw, &st); err != nil {
		return fmt.Errorf("auth: %s is corrupt: %w", s.path, err)
	}
	if fi, err := os.Stat(s.path); err == nil {
		s.mtime, s.size = fi.ModTime(), fi.Size()
	}
	s.st = st
	return nil
}

// refresh reloads the file when another process (the password reset) changed it. The caller holds mu.
func (s *Store) refresh() {
	fi, err := os.Stat(s.path)
	if err != nil || (fi.ModTime().Equal(s.mtime) && fi.Size() == s.size) {
		return
	}
	prev := s.st.Epoch
	if err := s.load(); err == nil && s.st.Epoch != prev {
		s.sessions = map[string]*Session{}
	}
}

// save writes the state atomically. The caller holds mu.
func (s *Store) save() error {
	raw, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".auth-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		return err
	}
	if fi, err := os.Stat(s.path); err == nil {
		s.mtime, s.size = fi.ModTime(), fi.Size()
	}
	return nil
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system's randomness is gone: nothing safe is left to do
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashSecret(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

func equalHash(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// ---- setup

// SetupCompleted reports whether the first-start setup has finished.
func (s *Store) SetupCompleted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	return s.st.SetupDone
}

// NewSetupToken creates the one-time token of the first start (only its hash is stored) and
// replaces an earlier one: every start of an unfinished setup prints a new token.
func (s *Store) NewSetupToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.SetupDone {
		return "", ErrSetupDone
	}
	t := "setup_" + randomString(24)
	s.st.SetupTokenHash = hashSecret(t)
	return t, s.save()
}

// CheckSetupToken reports whether t is the current setup token.
func (s *Store) CheckSetupToken(t string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	return !s.st.SetupDone && s.st.SetupTokenHash != "" && equalHash(s.st.SetupTokenHash, hashSecret(t))
}

// CompleteSetup sets the admin password and ends the setup: the token is void from now on.
func (s *Store) CompleteSetup(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	if s.st.SetupDone {
		return ErrSetupDone
	}
	h, err := HashPassword(password, s.params)
	if err != nil {
		return err
	}
	s.st.PasswordHash, s.st.SetupDone, s.st.SetupTokenHash = h, true, ""
	s.st.Epoch++
	s.sessions = map[string]*Session{}
	return s.save()
}

// ---- password

// VerifyPassword checks the admin password.
func (s *Store) VerifyPassword(p string) bool {
	s.mu.Lock()
	h := s.st.PasswordHash
	s.refresh()
	if s.st.PasswordHash != "" {
		h = s.st.PasswordHash
	}
	s.mu.Unlock()
	if h == "" {
		return false
	}
	ok, _ := CheckPassword(p, h)
	return ok
}

// ChangePassword sets a new password after the current one was checked and ends every session
// except keep (the caller's own).
func (s *Store) ChangePassword(current, next, keep string) error {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if wait := s.limiter.blocked("password-change"); wait > 0 {
		return &RateLimited{RetryAfter: wait}
	}
	if !s.VerifyPassword(current) {
		if wait := s.limiter.fail("password-change"); wait > 0 {
			return &RateLimited{RetryAfter: wait}
		}
		return ErrBadCredentials
	}
	s.limiter.succeed("password-change")
	return s.setPassword(next, keep)
}

// ResetPassword sets a new password without the old one (`chaosgw admin reset-password`) and
// ends all sessions, also those of a running API process, which notices the new epoch.
func (s *Store) ResetPassword(next string) error { return s.setPassword(next, "") }

func (s *Store) setPassword(next, keep string) error {
	h, err := HashPassword(next, s.params)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	s.st.PasswordHash = h
	s.st.Epoch++
	for id, sess := range s.sessions {
		if id != keep {
			delete(s.sessions, id)
		} else {
			sess.epoch = s.st.Epoch
		}
	}
	return s.save()
}

// ---- tokens

// CreateToken creates an API token. The value is returned once and stored only as a hash.
func (s *Store) CreateToken(name string, scope Scope, expiresIn time.Duration) (Token, string, error) {
	if !scope.Valid() {
		return Token{}, "", fmt.Errorf("auth: invalid scope %q", scope)
	}
	value := TokenPrefix + randomString(32)
	t := Token{ID: uuid.NewString(), Name: name, Scope: scope, CreatedAt: s.clk.Now().UTC()}
	if expiresIn > 0 {
		e := t.CreatedAt.Add(expiresIn)
		t.ExpiresAt = &e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	s.st.Tokens = append(s.st.Tokens, tokenRecord{Token: t, Hash: hashSecret(value)})
	return t, value, s.save()
}

// ListTokens returns the tokens in creation order, without their values.
func (s *Store) ListTokens() []Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	out := make([]Token, 0, len(s.st.Tokens))
	for _, r := range s.st.Tokens {
		t := r.Token
		if lu, ok := s.lastUsed[t.ID]; ok {
			l := lu
			t.LastUsedAt = &l
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// DeleteToken revokes a token.
func (s *Store) DeleteToken(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	for i, r := range s.st.Tokens {
		if r.ID == id {
			s.st.Tokens = append(s.st.Tokens[:i:i], s.st.Tokens[i+1:]...)
			delete(s.lastUsed, id)
			return s.save()
		}
	}
	return ErrNoSuchToken
}

// ServiceTokenID identifies the service token in Token and in the audit log.
const ServiceTokenID = "service"

// EnsureServiceToken makes sure the token of the service containers (the DNS proxy, the TLS
// responder, Kea's hook: scope `service`, only /internal) exists and that its value is in the file at
// path, which the services mount read-only. The token is never created by a user and not listed. A
// token that matches the file is left alone; otherwise a new one replaces it.
func (s *Store) EnsureServiceToken(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	if raw, err := os.ReadFile(path); err == nil && s.st.ServiceTokenHash != "" && equalHash(s.st.ServiceTokenHash, hashSecret(strings.TrimSpace(string(raw)))) {
		return nil
	}
	value := TokenPrefix + "svc_" + randomString(32)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".service-token-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	// readable by the service containers' users, which differ from ours
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(value + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	s.st.ServiceTokenHash = hashSecret(value)
	if err := s.save(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// AuthenticateToken finds the token with the given value; an expired one does not count. The
// service token authenticates with the scope `service`.
func (s *Store) AuthenticateToken(value string) (Token, bool) {
	h := hashSecret(value)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	if s.st.ServiceTokenHash != "" && equalHash(s.st.ServiceTokenHash, h) {
		return Token{ID: ServiceTokenID, Name: "service", Scope: ScopeService}, true
	}
	now := s.clk.Now()
	for _, r := range s.st.Tokens {
		if !equalHash(r.Hash, h) {
			continue
		}
		if r.ExpiresAt != nil && !now.Before(*r.ExpiresAt) {
			return Token{}, false
		}
		s.lastUsed[r.ID] = now.UTC()
		t := r.Token
		l := now.UTC()
		t.LastUsedAt = &l
		return t, true
	}
	return Token{}, false
}

// TokenValid reports whether the token with this id still exists and has not expired.
func (s *Store) TokenValid(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	now := s.clk.Now()
	for _, r := range s.st.Tokens {
		if r.ID == id {
			return r.ExpiresAt == nil || now.Before(*r.ExpiresAt)
		}
	}
	return false
}
