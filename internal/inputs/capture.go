package inputs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxInputBytes int64 = 32 << 20

type Document struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type Bundle struct {
	Brief     string     `json:"brief,omitempty"`
	Documents []Document `json:"documents,omitempty"`
}

// Capture stores an immutable copy of each CLI input for one execution.
// Paths are resolved from the caller's working directory, independent of the
// factory.json location. A single '-' reads stdin once.
func Capture(directory, brief string, files []string, stdin io.Reader) (Bundle, error) {
	if strings.TrimSpace(brief) == "" && len(files) == 0 {
		return Bundle{}, errors.New("provide a brief or at least one --file")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return Bundle{}, err
	}
	bundle := Bundle{Brief: brief}
	var total int64
	stdinUsed := false
	for i, source := range files {
		var reader io.Reader
		var closeReader func() error
		name := filepath.Base(source)
		if source == "-" {
			if stdinUsed {
				return Bundle{}, errors.New("stdin may appear only once")
			}
			stdinUsed = true
			if stdin == nil {
				return Bundle{}, errors.New("stdin is unavailable")
			}
			reader = stdin
			name = "stdin"
		} else {
			absolute, err := filepath.Abs(source)
			if err != nil {
				return Bundle{}, err
			}
			file, err := os.Open(absolute)
			if err != nil {
				return Bundle{}, fmt.Errorf("open input %s: %w", source, err)
			}
			info, err := file.Stat()
			if err != nil || !info.Mode().IsRegular() {
				_ = file.Close()
				return Bundle{}, fmt.Errorf("input %s must be a regular file", source)
			}
			reader, closeReader = file, file.Close
			source = absolute
		}
		path := filepath.Join(directory, fmt.Sprintf("%03d-%s", i+1, name))
		out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			if closeReader != nil {
				_ = closeReader()
			}
			return Bundle{}, err
		}
		hash := sha256.New()
		count, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(reader, maxInputBytes-total+1))
		closeErr := out.Close()
		if closeReader != nil {
			_ = closeReader()
		}
		if copyErr != nil || closeErr != nil {
			return Bundle{}, errors.Join(copyErr, closeErr)
		}
		total += count
		if total > maxInputBytes {
			return Bundle{}, fmt.Errorf("execution inputs exceed %d bytes", maxInputBytes)
		}
		bundle.Documents = append(bundle.Documents, Document{
			Name: name, Source: source, Path: path, Digest: hex.EncodeToString(hash.Sum(nil)), Size: count,
		})
	}
	return bundle, nil
}
