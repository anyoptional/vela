package jinx

import (
	"errors"
	"net/url"
	"strings"
)

// Target identifies one logical client and its routing mode.
type Target struct {
	Name string
	URL  string
}

// TargetMode describes how a Target resolves its endpoint.
type TargetMode uint8

const (
	// TargetService resolves instances through service discovery.
	TargetService TargetMode = iota
	// TargetDirect sends requests to the configured URL.
	TargetDirect
)

// Mode returns the routing mode selected by the target URL.
func (t Target) Mode() TargetMode {
	if t.URL != "" {
		return TargetDirect
	}
	return TargetService
}

// Validate checks target fields before any network activity.
func (t Target) Validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("jinx: target name is required")
	}
	if t.URL == "" {
		return nil
	}

	parsed, err := url.Parse(t.URL)
	if err != nil || !parsed.IsAbs() || parsed.Opaque != "" || parsed.Host == "" ||
		parsed.Hostname() == "" || (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
		return errors.New("jinx: direct target URL must be an absolute http or https URL with a host")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(t.URL, "#") {
		return errors.New("jinx: direct target URL must not contain query or fragment")
	}
	return nil
}
