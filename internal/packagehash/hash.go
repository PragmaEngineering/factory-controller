// Package packagehash verifies the bytes of a local agent package snapshot.
package packagehash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"path/filepath"

	"controls/execution"
)

func Directory(root string, excluded ...string) (string, error) {
	digest := sha256.New()
	if err := hashPath(digest, root, ".", 0, excluded); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func Verify(pkg execution.PackageSnapshot) error {
	actual, err := Directory(pkg.Source, pkg.ExcludedPaths...)
	if err != nil {
		return err
	}
	if actual != pkg.Digest {
		return fmt.Errorf("package %q changed since execution began", pkg.Name)
	}
	return nil
}

func hashPath(digest hash.Hash, path, relative string, depth int, excluded []string) error {
	for _, directory := range excluded {
		if filepath.Clean(path) == filepath.Clean(directory) {
			return nil
		}
	}
	if depth > 32 {
		return fmt.Errorf("agent package links are nested too deeply at %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := hashPath(digest, filepath.Join(path, entry.Name()), filepath.Join(relative, entry.Name()), depth+1, excluded); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported package file %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, _ = digest.Write([]byte(relative))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(data)
	return nil
}
