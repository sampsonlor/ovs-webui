//go:build !linux

package authn

import "errors"

func KeyFile(string, bool) ([]byte, error) { return nil, errors.New("AUTH_REQUIRES_LINUX") }
