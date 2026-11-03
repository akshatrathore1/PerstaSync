package openflags

import "os"

func L1UnknownSynchronousFlag() error {
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
