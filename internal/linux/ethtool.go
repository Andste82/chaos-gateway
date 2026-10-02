package linux

import "strings"

// Features is the result of `ethtool -k`: feature name to its state.
type Features map[string]Feature

// Feature is one offload feature.
type Feature struct {
	On    bool
	Fixed bool // cannot be changed on this device
}

// ParseEthtoolFeatures parses `ethtool -k <dev>`.
func ParseEthtoolFeatures(out string) Features {
	f := Features{}
	for _, line := range strings.Split(out, "\n") {
		name, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.HasPrefix(line, "Features for") || strings.TrimSpace(rest) == "" {
			continue
		}
		fields := strings.Fields(rest)
		f[name] = Feature{On: fields[0] == "on", Fixed: strings.Contains(rest, "[fixed]")}
	}
	return f
}

// OffloadsStillOn lists the segmentation and receive offloads that are still on and can be changed.
// An offload that is fixed in the "off" state, or absent, counts as off.
func (f Features) OffloadsStillOn() (stillOn []string) {
	for _, n := range []string{"generic-receive-offload", "generic-segmentation-offload", "tcp-segmentation-offload", "large-receive-offload"} {
		if x, ok := f[n]; ok && x.On {
			stillOn = append(stillOn, n)
		}
	}
	return stillOn
}
