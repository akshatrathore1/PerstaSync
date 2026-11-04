// Package callees encodes expected levels at the publication functions.
package callees

import "os"

func L2CalleeEffects() error {
	file, _ := os.Create("state.tmp")
	writeAndSync(file)
	file.Close()
	os.Rename("state.tmp", "state")
	directory, _ := os.Open(".")
	return syncHandle(directory)
}

func L0CalleeMetadata() error {
	file, _ := os.Create("state.tmp")
	writeAndSync(file)
	changeMode(file)
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L1DirectorySyncTooEarly() error {
	file, _ := os.Create("state.tmp")
	writeAndSync(file)
	file.Close()
	directory, _ := os.Open(".")
	syncHandle(directory)
	return os.Rename("state.tmp", "state")
}

func L2ReturnedHandle() error {
	file := newFile()
	writeAndSync(file)
	file.Close()
	os.Rename("state.tmp", "state")
	directory, _ := os.Open(".")
	return syncHandle(directory)
}

func CompletePublicationCaller() error {
	file, _ := os.Create("state.tmp")
	writeAndSync(file)
	file.Close()
	L2PublicationInCallee()
	directory, _ := os.Open(".")
	return directory.Sync()
}

func L1UnknownRecursiveCall() error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	recursiveSync(file)
	file.Close()
	return os.Rename("state.tmp", "state")
}

func L2PublicationInCallee() error {
	return os.Rename("state.tmp", "state")
}

func L1IgnoredCalleeClose() error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0600)
	file.WriteString("state")
	closeHandle(file)
	return os.Rename("state.tmp", "state")
}

func L1ReturnedCalleeClose() error {
	file, _ := os.OpenFile("state.tmp", os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0600)
	file.WriteString("state")
	os.Rename("state.tmp", "state")
	return closeHandle(file)
}

func SyncedContext() error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	file.Sync()
	file.Close()
	return L1UnknownMixedContexts()
}

func UnsyncedContext() error {
	file, _ := os.Create("state.tmp")
	file.WriteString("state")
	file.Close()
	return L1UnknownMixedContexts()
}

func L1UnknownMixedContexts() error {
	return os.Rename("state.tmp", "state")
}

func writeAndSync(file *os.File) error {
	file.WriteString("state")
	return syncHandle(file)
}

func syncHandle(file *os.File) error {
	return file.Sync()
}

func changeMode(file *os.File) error {
	return file.Chmod(0400)
}

func newFile() *os.File {
	file, _ := os.Create("state.tmp")
	return file
}

func recursiveSync(file *os.File) error {
	return recursiveSync(file)
}

func closeHandle(file *os.File) error {
	return file.Close()
}
