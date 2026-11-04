// Package structs encodes expected levels in publication function names.
package structs

import "os"

type writer struct {
	file        *os.File
	temporary   string
	destination string
}

func (pending *writer) L2Commit() error {
	pending.file.Close()
	os.Rename(pending.temporary, pending.destination)
	directory, _ := os.Open(".")
	return directory.Sync()
}

func UseStructWriter() error {
	pending := newWriter("state.tmp", "state")
	pending.writeAndSync()
	return pending.L2Commit()
}

func L0DifferentInstance() error {
	pending := newWriter("state.tmp", "state")
	other := newWriter("other.tmp", "other")
	pending.file.WriteString("state")
	other.writeAndSync()
	pending.file.Close()
	return os.Rename(pending.temporary, pending.destination)
}

func L0DifferentField() error {
	file, _ := os.Create("state.tmp")
	other, _ := os.Create("other.tmp")
	pair := struct{ first, second *os.File }{file, other}
	pair.first.WriteString("state")
	pair.second.Sync()
	pair.first.Close()
	return os.Rename("state.tmp", "state")
}

func L0MetadataAfterMethodSync() error {
	pending := newWriter("state.tmp", "state")
	pending.writeAndSync()
	pending.changeMode()
	pending.file.Close()
	return os.Rename(pending.temporary, pending.destination)
}

func L0ReassignedField() error {
	pending := newWriter("state.tmp", "state")
	pending.writeAndSync()
	replacement, _ := os.Create("other.tmp")
	pending.file = replacement
	pending.temporary = "other.tmp"
	pending.file.WriteString("replacement")
	pending.file.Close()
	return os.Rename(pending.temporary, pending.destination)
}

func L1CopiedStructDoesNotAlias() error {
	file, _ := os.Create("state.tmp")
	other, _ := os.Create("other.tmp")
	original := struct{ file *os.File }{file}
	copied := original
	copied.file = other
	original.file.WriteString("state")
	original.file.Sync()
	original.file.Close()
	copied.file.Close()
	return os.Rename("state.tmp", "state")
}

func newWriter(temporary, destination string) *writer {
	file, _ := os.Create(temporary)
	return &writer{file: file, temporary: temporary, destination: destination}
}

func (pending *writer) writeAndSync() error {
	pending.file.WriteString("state")
	return pending.file.Sync()
}

func (pending *writer) changeMode() error {
	return pending.file.Chmod(0400)
}
