package apply

import "sort"

func sortStrings(s []string) { sort.Strings(s) }

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func minus(a, b []string) []string {
	var out []string
	for _, x := range a {
		if !contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func union(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, x := range b {
		if !contains(out, x) {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}
