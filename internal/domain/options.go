package domain

import "github.com/Andste82/chaos-gateway/internal/model"

// Option changes how a document is validated or resolved.
type Option func(*options)

type options struct {
	discovered []string
}

// WithDiscovered tells the validation which devices have been discovered but are not configured.
// They have UUIDs of their own, and overlays and groups may refer to them before they are adopted
// (plan §2.1.1, Group.members in the spec). Without the option a reference to an unknown UUID is
// an unknown_reference.
func WithDiscovered(ids ...string) Option {
	return func(o *options) { o.discovered = append(o.discovered, ids...) }
}

func collectOptions(opts []Option) options {
	var o options
	for _, f := range opts {
		f(&o)
	}
	return o
}

// addDiscovered makes the discovered devices known to the index.
func (x *Index) addDiscovered(ids []string) {
	if len(ids) == 0 {
		return
	}
	if x.Discovered == nil {
		x.Discovered = map[string]bool{}
	}
	for _, id := range ids {
		x.Discovered[lower(id)] = true
	}
}

// newIndex builds the index of a configuration with the discovered devices.
func newIndex(cfg *modelConfiguration, o options) (*Index, []validationError) {
	idx, errs := BuildIndex(cfg)
	idx.addDiscovered(o.discovered)
	return idx, errs
}

type (
	modelConfiguration = model.Configuration
	validationError    = model.ValidationError
)
