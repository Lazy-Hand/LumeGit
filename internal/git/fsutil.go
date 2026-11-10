package git

import "os"

// fileExists reports whether a path exists (file or directory).
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
