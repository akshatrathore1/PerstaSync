// Package direct encodes NoSync=L0, FileOnly=L1, FileAndDirectory=L2,
// MetadataAfterSync=L0, and both path-boundary functions=undetermined.
package direct

import "os"

func NoSync() error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	file.Close()
	return os.Rename("state.tmp", "state")
}

func FileOnly() error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	file.Sync()
	file.Close()
	return os.Rename("state.tmp", "state")
}

func FileAndDirectory() error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	file.Sync()
	file.Close()
	os.Rename("state.tmp", "state")
	directory, _ := os.Open(".")
	return directory.Sync()
}

func MetadataAfterSync() error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	file.Sync()
	file.Chmod(0600)
	file.Close()
	return os.Rename("state.tmp", "state")
}

func CrossDirectory() error {
	file, _ := os.Create("source/state.tmp")
	file.WriteString("state")
	file.Sync()
	file.Close()
	return os.Rename("source/state.tmp", "destination/state")
}

func NonLiteral(destination string) error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	file.Sync()
	file.Close()
	return os.Rename("state.tmp", destination)
}
