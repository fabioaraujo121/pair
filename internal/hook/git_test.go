package hook

import "os/exec"

func execGit(dir string, args ...string) ([]byte, error) {
	return exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
}
