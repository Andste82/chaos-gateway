package domain

import (
	"bytes"
	"strconv"
)

func itoa(i int) string { return strconv.Itoa(i) }

func quote(s string) string { return strconv.Quote(s) }

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
