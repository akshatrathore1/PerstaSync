// Package openflags encodes expected Linux levels in each function name.
package openflags

import (
	"os"
	"syscall"
)

func L1SynchronousFlag() error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0600)
	file.WriteString("state")
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L1UnknownDynamicFlags(flags int) error {
	file, _ := os.OpenFile("state.tmp", flags, 0600)
	file.WriteString("state")
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L2ExplicitSynchronization() error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0600)
	file.WriteString("state")
	file.Sync()
	file.Close()
	os.Rename("state.tmp", "state")
	directory, _ := os.Open(".")
	return directory.Sync()
}

func L0UncoveredMetadata() error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0600)
	file.WriteString("state")
	file.Sync()
	file.Chmod(0400)
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L0EmptySynchronousCreation() error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0600)
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L0SynchronousWriteThenChmod() error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0600)
	file.WriteString("state")
	file.Chmod(0400)
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L1SynchronousChmodThenWrite() error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0600)
	file.Chmod(0400)
	file.Write([]byte("state"))
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L1UnknownDataSynchronousWrite() error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|syscall.O_DSYNC, 0600)
	file.WriteString("state")
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L1DataSynchronousAfterMetadataSync() error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|syscall.O_DSYNC, 0600)
	file.Sync()
	file.WriteAt([]byte("state"), 0)
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L0DataSynchronousWriteThenChmod() error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|syscall.O_DSYNC, 0600)
	file.WriteString("state")
	file.Chmod(0400)
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L0ZeroLengthSynchronousWrite() error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0600)
	file.WriteString("")
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L1UnknownPossiblyEmptyWrite(data []byte) error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0600)
	file.Write(data)
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L1EmptyCreationExplicitSync() error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0600)
	file.Sync()
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L0SynchronousWriteThenTruncate() error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0600)
	file.WriteString("state")
	file.Truncate(0)
	file.Close()
	return os.Rename("state.tmp", "state")
}
