package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputeAvatarHash(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty returns empty",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace-only returns empty",
			input: "   \t \n",
			want:  "",
		},
		{
			name:  "trims leading and trailing whitespace",
			input: "  MyEmailAddress@example.com  ",
			want:  "0bc83cb571cd1c50ba6f3e8a78ef1346", // md5("myemailaddress@example.com")
		},
		{
			name:  "lowercases mixed case",
			input: "MyEmailAddress@example.com",
			want:  "0bc83cb571cd1c50ba6f3e8a78ef1346",
		},
		{
			name:  "stable for already-normalised input",
			input: "myemailaddress@example.com",
			want:  "0bc83cb571cd1c50ba6f3e8a78ef1346",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ComputeAvatarHash(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}
