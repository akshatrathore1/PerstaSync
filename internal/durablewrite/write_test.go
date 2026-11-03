package durablewrite

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

type operationLog struct {
	operations []string
	failures   map[string]error
	parent     string
	shortWrite bool
}

func (log *operationLog) record(operation string) error {
	log.operations = append(log.operations, operation)
	return log.failures[operation]
}

func (log *operationLog) filesystem(t *testing.T, destination string) filesystem {
	t.Helper()
	log.parent = filepath.Dir(destination)
	return filesystem{
		createTemp: func(parent, pattern string) (file, error) {
			if parent != log.parent || pattern != ".replace-*" {
				t.Fatalf("temporary placement got %q/%q, want parent %q", parent, pattern, log.parent)
			}
			if err := log.record("create"); err != nil {
				return nil, err
			}
			return &loggedFile{log: log, kind: "file"}, nil
		},
		rename: func(temporary, target string) error {
			if temporary != filepath.Join(log.parent, ".replace-test") || target != destination {
				t.Fatalf("rename got %q -> %q", temporary, target)
			}
			return log.record("rename")
		},
		openDirectory: func(parent string) (directory, error) {
			if parent != log.parent {
				t.Fatalf("directory sync targets %q, want %q", parent, log.parent)
			}
			if err := log.record("open-directory"); err != nil {
				return nil, err
			}
			return &loggedFile{log: log, kind: "directory"}, nil
		},
		remove: func(temporary string) error {
			if temporary != filepath.Join(log.parent, ".replace-test") {
				t.Fatalf("cleanup targets an unexpected name: %q", temporary)
			}
			return log.record("remove")
		},
	}
}

type loggedFile struct {
	log  *operationLog
	kind string
}

func (handle *loggedFile) Name() string {
	return filepath.Join(handle.log.parent, ".replace-test")
}

func (handle *loggedFile) Write(data []byte) (int, error) {
	if err := handle.log.record("write"); err != nil {
		return 0, err
	}
	if handle.log.shortWrite {
		return 0, nil
	}
	return len(data), nil
}

func (handle *loggedFile) Chmod(mode os.FileMode) error {
	if mode != 0600 {
		return errors.New("unexpected reference file mode")
	}
	return handle.log.record("chmod")
}

func (handle *loggedFile) Sync() error {
	return handle.log.record(handle.kind + "-sync")
}

func (handle *loggedFile) Close() error {
	return handle.log.record(handle.kind + "-close")
}

func TestReplaceOrdering(t *testing.T) {
	destination := filepath.Join("parent", "state")
	log := &operationLog{}
	if err := replace(destination, []byte("state"), 0600, log.filesystem(t, destination)); err != nil {
		t.Fatal(err)
	}
	expected := []string{"create", "write", "chmod", "file-sync", "file-close", "rename", "open-directory", "directory-sync", "directory-close"}
	if !reflect.DeepEqual(log.operations, expected) {
		t.Fatalf("operations got %v, want %v", log.operations, expected)
	}
}

func TestReplaceFailures(t *testing.T) {
	destination := filepath.Join("parent", "state")
	operations := []string{"create", "write", "chmod", "file-sync", "file-close", "rename", "open-directory", "directory-sync", "directory-close"}
	for index, operation := range operations {
		t.Run(operation, func(t *testing.T) {
			failure := errors.New(operation + " failed")
			log := &operationLog{failures: map[string]error{operation: failure}}
			err := replace(destination, []byte("state"), 0600, log.filesystem(t, destination))
			if !errors.Is(err, failure) {
				t.Fatalf("lost original failure: %v", err)
			}
			expected := append([]string(nil), operations[:index+1]...)
			switch operation {
			case "write", "chmod", "file-sync":
				expected = append(expected, "file-close", "remove")
			case "file-close", "rename":
				expected = append(expected, "remove")
			case "directory-sync":
				expected = append(expected, "directory-close")
			}
			if !reflect.DeepEqual(log.operations, expected) {
				t.Fatalf("failed sequence got %v, want %v", log.operations, expected)
			}
		})
	}
}

func TestReplacePreservesCleanupErrors(t *testing.T) {
	destination := filepath.Join("parent", "state")
	writeErr := errors.New("write failed")
	closeErr := errors.New("close failed")
	removeErr := errors.New("remove failed")
	log := &operationLog{failures: map[string]error{"write": writeErr, "file-close": closeErr, "remove": removeErr}}
	err := replace(destination, []byte("state"), 0600, log.filesystem(t, destination))
	for _, expected := range []error{writeErr, closeErr, removeErr} {
		if !errors.Is(err, expected) {
			t.Fatalf("failure not preserved: %v in %v", expected, err)
		}
	}
	log = &operationLog{failures: map[string]error{"file-sync": writeErr, "remove": os.ErrNotExist}}
	err = replace(destination, []byte("state"), 0600, log.filesystem(t, destination))
	if !errors.Is(err, writeErr) || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected cleanup error: %v", err)
	}
	log = &operationLog{failures: map[string]error{"directory-sync": writeErr, "directory-close": closeErr}}
	err = replace(destination, []byte("state"), 0600, log.filesystem(t, destination))
	if !errors.Is(err, writeErr) || !errors.Is(err, closeErr) {
		t.Fatalf("directory errors were not both preserved: %v", err)
	}
}

func TestReplaceShortWrite(t *testing.T) {
	destination := filepath.Join("parent", "state")
	log := &operationLog{shortWrite: true}
	err := replace(destination, []byte("state"), 0600, log.filesystem(t, destination))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write accepted: %v", err)
	}
	expected := []string{"create", "write", "file-close", "remove"}
	if !reflect.DeepEqual(log.operations, expected) {
		t.Fatalf("short write published or skipped cleanup: %v", log.operations)
	}
}

func TestReplaceUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux supports the reference sequence")
	}
	parent := t.TempDir()
	err := Replace(filepath.Join(parent, "state"), []byte("state"), 0600)
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("unsupported platform accepted: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unsupported replacement modified the directory: %v err=%v", entries, err)
	}
}

func TestReplaceRoundTrip(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ordinary file and directory synchronization requires Linux")
	}
	parent := t.TempDir()
	destination := filepath.Join(parent, "state")
	for _, data := range [][]byte{[]byte("initial"), []byte("replacement"), {}} {
		if err := Replace(destination, data, 0600); err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(destination)
		if err != nil || !bytes.Equal(actual, data) {
			t.Fatalf("content round trip got %q, want %q, err=%v", actual, data, err)
		}
		info, err := os.Stat(destination)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("mode round trip failed: %v err=%v", info, err)
		}
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 || entries[0].Name() != "state" {
		t.Fatalf("temporary names remain: %v err=%v", entries, err)
	}
}
