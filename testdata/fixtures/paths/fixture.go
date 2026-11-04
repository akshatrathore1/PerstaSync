// Package paths encodes expected path and dispatch boundary levels in function names.
package paths

import (
	"io"
	"os"
	"path/filepath"
)

func L2DerivedParent(destination string) error {
	parent := filepath.Dir(destination)
	file, _ := os.CreateTemp(parent, ".state-*")
	file.WriteString("state")
	file.Sync()
	file.Close()
	os.Rename(file.Name(), destination)
	directory, _ := os.Open(parent)
	return directory.Sync()
}

func L2RecordedName() error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	file.Sync()
	file.Close()
	os.Rename(file.Name(), "state")
	directory, _ := os.Open(".")
	return directory.Sync()
}

func UndeterminedCrossDirectory() error {
	file, _ := os.CreateTemp("source", ".state-*")
	file.WriteString("state")
	file.Sync()
	file.Close()
	return os.Rename(file.Name(), "destination/state")
}

func UndeterminedUnrelatedPath(destination string) error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	file.Sync()
	file.Close()
	return os.Rename(file.Name(), destination)
}

func UndeterminedDifferentParent(destination string) error {
	parent := filepath.Dir(filepath.Dir(destination))
	file, _ := os.CreateTemp(parent, ".state-*")
	file.WriteString("state")
	file.Sync()
	file.Close()
	return os.Rename(file.Name(), destination)
}

func L1WrongDirectoryHandle(destination string) error {
	parent := filepath.Dir(destination)
	file, _ := os.CreateTemp(parent, ".state-*")
	file.WriteString("state")
	file.Sync()
	file.Close()
	os.Rename(file.Name(), destination)
	directory, _ := os.Open("unrelated")
	return directory.Sync()
}

type synchronizer interface {
	Sync() error
}

func L1UnknownInterface(syncer synchronizer) error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	syncer.Sync()
	file.Close()
	return os.Rename("state.tmp", "state")
}

func UndeterminedPathAndInterface(destination string, syncer synchronizer) error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	syncer.Sync()
	file.Close()
	return os.Rename("state.tmp", destination)
}

func L1UnknownExternalWriter() error {
	file, _ := os.Create("state.tmp")
	io.WriteString(file, "state")
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L1UnknownIgnoredInterfaceError(syncer synchronizer) error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0600)
	file.WriteString("state")
	syncer.Sync()
	os.Rename("state.tmp", "state")
	return file.Close()
}

func L1UnknownReturnedInterfaceError(syncer synchronizer) error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0600)
	file.WriteString("state")
	os.Rename("state.tmp", "state")
	return syncer.Sync()
}

func GenericCaller(syncer synchronizer) error {
	return L1UnknownGenericPublish[synchronizer](syncer)
}

func L1UnknownGenericPublish[Syncer synchronizer](syncer Syncer) error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	syncer.Sync()
	file.Close()
	return os.Rename("state.tmp", "state")
}
