package inputs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureCopiesDocuments(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "request.txt")
	if err := os.WriteFile(source, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	bundle, err := Capture(filepath.Join(root, "snapshot"), "task", []string{source, "-"}, strings.NewReader("stdin content"))
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Documents) != 2 || bundle.Documents[0].Digest == "" || bundle.Documents[1].Name != "stdin" {
		t.Fatalf("unexpected input bundle: %+v", bundle)
	}
	if err := os.WriteFile(source, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(bundle.Documents[0].Path)
	if err != nil || string(data) != "original" {
		t.Fatalf("snapshot changed with source: %q, %v", data, err)
	}
}
