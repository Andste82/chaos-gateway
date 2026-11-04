package ntpserver

import (
	"net/netip"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is what the server answers, the kinds of plan §2.18:
//
//	normal           the host's time, or the time of an offset, absolute, skew or leap fault: mode 4, stratum 2, the
//	                 reference id the gateway address of the client's network
//	unsynchronized   leap indicator 3 and stratum 16, with the correct time, so that a careless client takes it
//	kiss_of_death    leap indicator 3, stratum 0 and the kiss code as reference id
//	silence          nothing; the query is counted
//	origin_mismatch  the origin timestamp is not the client's transmit timestamp (the client's replay protection)
//	zero_transmit    a transmit timestamp of zero
//	wrong_mode       the mode of the answer is 5, not 4
//
// A normal answer is also stratum 16 while the host clock is not synchronized (the server never claims a time it does not
// have), unless the fault sets the time by itself (absolute).

const (
	// answerStratum is the stratum of a server that gets its time from the host's clock, which a time source disciplines
	answerStratum  = 2
	invalidStratum = 16
	// precision is the exponent of the clock's precision in seconds: 2^-20 is about a microsecond
	precision = -20
	// originMask turns a transmit timestamp into one the client did not send, whatever it was (also zero)
	originMask = 0x0000000180000000
)

// probeTransmit is the transmit timestamp of the query of the container's health check (Probe): the server answers it
// with the host's time at any time, even before it has a configuration, without counting or logging it.
const probeTransmit Timestamp = 0xC0DE_0123_FEED_BEEF

type result struct {
	reply    []byte // nil: no answer
	answer   model.NtpQueryLogEntryAnswer
	served   time.Time
	offsetMs int64
}

// answerTo builds the answer to a client query q that arrived at host time rx and is answered now (tx). f is the time
// fault that decides, nil for none.
func (s *Server) answerTo(q Packet, f *parsedFault, cfg *parsedConfig, client netip.Addr, rx, tx time.Time) result {
	kind := model.NtpQueryLogEntryAnswerNormal
	if f != nil {
		kind = f.kind
	}
	if kind == model.NtpQueryLogEntryAnswerSilence {
		return result{answer: kind}
	}
	sync := s.opt.Sync.State()
	// the host's clock is not to be trusted, and what a fault serves on top of it is not either; a fault that sets the
	// time itself does not depend on it
	if kind == model.NtpQueryLogEntryAnswerNormal && !sync.Synchronized && (f == nil || f.absolute.IsZero()) {
		kind = model.NtpQueryLogEntryAnswerUnsynchronized
	}
	servedRx, servedTx := f.serve(rx), f.serve(tx)
	p := Packet{
		Version:        min(q.Version, 4),
		Mode:           ModeServer,
		Stratum:        answerStratum,
		Poll:           q.Poll,
		Precision:      precision,
		RootDelay:      shortFormat(sync.EstError),
		RootDispersion: shortFormat(sync.MaxError),
		RefID:          cfg.gatewayOf(client),
		// the time the clock was last set: the whole second the answer's receive time lies in
		Reference: TimestampOf(time.Unix(servedRx.Unix(), 0)),
		Origin:    q.Transmit,
		Receive:   TimestampOf(servedRx),
		Transmit:  TimestampOf(servedTx),
	}
	if f != nil && kind == model.NtpQueryLogEntryAnswerNormal {
		p.Leap = f.leap
	}
	switch kind {
	case model.NtpQueryLogEntryAnswerUnsynchronized:
		p.Leap, p.Stratum, p.RefID = LeapUnsynchronized, invalidStratum, [4]byte{}
	case model.NtpQueryLogEntryAnswerKissOfDeath:
		p.Leap, p.Stratum, p.RefID = LeapUnsynchronized, 0, f.kiss
	case model.NtpQueryLogEntryAnswerOriginMismatch:
		p.Origin = q.Transmit ^ originMask
	case model.NtpQueryLogEntryAnswerZeroTransmit:
		p.Transmit = 0
	case model.NtpQueryLogEntryAnswerWrongMode:
		p.Mode = ModeBroadcast
	}
	r := result{reply: p.Marshal(), answer: kind, served: servedTx, offsetMs: offsetMs(servedTx, tx)}
	if kind == model.NtpQueryLogEntryAnswerZeroTransmit {
		r.served = time.Time{} // the device got no transmit time
	}
	return r
}
