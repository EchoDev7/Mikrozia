package acme

import (
	"errors"
	"net"
	"strings"

	"golang.org/x/net/idna"
)

var ErrInvalidDomain = errors.New("a valid DNS hostname is required")

// NormalizeDomain accepts DNS hostnames, including IDNA, and returns a safe canonical label sequence.
func NormalizeDomain(value string) (string, error) {
	if value == "" || strings.ContainsAny(value, "/\\\x00") {
		return "", ErrInvalidDomain
	}
	for _, r := range value {
		if r <= ' ' || r == 127 {
			return "", ErrInvalidDomain
		}
	}
	name, err := idna.Lookup.ToASCII(value)
	name = strings.ToLower(name)
	if err != nil || len(name) > 253 || net.ParseIP(name) != nil {
		return "", ErrInvalidDomain
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return "", ErrInvalidDomain
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInvalidDomain
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return "", ErrInvalidDomain
			}
		}
	}
	return name, nil
}
