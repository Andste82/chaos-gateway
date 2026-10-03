package bird

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const maxConfigBytes = 1 << 20

var kernelTableRE = regexp.MustCompile(`\bkernel\s+table\s+(\d+)`)

// strip removes comments and string contents so that keywords can be looked for in what BIRD
// actually reads. It fails on an unterminated string or comment.
func strip(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		switch {
		case s[i] == '#':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return "", errors.New("unterminated comment")
			}
			i += end + 4
			b.WriteByte(' ')
		case s[i] == '"':
			j := i + 1
			for j < len(s) && s[j] != '"' && s[j] != '\n' {
				j++
			}
			if j >= len(s) || s[j] != '"' {
				return "", errors.New("unterminated string")
			}
			b.WriteString(`""`)
			i = j + 1
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String(), nil
}

func printable(s string) error {
	for _, r := range s {
		if r == 0 || (r < 0x20 && r != '\n' && r != '\t' && r != '\r') || r > 0x7e {
			return fmt.Errorf("character %U is not allowed", r)
		}
	}
	return nil
}

func balanced(s string) error {
	depth := 0
	for _, r := range s {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return errors.New("a closing brace without an opening one")
			}
		}
	}
	if depth != 0 {
		return errors.New("an opening brace is not closed")
	}
	return nil
}

// CheckSnippet checks the raw configuration a user may add inside a protocol block: printable text,
// balanced braces (it cannot close the block and add statements of its own), and none of the
// statements that would change what the generated configuration guarantees.
func CheckSnippet(s string) error {
	if len(s) > 64<<10 {
		return errors.New("too long")
	}
	if err := printable(s); err != nil {
		return err
	}
	code, err := strip(s)
	if err != nil {
		return err
	}
	if err := balanced(code); err != nil {
		return err
	}
	for _, kw := range []string{"include", "protocol", "router", "kernel", "template", "eval"} {
		if regexp.MustCompile(`\b` + kw + `\b`).MatchString(code) {
			return fmt.Errorf("%q is not allowed in a snippet", kw)
		}
	}
	return nil
}

// CheckText checks a whole configuration before the executor writes it: printable text, balanced
// braces, no `include` (BIRD would read any file), and `kernel table` only for the given tables.
func CheckText(text string, allowedTables []int) error {
	if len(text) > maxConfigBytes {
		return errors.New("configuration too long")
	}
	if err := printable(text); err != nil {
		return err
	}
	code, err := strip(text)
	if err != nil {
		return err
	}
	if err := balanced(code); err != nil {
		return err
	}
	if regexp.MustCompile(`\binclude\b`).MatchString(code) {
		return errors.New("`include` is not allowed")
	}
	for _, m := range kernelTableRE.FindAllStringSubmatch(code, -1) {
		n, _ := strconv.Atoi(m[1])
		ok := false
		for _, t := range allowedTables {
			ok = ok || t == n
		}
		if !ok {
			return fmt.Errorf("`kernel table %d` is outside Chaos Gateway's tables", n)
		}
	}
	return nil
}
