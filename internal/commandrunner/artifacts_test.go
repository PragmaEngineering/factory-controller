package commandrunner

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"controls/execution"
	"controls/internal/artifacts"
)

func TestCollectRequiredArtifactsUsesWorkflowPaths(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "reviews"), 0700); err != nil {
		t.Fatal(err)
	}
	content := []byte("review complete\n")
	path := filepath.Join(project, "reviews", "architecture.md")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	artifacts, err := artifacts.Collect(project, []string{"reviews/architecture.md"}, []execution.Artifact{{Name: "Architecture review", URI: path}})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("got %d artifacts", len(artifacts))
	}
	actual := artifacts[1]
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Name != "reviews/architecture.md" || actual.URI != resolvedPath || actual.Digest != fmt.Sprintf("%x", sha256.Sum256(content)) {
		t.Fatalf("wrong required artifact: %+v", actual)
	}
}

func TestCollectRequiredArtifactsRejectsMissingOrEscapingOutput(t *testing.T) {
	project := t.TempDir()
	if _, err := artifacts.Collect(project, []string{"missing.md"}, nil); err == nil {
		t.Fatal("missing required output accepted")
	}
	if _, err := artifacts.Collect(project, []string{"../outside.md"}, nil); err == nil {
		t.Fatal("escaping required output accepted")
	}
}
