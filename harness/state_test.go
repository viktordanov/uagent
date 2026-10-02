package harness_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/harness"
)

func TestMoveLegacyStateDir(t *testing.T) {
	t.Run("moves the old directory once", func(t *testing.T) {
		base := t.TempDir()
		t.Setenv("XDG_STATE_HOME", base)
		legacy := filepath.Join(base, "unreal-agent")
		require.NoError(t, os.MkdirAll(filepath.Join(legacy, "runs"), 0o700))

		require.NoError(t, harness.MoveLegacyStateDir())

		assert.Equal(t, filepath.Join(base, "uagent"), harness.DefaultStateDir())
		assert.DirExists(t, filepath.Join(base, "uagent", "runs"))
		assert.NoDirExists(t, legacy)
	})

	t.Run("keeps an existing new directory", func(t *testing.T) {
		base := t.TempDir()
		t.Setenv("XDG_STATE_HOME", base)
		legacy := filepath.Join(base, "unreal-agent")
		require.NoError(t, os.MkdirAll(legacy, 0o700))
		require.NoError(t, os.MkdirAll(filepath.Join(base, "uagent"), 0o700))

		require.NoError(t, harness.MoveLegacyStateDir())

		assert.DirExists(t, legacy)
	})

	t.Run("does nothing without an old directory", func(t *testing.T) {
		base := t.TempDir()
		t.Setenv("XDG_STATE_HOME", base)

		require.NoError(t, harness.MoveLegacyStateDir())

		assert.NoDirExists(t, filepath.Join(base, "uagent"))
	})
}
