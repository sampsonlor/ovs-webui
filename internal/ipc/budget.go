package ipc

import (
	"context"
	"errors"
	"time"
)

type Class int

const (
	Auth Class = iota
	Control
	Read
	Heavy
	Observation
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
	return map[Class]*budget{
		Auth: newBudget(4, 8), Control: newBudget(4, 8), Read: newBudget(8, 16), Heavy: newBudget(2, 16),
		Observation: newBudget(1, 0),
	}
}

func (h *Handler) queueHealth(now time.Time) []HealthComponent {
	classes := [...]struct {
		class Class
		key   string
	}{
		{Auth, "ipc-auth"}, {Control, "ipc-control"}, {Read, "ipc-read"},
		{Heavy, "ipc-heavy"}, {Observation, "ipc-observation"},
	}
	out := make([]HealthComponent, 0, len(classes))
	for _, item := range classes {
		b := h.budgets[item.class]
		active, capacity, waiting := len(b.active), cap(b.active), len(b.waiting)
		state, reason := "ready", ""
		if waiting > 0 || active == capacity {
			state, reason = "degraded", "IPC_QUEUE_PRESSURE"
		}
		out = append(out, HealthComponent{
			Key: item.key, State: state, Reason: reason, Authority: "manager-ipc-admission",
			ObservedAt: &now, Active: &active, Capacity: &capacity,
		})
	}
	return out
}
