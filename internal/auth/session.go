package auth

import (
	"time"
)

// Session is a browser session of the admin.
type Session struct {
	ID        string
	CSRF      string
	ExpiresAt time.Time
	epoch     int64
}

// Login checks the admin password and starts a session. remote identifies the client for the rate
// limit; after repeated failures the answer is *RateLimited.
func (s *Store) Login(password, remote string) (*Session, error) {
	// one attempt at a time: a burst of parallel guesses must not pass the limiter before the first
	// failure is recorded, and the hashes (32 MiB each) must not pile up
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if wait := s.limiter.blocked(remote); wait > 0 {
		return nil, &RateLimited{RetryAfter: wait}
	}
	if !s.VerifyPassword(password) {
		if wait := s.limiter.fail(remote); wait > 0 {
			return nil, &RateLimited{RetryAfter: wait}
		}
		return nil, ErrBadCredentials
	}
	s.limiter.succeed(remote)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	s.sweepSessions(now)
	sess := &Session{ID: randomString(32), CSRF: randomString(24), ExpiresAt: now.Add(s.lifetime).UTC(), epoch: s.st.Epoch}
	s.sessions[sess.ID] = sess
	return sess, nil
}

// MaxSessions is the number of sessions the store keeps: a login (which takes the password) past it
// ends the session that expires first.
const MaxSessions = 100

// sweepSessions drops the sessions that have expired or belong to an earlier epoch, which nobody
// looks up again, and then the ones that expire first until there is room for one more. The caller
// holds mu.
func (s *Store) sweepSessions(now time.Time) {
	for id, sess := range s.sessions {
		if !now.Before(sess.ExpiresAt) || sess.epoch != s.st.Epoch {
			delete(s.sessions, id)
		}
	}
	for len(s.sessions) >= MaxSessions {
		oldest := ""
		for id, sess := range s.sessions {
			if oldest == "" || sess.ExpiresAt.Before(s.sessions[oldest].ExpiresAt) {
				oldest = id
			}
		}
		delete(s.sessions, oldest)
	}
}

// LookupSession returns the session with the given id while it is valid.
func (s *Store) LookupSession(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	if !s.clk.Now().Before(sess.ExpiresAt) || sess.epoch != s.st.Epoch {
		delete(s.sessions, id)
		return nil, false
	}
	c := *sess
	return &c, true
}

// Logout ends a session.
func (s *Store) Logout(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// EndSessions ends every session.
func (s *Store) EndSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = map[string]*Session{}
}
