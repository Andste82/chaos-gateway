package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

var t0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func open(t *testing.T, opts ...Option) (*Store, *clock.Fake, string) {
	t.Helper()
	clk := clock.NewFake(t0)
	dir := filepath.Join(t.TempDir(), "auth")
	s, err := Open(dir, append([]Option{WithClock(clk), WithHashParams(FastHashParams)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return s, clk, dir
}

func TestScopesInclude(t *testing.T) {
	cases := []struct {
		have, need Scope
		want       bool
	}{
		{ScopeFull, ScopeRead, true}, {ScopeFull, ScopeOverlays, true}, {ScopeFull, ScopeFull, true},
		{ScopeOverlays, ScopeRead, true}, {ScopeOverlays, ScopeFull, false},
		{ScopeRead, ScopeOverlays, false}, {ScopeRead, ScopeRead, true},
		{ScopeService, ScopeRead, false}, {ScopeFull, ScopeService, false}, {ScopeService, ScopeService, true},
		{"", ScopeRead, false}, {ScopeRead, ScopeNone, true}, {"", ScopeNone, true},
	}
	for _, c := range cases {
		if got := c.have.Allows(c.need); got != c.want {
			t.Errorf("%q allows %q: %v", c.have, c.need, got)
		}
	}
}

func TestPasswordHashesAreSaltedAndVerify(t *testing.T) {
	a, _ := HashPassword("correct horse battery", FastHashParams)
	b, _ := HashPassword("correct horse battery", FastHashParams)
	if a == b || !strings.HasPrefix(a, "$argon2id$") {
		t.Fatalf("%s %s", a, b)
	}
	if ok, err := CheckPassword("correct horse battery", a); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := CheckPassword("wrong", a); ok {
		t.Error("a wrong password verifies")
	}
	if _, err := CheckPassword("x", "plaintext"); err == nil {
		t.Error("a malformed hash is accepted")
	}
}

func TestSetupTokenIsOneTime(t *testing.T) {
	s, _, dir := open(t)
	tok, err := s.NewSetupToken()
	if err != nil {
		t.Fatal(err)
	}
	if !s.CheckSetupToken(tok) || s.CheckSetupToken("setup_wrong") || s.CheckSetupToken("") {
		t.Fatal("the setup token check is wrong")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	if strings.Contains(string(raw), tok) {
		t.Error("the setup token is stored in clear")
	}
	// a new start prints a new token; the old one is void
	tok2, _ := s.NewSetupToken()
	if s.CheckSetupToken(tok) || !s.CheckSetupToken(tok2) {
		t.Error("the earlier token still works")
	}
	if err := s.CompleteSetup("a long enough password"); err != nil {
		t.Fatal(err)
	}
	if s.CheckSetupToken(tok2) || !s.SetupCompleted() {
		t.Error("the token works after the setup")
	}
	if err := s.CompleteSetup("another long password"); !errors.Is(err, ErrSetupDone) {
		t.Errorf("%v", err)
	}
	if _, err := s.NewSetupToken(); !errors.Is(err, ErrSetupDone) {
		t.Errorf("%v", err)
	}
}

func TestFilesAreWrittenPrivately(t *testing.T) {
	s, _, dir := open(t)
	_ = s.CompleteSetup("a long enough password")
	for p, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "auth.json"): 0o600} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: %v %v", p, fi, err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	if strings.Contains(string(raw), "a long enough password") {
		t.Error("the password is stored in clear")
	}
}

func TestSessionsAndTheirEnd(t *testing.T) {
	s, clk, _ := open(t, WithSessionLifetime(time.Hour))
	_ = s.CompleteSetup("a long enough password")
	if _, err := s.Login("wrong password!", "1.1.1.1"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("%v", err)
	}
	sess, err := s.Login("a long enough password", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.CSRF == "" || sess.ID == sess.CSRF || !sess.ExpiresAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("%+v", sess)
	}
	if got, ok := s.LookupSession(sess.ID); !ok || got.CSRF != sess.CSRF {
		t.Fatal("the session is not found")
	}
	s.Logout(sess.ID)
	if _, ok := s.LookupSession(sess.ID); ok {
		t.Error("a session survives logout")
	}
	sess, _ = s.Login("a long enough password", "1.1.1.1")
	clk.Advance(time.Hour)
	if _, ok := s.LookupSession(sess.ID); ok {
		t.Error("an expired session is valid")
	}
}

func TestChangingThePasswordEndsTheOtherSessions(t *testing.T) {
	s, _, _ := open(t)
	_ = s.CompleteSetup("a long enough password")
	mine, _ := s.Login("a long enough password", "a")
	other, _ := s.Login("a long enough password", "b")
	if err := s.ChangePassword("wrong password!", "new password here", mine.ID); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("%v", err)
	}
	if err := s.ChangePassword("a long enough password", "new password here", mine.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LookupSession(mine.ID); !ok {
		t.Error("the caller's own session ended")
	}
	if _, ok := s.LookupSession(other.ID); ok {
		t.Error("another session survived the password change")
	}
	if s.VerifyPassword("a long enough password") || !s.VerifyPassword("new password here") {
		t.Error("the password did not change")
	}
}

func TestAResetFromAnotherProcessEndsRunningSessions(t *testing.T) {
	s, _, dir := open(t)
	_ = s.CompleteSetup("a long enough password")
	sess, _ := s.Login("a long enough password", "a")
	// `chaosgw admin reset-password` is another process on the same directory
	cli, err := Open(dir, WithHashParams(FastHashParams))
	if err != nil {
		t.Fatal(err)
	}
	// make sure the file's time is newer than what the API process has seen
	later := time.Now().Add(time.Minute)
	if err := cli.ResetPassword("reset by the admin!"); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(filepath.Join(dir, "auth.json"), later, later)
	if _, ok := s.LookupSession(sess.ID); ok {
		t.Error("a session survived the reset")
	}
	if !s.VerifyPassword("reset by the admin!") || s.VerifyPassword("a long enough password") {
		t.Error("the running process does not see the new password")
	}
}

func TestTokens(t *testing.T) {
	s, clk, dir := open(t)
	tok, value, err := s.CreateToken("ci", ScopeOverlays, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(value, TokenPrefix) || tok.ExpiresAt == nil {
		t.Fatalf("%s %+v", value, tok)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	if strings.Contains(string(raw), value) {
		t.Fatal("the token value is stored")
	}
	got, ok := s.AuthenticateToken(value)
	if !ok || got.ID != tok.ID || got.Scope != ScopeOverlays {
		t.Fatalf("%+v %v", got, ok)
	}
	if _, ok := s.AuthenticateToken(value + "x"); ok {
		t.Error("a wrong token authenticates")
	}
	if l := s.ListTokens(); len(l) != 1 || l[0].LastUsedAt == nil {
		t.Errorf("%+v", l)
	}
	if _, _, err := s.CreateToken("x", "root", 0); err == nil {
		t.Error("an invalid scope is accepted")
	}
	clk.Advance(time.Hour)
	if _, ok := s.AuthenticateToken(value); ok {
		t.Error("an expired token authenticates")
	}
	_, v2, _ := s.CreateToken("forever", ScopeRead, 0)
	if err := s.DeleteToken(tok.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteToken(tok.ID); !errors.Is(err, ErrNoSuchToken) {
		t.Errorf("%v", err)
	}
	if _, ok := s.AuthenticateToken(v2); !ok {
		t.Error("another token was revoked")
	}
	// the tokens survive a restart
	again, err := Open(dir, WithClock(clk), WithHashParams(FastHashParams))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := again.AuthenticateToken(v2); !ok {
		t.Error("a token does not survive a restart")
	}
}

func TestLoginIsRateLimitedPerClient(t *testing.T) {
	s, clk, _ := open(t)
	_ = s.CompleteSetup("a long enough password")
	var rl *RateLimited
	for i := 0; i < maxFailures; i++ {
		_, err := s.Login("wrong password!", "attacker")
		if i < maxFailures-1 && !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if i == maxFailures-1 && !errors.As(err, &rl) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if rl.RetryAfter <= 0 {
		t.Fatalf("%+v", rl)
	}
	// even the right password has to wait, but only for this client
	if _, err := s.Login("a long enough password", "attacker"); !errors.As(err, &rl) {
		t.Errorf("%v", err)
	}
	if _, err := s.Login("a long enough password", "friend"); err != nil {
		t.Errorf("another client is blocked: %v", err)
	}
	clk.Advance(rl.RetryAfter)
	if _, err := s.Login("a long enough password", "attacker"); err != nil {
		t.Errorf("still blocked after the wait: %v", err)
	}
	// a repeated failure doubles the wait
	for i := 0; i < maxFailures; i++ {
		_, _ = s.Login("wrong password!", "again")
	}
	_, e1 := s.Login("x", "again")
	var r1 *RateLimited
	errors.As(e1, &r1)
	clk.Advance(r1.RetryAfter)
	_, e2 := s.Login("wrong password!", "again")
	var r2 *RateLimited
	if !errors.As(e2, &r2) || r2.RetryAfter <= baseWait {
		t.Errorf("the wait does not grow: %v", e2)
	}
}
