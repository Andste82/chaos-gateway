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
	sess := &Session{ID: randomString(32), CSRF: randomString(24), ExpiresAt: now.Add(s.lifetime).UTC(), epoch: s.st.Epoch}
	s.sessions[sess.ID] = sess
	return sess, nil
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
