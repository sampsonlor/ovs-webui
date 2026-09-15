//go:build !linux

package sqlite

import "errors"

type fileGuard struct{}

func acquireGuard(string) (*fileGuard, error) { return nil, errors.New("STORAGE_REQUIRES_LINUX") }
func (*fileGuard) check() error               { return errors.New("STORAGE_REQUIRES_LINUX") }
func (*fileGuard) close()                     {}
func syncDirectory(string) error              { return errors.New("STORAGE_REQUIRES_LINUX") }
func checkPrivateFile(string) error           { return errors.New("STORAGE_REQUIRES_LINUX") }
