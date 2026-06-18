package domain_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestNewIMDBID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    domain.IMDBID
		wantErr bool
	}{
		{name: "valid_short", input: "tt0000001", want: "tt0000001"},
		{name: "valid_long", input: "tt123456789", want: "tt123456789"},
		{name: "valid_leading_whitespace", input: "  tt0944947", want: "tt0944947"},
		{name: "valid_trailing_whitespace", input: "tt0944947\n", want: "tt0944947"},
		{name: "invalid_uppercase_prefix", input: "TT0000001", wantErr: true},
		{name: "invalid_no_digits", input: "tt", wantErr: true},
		{name: "invalid_digits_only", input: "123", wantErr: true},
		{name: "invalid_alpha", input: "abc", wantErr: true},
		{name: "invalid_empty", input: "", wantErr: true},
		{name: "invalid_whitespace_only", input: "   ", wantErr: true},
		{name: "invalid_letter_suffix", input: "tt12a34", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.NewIMDBID(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, errors.Is(err, domain.ErrInvalidIMDBID),
					"want ErrInvalidIMDBID, got %v", err)
				assert.Equal(t, domain.IMDBID(""), got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewQbitHash(t *testing.T) {
	t.Parallel()

	const valid40 = "0123456789abcdef0123456789abcdef01234567"
	const valid40Upper = "0123456789ABCDEF0123456789ABCDEF01234567"

	tests := []struct {
		name    string
		input   string
		want    domain.QbitHash
		wantErr bool
	}{
		{name: "valid_lowercase", input: valid40, want: domain.QbitHash(valid40)},
		{name: "valid_uppercase_normalized", input: valid40Upper, want: domain.QbitHash(valid40)},
		{name: "valid_mixed_case_normalized", input: "0123456789aBcDeF0123456789ABCDEF01234567", want: domain.QbitHash(valid40)},
		{name: "valid_with_leading_whitespace", input: "  " + valid40, want: domain.QbitHash(valid40)},
		{name: "valid_with_trailing_whitespace", input: valid40 + "\t\n", want: domain.QbitHash(valid40)},
		{name: "invalid_39_chars", input: valid40[:39], wantErr: true},
		{name: "invalid_41_chars", input: valid40 + "0", wantErr: true},
		{name: "invalid_non_hex_g", input: "g123456789abcdef0123456789abcdef01234567", wantErr: true},
		{name: "invalid_non_hex_xyz", input: "xyz3456789abcdef0123456789abcdef01234567", wantErr: true},
		{name: "invalid_empty", input: "", wantErr: true},
		{name: "invalid_whitespace_only", input: "    ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.NewQbitHash(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, errors.Is(err, domain.ErrInvalidQbitHash),
					"want ErrInvalidQbitHash, got %v", err)
				assert.Equal(t, domain.QbitHash(""), got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
