package procstart

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLinuxStartTime(t *testing.T) {
	for name, tc := range map[string]struct{ stat, want string }{
		"plain name": {
			stat: "4242 (sleep) S 1 4242 4242 0 -1 4194304 97 0 0 0 0 0 0 0 20 0 1 0 8675309 2338816 128 18446744073709551615 1 1 0 0 0 0 0 0 0 0 0 0 17 3 0 0 0 0 0",
			want: "8675309",
		},
		"name with spaces and parentheses": {
			stat: "77 (a) b (c) R 1 77 77 0 -1 4194304 97 0 0 0 0 0 0 0 20 0 1 0 123 2338816 128 18446744073709551615 1 1 0 0 0 0 0 0 0 0 0 0 17 3 0 0 0 0 0\n",
			want: "123",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := linuxStartTime(tc.stat)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
	for _, stat := range []string{"", "4242 sleep S 1", "4242 (sleep) S 1 4242"} {
		_, err := linuxStartTime(stat)
		assert.Error(t, err, stat)
	}
}

func TestDarwinStartTime(t *testing.T) {
	assert.Equal(t, "1790080486.018109", darwinStartTime(1790080486, 18109))
}
