package opcred

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// defaultOpPaths are the only places searched when MYSQL_OP_PATH is unset.
// PATH is never consulted, so a planted "op" earlier on PATH is ignored.
var defaultOpPaths = []string{"/opt/homebrew/bin/op", "/usr/local/bin/op"}

// opRequirement pins the binary to 1Password's Apple Developer team.
const opRequirement = `anchor apple generic and certificate leaf[subject.OU] = "2BUA8C4S2C"`

// ResolveOpPath returns the absolute, symlink-resolved path of an op binary
// signed by 1Password.
func ResolveOpPath(explicit string) (string, error) {
	candidates := defaultOpPaths
	if explicit != "" {
		if !filepath.IsAbs(explicit) {
			return "", fmt.Errorf("MYSQL_OP_PATH must be an absolute path, got %q", explicit)
		}
		candidates = []string{explicit}
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", p, err)
		}
		if err := verifySignature(resolved); err != nil {
			return "", err
		}
		return resolved, nil
	}
	return "", fmt.Errorf("1Password CLI not found at %s", strings.Join(candidates, " or "))
}

func verifySignature(path string) error {
	out, err := exec.Command("/usr/bin/codesign", "--verify", "--strict", "-R="+opRequirement, path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s is not signed by 1Password: %s", path, strings.TrimSpace(string(out)))
	}
	return nil
}
