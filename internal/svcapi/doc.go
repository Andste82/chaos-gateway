// Package svcapi is the client of the gateway's internal API that the service containers share (plan §2.15, scope
// `service`): the DNS proxy and the TLS responder reach the API on the gateway's end of the link to the service
// namespace with the service token, and trust the API's certificate from a file.
package svcapi
