// Package tlsresponder is the TLS responder of the gateway (plan §2.8, M21): the certificate test cases. Connections
// that a TLS case selects are routed into the service namespace (plan §3.3) and end here. The responder reads the
// ClientHello, asks the API which case decides the connection, and answers with a deliberately broken certificate
// (expired, not yet valid, for another name, self-signed, signed by a CA nobody trusts) or breaks the handshake
// (reset, close, stall). It never forwards what a device sends to a case: a device that completes the handshake
// has taken a certificate it must reject, which is what the report records. A connection no case decides is passed
// through to the destination the device meant.
package tlsresponder
