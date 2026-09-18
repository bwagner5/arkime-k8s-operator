package version

import "fmt"

const Default = "6.7.0"
const Image = "ghcr.io/arkime/arkime/arkime@sha256:754ac8d50c8d4136462c3bb3701c400be66310020ab01d5ea94a4e73942b0127"

// Resolve never follows a moving tag. The caller persists the initial resolution.
func Resolve(requested, previous, override string, unverified bool) (string, string, error) {
	v := requested
	if v == "" {
		v = previous
	}
	if v == "" {
		v = Default
	}
	if override != "" {
		if !unverified {
			return "", "", fmt.Errorf("image override requires image.allowUnverified")
		}
		return v, override, nil
	}
	if v == "6.6.0" {
		return v, "ghcr.io/arkime/arkime/arkime@sha256:c43159c5b8f486c1ca0d74a7754b28df6f784e36741595bac0ec582ae93bb2ca", nil
	}
	if v != Default {
		return "", "", fmt.Errorf("version %s has no pinned compatibility entry", v)
	}
	return v, Image, nil
}

func CanUpgrade(from, to string) bool { return from == "6.6.0" && to == "6.7.0" }
