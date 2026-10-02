package tailnet

import (
	"fmt"
	"os"
)

// checkBinary accepts an existing regular file. Windows guards Program Files
// with ACLs that the Unix permission bits Go reports do not describe.
func checkBinary(path string) error {
	info, err := os.Stat(path)
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", path)
	}
	return err
}
