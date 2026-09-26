package jsruntime

import (
	"context"
	"fmt"
)

// ConfirmRequest is one yes/no question asked of the user.
type ConfirmRequest struct {
	// Title identifies the asker.
	Title string

	// Body carries the question.
	Body string
}

// Notification is one notice shown to the user.
type Notification struct {
	// Title identifies the sender.
	Title string

	// Body carries the notice.
	Body string
}

// Interactor is the front end that answers the questions of an extension.
type Interactor interface {
	// Confirm asks the user a yes/no question and blocks until they answer
	// or ctx is canceled.
	Confirm(ctx context.Context, req ConfirmRequest) (bool, error)

	// Notify shows a non-blocking notice to the user.
	Notify(ctx context.Context, note Notification)
}

// interactorKey is the context key carrying the front end of a run.
type interactorKey struct{}

// WithInteractor attaches i to ctx.
func WithInteractor(ctx context.Context, i Interactor) context.Context {
	return context.WithValue(ctx, interactorKey{}, i)
}

// InteractorFromContext returns the interactor attached to ctx, if any.
func InteractorFromContext(ctx context.Context) (Interactor, bool) {
	interactor, ok := ctx.Value(interactorKey{}).(Interactor)
	if !ok || interactor == nil {
		return nil, false
	}
	return interactor, true
}

// ApproveAll wraps i so every confirmation is approved without asking. A nil i
// yields an interactor that approves everything and shows nothing, which is
// the headless default with the auto-approve option set.
func ApproveAll(i Interactor) Interactor {
	return approveAll{inner: i}
}

// approveAll approves every confirmation and forwards the notices.
type approveAll struct {
	inner Interactor
}

// Confirm approves without asking.
func (a approveAll) Confirm(ctx context.Context, req ConfirmRequest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("jsruntime: confirm canceled: %w", err)
	}
	return true, nil
}

// Notify forwards the notice, dropping it when there is no inner interactor.
func (a approveAll) Notify(ctx context.Context, note Notification) {
	if a.inner == nil {
		return
	}
	a.inner.Notify(ctx, note)
}
