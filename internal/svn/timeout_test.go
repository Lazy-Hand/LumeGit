package svn

import "testing"

// Data-moving commands must not inherit the 45s cap: killing an update
// mid-flight leaves the working copy locked.
func TestTimeoutFor(t *testing.T) {
	for _, sub := range []string{"update", "commit", "checkout", "switch", "merge", "cleanup"} {
		if got := timeoutFor([]string{sub}); got != longCmdTimeout {
			t.Errorf("%s: timeout %v, want %v", sub, got, longCmdTimeout)
		}
	}
	for _, sub := range []string{"status", "info", "log", "cat", "diff"} {
		if got := timeoutFor([]string{sub}); got != cmdTimeout {
			t.Errorf("%s: timeout %v, want %v", sub, got, cmdTimeout)
		}
	}
	if timeoutFor(nil) != cmdTimeout {
		t.Error("empty args should use the short cap")
	}
}
