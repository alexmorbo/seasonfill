package commands

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthMode_DeprecatedNoOp(t *testing.T) {
	t.Parallel()
	require.NoError(t, AuthMode(nil))
	require.NoError(t, AuthMode([]string{"--get"}))
	require.NoError(t, AuthMode([]string{"--set", "forms"}))
	require.NoError(t, AuthMode([]string{"whatever"}))
}
