// Package linux parses the output of the standard network tools (`ip -j`, `tc -j`, `nft -j`,
// `ethtool -k`). The executor reads the kernel state through it (plan §3.4). The parsers are
// tolerant of fields they do not know: iproute2 and nftables add fields between releases.
package linux
