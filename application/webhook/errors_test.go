package webhook

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/grab"
)

func TestIsTransient(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"db_unavailable_bare", ports.ErrDBUnavailable, true},
		{"db_unavailable_wrapped", fmt.Errorf("m: %w", ports.ErrDBUnavailable), true},
		{"db_unavailable_double", fmt.Errorf("o: %w", fmt.Errorf("i: %w", ports.ErrDBUnavailable)), true},
		{"deadline", context.DeadlineExceeded, true},
		{"canceled", context.Canceled, true},
		{"not_found_logic", ports.ErrNotFound, false},
		{"invalid_transition_logic", grab.ErrInvalidStatusTransition, false},
		{"unknown_logic", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, IsTransient(tc.err))
		})
	}
}

func TestErrorKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"db_unavailable", fmt.Errorf("w: %w", ports.ErrDBUnavailable), "db_unavailable"},
		{"timeout", context.DeadlineExceeded, "timeout"},
		{"canceled", context.Canceled, "canceled"},
		{"not_found", ports.ErrNotFound, "not_found"},
		{"invalid_transition", fmt.Errorf("r: %w", grab.ErrInvalidStatusTransition), "invalid_transition"},
		{"other", errors.New("x"), "other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ErrorKind(tc.err))
		})
	}
}
