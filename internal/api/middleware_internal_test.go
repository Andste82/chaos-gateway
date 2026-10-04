package api

import (
	"crypto/tls"
	"net/http"
	"testing"
)

// M5-18 test: the session cookie uses the __Host- prefixed name over TLS, the plain name
// otherwise, and a cookie is read back under either name.
func TestSessionCookieNameFollowsTLS(t *testing.T) {
	plain := &http.Request{Header: http.Header{}}
	tlsReq := &http.Request{Header: http.Header{}, TLS: &tls.ConnectionState{}}

	if got := sessionCookieName(plain); got != SessionCookie {
		t.Errorf("plain HTTP: %q", got)
	}
	if got := sessionCookieName(tlsReq); got != HostSessionCookie {
		t.Errorf("TLS: %q", got)
	}

	withPlain := &http.Request{Header: http.Header{"Cookie": {SessionCookie + "=abc"}}}
	if ck, err := sessionCookie(withPlain); err != nil || ck.Value != "abc" {
		t.Errorf("reading the plain name: %v %v", ck, err)
	}
	withHost := &http.Request{Header: http.Header{"Cookie": {HostSessionCookie + "=def"}}}
	if ck, err := sessionCookie(withHost); err != nil || ck.Value != "def" {
		t.Errorf("reading the __Host- name: %v %v", ck, err)
	}
	// the __Host- name is checked first: a client holding both (e.g. across a scheme change)
	// is read as the one a current TLS session would have set
	withBoth := &http.Request{Header: http.Header{"Cookie": {SessionCookie + "=old; " + HostSessionCookie + "=new"}}}
	if ck, err := sessionCookie(withBoth); err != nil || ck.Value != "new" {
		t.Errorf("reading with both present: %v %v", ck, err)
	}
	if _, err := sessionCookie(&http.Request{Header: http.Header{}}); err == nil {
		t.Error("neither cookie present: want an error")
	}
}
