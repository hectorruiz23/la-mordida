package disk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveStreamsHashAndWritesContentAddressedObject(t *testing.T) {
	root := filepath.Join(t.TempDir(), "objects")
	store := New(root)
	content := []byte("pliego de prescripciones técnicas")

	object, err := store.Save(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])
	if object.SHA256 != want {
		t.Fatalf("sha256 = %s, want %s", object.SHA256, want)
	}
	if object.Bytes != int64(len(content)) {
		t.Fatalf("bytes = %d, want %d", object.Bytes, len(content))
	}

	stored, err := os.ReadFile(filepath.Join(root, want[:2], want))
	if err != nil {
		t.Fatalf("read stored object: %v", err)
	}
	if !bytes.Equal(stored, content) {
		t.Fatalf("stored bytes = %q, want %q", stored, content)
	}
	if !store.Has(want) {
		t.Fatal("Has says the object is missing")
	}
}

// failingReader yields some bytes and then fails, like a connection cut before
// the download ends.
type failingReader struct {
	read bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, errors.New("connection cut")
	}
	r.read = true
	return copy(p, "medio pliego"), nil
}

func TestInterruptionBeforeRenameLeavesNoObjectAndNoTemp(t *testing.T) {
	root := filepath.Join(t.TempDir(), "objects")
	store := New(root)

	if _, err := store.Save(context.Background(), &failingReader{}); err == nil {
		t.Fatal("Save accepted a body that failed midway")
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("root after interrupted download = %v, want empty", entries)
	}
	if matches, err := filepath.Glob(filepath.Join(root, ".download-*")); err != nil || len(matches) != 0 {
		t.Fatalf("temporary files after interrupted download = %v (%v)", matches, err)
	}
}

func TestSameContentKeepsOneFileAndReturnsTheSameHash(t *testing.T) {
	root := filepath.Join(t.TempDir(), "objects")
	store := New(root)
	content := []byte("mismo pliego")

	first, err := store.Save(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	second, err := store.Save(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatalf("hashes = %s / %s", first.SHA256, second.SHA256)
	}

	var files []string
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk objects: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("object files = %v, want 1", files)
	}
}

func TestRestoreIntoAnEmptyDirectoryRecoversEveryByte(t *testing.T) {
	content := []byte{0x00, 0x01, 0xfe, 0xff, 'p', 'd', 'f', '\n'}
	source := filepath.Join(t.TempDir(), "objects")
	store := New(source)
	object, err := store.Save(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	restored := filepath.Join(t.TempDir(), "restored", "objects")
	if err := copyTree(source, restored); err != nil {
		t.Fatalf("restore objects: %v", err)
	}

	recovered, err := New(restored).Open(object.SHA256)
	if err != nil {
		t.Fatalf("open restored object: %v", err)
	}
	defer recovered.Close()
	got, err := io.ReadAll(recovered)
	if err != nil {
		t.Fatalf("read restored object: %v", err)
	}
	if len(got) != len(content) {
		t.Fatalf("restored bytes = %d, want %d", len(got), len(content))
	}
	for index := range content {
		if got[index] != content[index] {
			t.Fatalf("byte %d = %#x, want %#x", index, got[index], content[index])
		}
	}
}

func TestHashesMustBeHexToReachTheFilesystem(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "objects"))
	if store.Has("../../etc/passwd") {
		t.Fatal("Has accepted a path instead of a hash")
	}
	if _, err := store.Open("../../etc/passwd"); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("Open traversal err = %v", err)
	}
}

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	})
}
