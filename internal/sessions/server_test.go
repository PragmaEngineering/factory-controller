package sessions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalServiceIsIndependentOfProjectAndLegacyRuntimeOverride(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	want := filepath.Join(home, ".factory", "runtime")
	for _, project := range []string{t.TempDir(), t.TempDir()} {
		t.Chdir(project)
		t.Setenv("FACTORY_RUNTIME_DIR", filepath.Join(project, "old-eval-runtime"))
		root, err := RuntimeRoot()
		require.NoError(t, err)
		require.Equal(t, want, root)
	}
}
