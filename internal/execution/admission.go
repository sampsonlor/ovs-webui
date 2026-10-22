package execution

import (
	"context"
	"errors"
)

type admissionKey struct{}

// WithAdmission carries the manager's live admission check across provider I/O.
// The callback is never persisted. Forward execution reloads current authority
// and workspace lease; automatic compensation reloads its durable checkpoint.
// It does not replace the one-shot, write-ahead beforeSend callback.
func WithAdmission(ctx context.Context, check func(context.Context) error) context.Context {
	return context.WithValue(ctx, admissionKey{}, check)
}

func CheckAdmission(ctx context.Context) error {
	check, ok := ctx.Value(admissionKey{}).(func(context.Context) error)
	if !ok || check == nil {
		return errors.New("EXECUTION_ADMISSION_REQUIRED")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return check(ctx)
}
