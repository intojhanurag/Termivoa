package tailnet

import "os"

// safeBinary accepts an existing regular file. Windows guards Program Files
// with ACLs that the Unix permission bits Go reports do not describe.
func safeBinary(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
