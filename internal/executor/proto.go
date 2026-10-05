package executor

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"

	"golang.org/x/sys/unix"
)

// ProtocolVersion is the version of the wire protocol. Both sides check it when a connection
// starts and refuse to work on a mismatch, so a half-updated set of containers fails loudly
// instead of misbehaving (plan §2.16).
const ProtocolVersion = 1

// maxLine bounds one protocol line: a large ruleset is the biggest legitimate message.
const maxLine = 64 << 20

// Error codes of a Response.
const (
	CodeProtocol  = "protocol_mismatch"
	CodeInvalid   = "invalid_operation"
	CodeScope     = "out_of_scope"
	CodeCommand   = "command_failed"
	CodeInternal  = "internal"
	CodeForbidden = "forbidden"
)

// Hello is the first line each side sends.
type Hello struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version,omitempty"` // build version, informational
	Role     string `json:"role"`              // executor | client
}

// Frame is one line on the wire. A line has exactly one of the members set.
type Frame struct {
	Hello    *Hello        `json:"hello,omitempty"`
	Request  *Request      `json:"request,omitempty"`
	Response *Response     `json:"response,omitempty"`
	Watch    *WatchRequest `json:"watch,omitempty"`
	Event    *WatchEvent   `json:"event,omitempty"`
	Error    *RemoteError  `json:"error,omitempty"` // connection-level error; the sender closes
}

// WatchRequest asks the executor to stream events on this connection instead of answering
// requests (M6a-04): the one message a watch connection ever sends after its hello. The executor
// answers with one Frame.Event per line until the connection closes or the executor stops.
type WatchRequest struct {
	What string `json:"what"` // "conntrack"
	NS   string `json:"ns,omitempty"`
}

// WatchEvent is one event of a watch: the raw JSON of the parsed record (for "conntrack",
// internal/linux.ConntrackEvent).
type WatchEvent struct {
	Data json.RawMessage `json:"data"`
}

// WhatConntrack is the one watchable kind WatchRequest.What accepts so far.
const WhatConntrack = "conntrack"

// Request asks the executor to run operations as one batch.
type Request struct {
	ID  uint64            `json:"id"`
	Ops []json.RawMessage `json:"ops"`
}

// Response answers a Request.
type Response struct {
	ID      uint64       `json:"id"`
	OK      bool         `json:"ok"`
	Error   *RemoteError `json:"error,omitempty"`
	Outcome Outcome      `json:"outcome"`
}

// RemoteError is an error reported by the other side.
type RemoteError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RemoteError) Error() string { return e.Code + ": " + e.Message }

// ErrProtocolMismatch is returned by both sides when the versions differ.
var ErrProtocolMismatch = errors.New("executor protocol version mismatch")

type codec struct {
	r *bufio.Scanner
	w io.Writer
}

func newCodec(rw io.ReadWriter) *codec {
	sc := bufio.NewScanner(rw)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	return &codec{r: sc, w: rw}
}

func (c *codec) read() (*Frame, error) {
	if !c.r.Scan() {
		if err := c.r.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	var f Frame
	if err := json.Unmarshal(c.r.Bytes(), &f); err != nil {
		return nil, fmt.Errorf("malformed frame: %w", err)
	}
	return &f, nil
}

func (c *codec) write(f *Frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	_, err = c.w.Write(append(b, '\n'))
	return err
}

// Cred is the identity of the process on the other end of a Unix socket.
type Cred struct {
	PID      int32
	UID, GID uint32
}

// PeerCred reads SO_PEERCRED from a Unix connection.
func PeerCred(c net.Conn) (Cred, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return Cred{}, errors.New("not a Unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return Cred{}, err
	}
	var cred *unix.Ucred
	var serr error
	if err := raw.Control(func(fd uintptr) {
		cred, serr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return Cred{}, err
	}
	if serr != nil {
		return Cred{}, serr
	}
	return Cred{PID: cred.Pid, UID: cred.Uid, GID: cred.Gid}, nil
}

// AllowUIDs returns an authorization function that accepts root and the given users.
func AllowUIDs(uids ...uint32) func(Cred) error {
	allowed := map[uint32]bool{0: true}
	for _, u := range uids {
		allowed[u] = true
	}
	return func(c Cred) error {
		if !allowed[c.UID] {
			return fmt.Errorf("uid %d is not allowed", c.UID)
		}
		return nil
	}
}
