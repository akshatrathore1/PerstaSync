// Package captures encodes expected levels under normal Go defer execution.
package captures

import "os"

func L2CapturedCleanup() error {
	var file *os.File
	defer func() { file.Close() }()
	file, _ = os.Create("state.tmp")
	file.WriteString("state")
	file.Sync()
	os.Rename("state.tmp", "state")
	directory, _ := os.Open(".")
	return directory.Sync()
}

func L0DeferredFileSync() error {
	var file *os.File
	defer func() {
		file.Sync()
		file.Close()
	}()
	file, _ = os.Create("state.tmp")
	file.WriteString("state")
	return os.Rename("state.tmp", "state")
}

func L2DeferredPublication() {
	var file *os.File
	defer func() {
		file.Sync()
		file.Close()
		os.Rename("state.tmp", "state")
		directory, _ := os.Open(".")
		directory.Sync()
	}()
	file, _ = os.Create("state.tmp")
	file.WriteString("state")
}

func L0ReassignedCapturedCell() error {
	file, _ := os.Create("state.tmp")
	original := file
	flush := func() { file.Sync() }
	original.WriteString("state")
	other, _ := os.Create("other.tmp")
	file = other
	flush()
	original.Close()
	return os.Rename("state.tmp", "state")
}

func L2DeferLIFO() {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	defer func() {
		os.Rename("state.tmp", "state")
		directory, _ := os.Open(".")
		directory.Sync()
	}()
	defer func() {
		file.Sync()
		file.Close()
	}()
}

func L0DeferReverse() {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	defer func() {
		file.Sync()
		file.Close()
	}()
	defer func() {
		os.Rename("state.tmp", "state")
		directory, _ := os.Open(".")
		directory.Sync()
	}()
}

func L1DeferredArgumentSnapshot() {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	defer func(handle *os.File) {
		handle.Sync()
		handle.Close()
		os.Rename("state.tmp", "state")
	}(file)
	file, _ = os.Create("other.tmp")
	file.Close()
}

func L1UnknownConcurrentSync() error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	go func() { file.Sync() }()
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L0DeferredMethodSync() error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	defer file.Close()
	defer file.Sync()
	return os.Rename("state.tmp", "state")
}

func L0DeferredMetadataAfterSync() {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	defer func() {
		file.Close()
		os.Rename("state.tmp", "state")
	}()
	defer file.Chmod(0400)
	defer file.Sync()
}
