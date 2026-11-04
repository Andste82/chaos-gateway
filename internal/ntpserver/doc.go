// Package ntpserver is the NTP server of the gateway (plan §2.18, M34): it answers the time queries of the devices of the
// test networks with the host's clock, or with a time fault of the family time. It runs in the service namespace
// and never forwards a query: the answer is built locally, so there is no upstream NTP traffic to manipulate. It keeps no
// state of its own: the configuration (the time faults with the clients they apply to, the ports to listen on) comes
// from the API by long poll, the query log goes back to it.
//
// The server tells the server a device asked from the port it received the query on (decision D36): the compiler
// redirects each distinct destination selector of a time fault to a port of its own, and the client's address tells the
// scope, as the DNS proxy does.
package ntpserver
