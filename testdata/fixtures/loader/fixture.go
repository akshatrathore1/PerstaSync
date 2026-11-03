package loaderfixture

import "os"

func init() {
	panic("package loading must not execute application initialization")
}

func Publish() error {
	return os.Rename("state.tmp", "state")
}
