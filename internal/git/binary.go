package git

// looksBinary applies a conservative NUL/invalid-byte check used only
// for untracked files (git does not show untracked content in diff
// output). For tracked files numstat's "-" decides.
func looksBinary(b []byte) bool {
	const max = 8000
	if len(b) > max {
		b = b[:max]
	}
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}
