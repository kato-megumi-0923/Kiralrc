//go:build !windows

package app

import "strings"

func protectSecret(plain string) (string, error) {
	return strings.TrimSpace(plain), nil
}

func unprotectSecret(stored string) (string, bool, error) {
	return strings.TrimSpace(stored), false, nil
}
