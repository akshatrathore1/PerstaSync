package durablewrite

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

type file interface {
	io.Writer
	Name() string
	Chmod(os.FileMode) error
	Sync() error
	Close() error
}

type directory interface {
	Sync() error
	Close() error
}

type filesystem struct {
	createTemp    func(string, string) (file, error)
	rename        func(string, string) error
	openDirectory func(string) (directory, error)
	remove        func(string) error
}

func Replace(destination string, data []byte, mode os.FileMode) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("reference replacement requires Linux: %w", errors.ErrUnsupported)
	}
	return replace(destination, data, mode, filesystem{
		createTemp: func(parent, pattern string) (file, error) {
			return os.CreateTemp(parent, pattern)
		},
		rename: os.Rename,
		openDirectory: func(parent string) (directory, error) {
			return os.Open(parent)
		},
		remove: os.Remove,
	})
}

func replace(destination string, data []byte, mode os.FileMode, system filesystem) (result error) {
	parent := filepath.Dir(destination)
	temporary, err := system.createTemp(parent, ".replace-*")
	if err != nil {
		return err
	}
	closed := false
	published := false
	defer func() {
		if !closed {
			result = errors.Join(result, temporary.Close())
		}
		if !published {
			removeErr := system.remove(temporary.Name())
			if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				result = errors.Join(result, removeErr)
			}
		}
	}()
	written, err := temporary.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	closed = true
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := system.rename(temporary.Name(), destination); err != nil {
		return err
	}
	published = true
	parentDirectory, err := system.openDirectory(parent)
	if err != nil {
		return err
	}
	syncErr := parentDirectory.Sync()
	closeErr := parentDirectory.Close()
	return errors.Join(syncErr, closeErr)
}
