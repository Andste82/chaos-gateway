package version

import "testing"

func TestString(t *testing.T) {
	old := [3]string{Version, Commit, Date}
	defer func() { Version, Commit, Date = old[0], old[1], old[2] }()
	Version, Commit, Date = "1.2.3", "abc1234", "2026-10-02"
	if got, want := String("chaosgw"), "chaosgw 1.2.3 (abc1234, 2026-10-02)"; got != want {
		t.Fatalf("String = %q, want %q", got, want)
	}
}

func TestDefaultsAreSet(t *testing.T) {
	if Version == "" || Commit == "" || Date == "" {
		t.Fatal("version variables must never be empty")
	}
}
