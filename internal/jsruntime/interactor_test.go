package jsruntime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type scriptInteractor struct {
	answer  bool
	err     error
	notices []Notification
	asked   []ConfirmRequest
}

func (s *scriptInteractor) Confirm(ctx context.Context, req ConfirmRequest) (bool, error) {
	s.asked = append(s.asked, req)
	if s.err != nil {
		return false, s.err
	}
	return s.answer, nil
}

func (s *scriptInteractor) Notify(_ context.Context, note Notification) {
	s.notices = append(s.notices, note)
}

func TestInteractor(t *testing.T) {
	t.Run("attaches and reads back", func(t *testing.T) {
		inner := &scriptInteractor{}
		got, ok := InteractorFromContext(WithInteractor(t.Context(), inner))
		require.True(t, ok)
		require.Same(t, inner, got)
	})

	t.Run("reports absence", func(t *testing.T) {
		_, ok := InteractorFromContext(t.Context())
		require.False(t, ok)
	})

	t.Run("approve all approves without asking", func(t *testing.T) {
		inner := &scriptInteractor{}
		approved, err := ApproveAll(
			inner,
		).Confirm(t.Context(), ConfirmRequest{Title: "t", Body: "b"})
		require.NoError(t, err)
		require.True(t, approved)
		require.Empty(t, inner.asked)
	})

	t.Run("approve all forwards notices", func(t *testing.T) {
		inner := &scriptInteractor{}
		ApproveAll(inner).Notify(t.Context(), Notification{Title: "t", Body: "b"})
		require.Len(t, inner.notices, 1)
	})

	t.Run("approve all works without an inner interactor", func(t *testing.T) {
		approved, err := ApproveAll(nil).Confirm(t.Context(), ConfirmRequest{Title: "t", Body: "b"})
		require.NoError(t, err)
		require.True(t, approved)
		ApproveAll(nil).Notify(t.Context(), Notification{Title: "t", Body: "b"})
	})

	t.Run("approve all honors cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := ApproveAll(nil).Confirm(ctx, ConfirmRequest{Title: "t", Body: "b"})
		require.Error(t, err)
	})
}
