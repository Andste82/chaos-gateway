package store

import (
	"strings"
	"testing"
)

func TestErrorMessagesNameWhatIsWrong(t *testing.T) {
	for err, want := range map[error]string{
		&ErrRevisionConflict{Active: 7}:                                 "active revision is 7",
		&ErrConfirmPending{Pending: 3}:                                  "revision 3 is waiting for confirmation",
		&ErrNotACandidate{ID: 4, Status: "active", Want: "candidate"}:   "revision 4 is active, not candidate",
		&ErrCorrupt{Path: "/x/000001.json", Reason: "bad"}:              "/x/000001.json is corrupt: bad",
		&ErrNewerSchema{Path: "/x/config.json", Found: 9, Supported: 1}: "schema version 9",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%T: %q lacks %q", err, err.Error(), want)
		}
	}
}
