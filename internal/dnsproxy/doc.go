// Package dnsproxy is the DNS proxy of the gateway (plan §2.6): the resolver the devices of the test
// networks use. It runs in the service namespace and answers over UDP and TCP, forwards to the
// upstream resolvers, caches, answers static entries, strips AAAA records (the V1 networks are
// IPv4 only) and logs every query. It keeps no state of its own: the configuration comes from the
// API (the proxy registers at start and follows it by long poll), the query log goes back to it.
package dnsproxy
