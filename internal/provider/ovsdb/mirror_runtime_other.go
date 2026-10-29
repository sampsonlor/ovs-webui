//go:build !linux

package ovsdb

import (
	"context"
	"errors"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func (p *Provider) mirrorRuntime(context.Context, *MirrorExecutionOptions, inventory.Evidence, bool) (string, error) {
	return "", errors.New("MIRROR_RUNTIME_UNQUALIFIED")
}

func mirrorAttemptContext(ctx context.Context, _ bool) (context.Context, func()) {
	return ctx, func() {}
}

func (p *Provider) mirrorObservation() (func(context.Context, inventory.Evidence) (string, error), func()) {
	return func(ctx context.Context, e inventory.Evidence) (string, error) {
		return p.mirrorRuntime(ctx, p.mirror, e, false)
	}, func() {}
}
