package engine_test

import (
	"net/netip"
	"strconv"
)

type netipPrefix = netip.Prefix

func mustPrefix(s string) netip.Prefix { return netip.MustParsePrefix(s) }
func itoa(n int) string                { return strconv.Itoa(n) }
