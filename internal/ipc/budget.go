package ipc

import (
	"context"
	"errors"
)

type Class int

const (
	Auth Class = iota
	Control
	Read
	Heavy
)

var ErrQueueFull = errors.New("queue full")

type budget struct{ active, waiting chan struct{} }

func newBudget(active, waiting int) *budget {
	return &budget{active: make(chan struct{}, active), waiting: make(chan struct{}, waiting)}
}
func (b *budget) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case b.active <- struct{}{}:
		return func() { <-b.active }, nil
	default:
	}
	select {
	case b.waiting <- struct{}{}:
		defer func() { <-b.waiting }()
	default:
		return nil, ErrQueueFull
	}
	select {
	case b.active <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-b.active
			return nil, err
		}
		return func() { <-b.active }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func defaultBudgets() map[Class]*budget {
	return map[Class]*budget{Auth: newBudget(4, 8), Control: newBudget(4, 8), Read: newBudget(8, 16), Heavy: newBudget(2, 16)}
}
