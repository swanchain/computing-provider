package conf

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvFileName is read from $CP_PATH at startup, if present.
const EnvFileName = ".env"

// LoadEnvFile reads KEY=VALUE lines from $CP_PATH/.env into the process
// environment.
//
// Secrets should not sit in config.toml: that file is the first thing an
// operator pastes into a support thread, and it is routinely copied between
// machines. Environment variables avoid that, but exporting one by hand means
// remembering it on every restart and putting it in a shell wrapper — so the
// provider reads a file that only it needs to know about.
//
// A variable already set in the real environment always wins, so an operator
// can override the file for one run without editing it.
func LoadEnvFile(cpRepoPath string) error {
	path := filepath.Join(cpRepoPath, EnvFileName)

	info, err := os.Stat(path)
	if err != nil {
		return nil // Absent is the normal case, not an error.
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	// Anything readable beyond the owner defeats the purpose of moving the
	// secret out of config.toml in the first place.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		fmt.Fprintf(os.Stderr, "warning: %s is readable by other users (mode %04o); run: chmod 600 %s\n", path, perm, path)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		key, value, ok := parseEnvLine(scanner.Text())
		if !ok {
			continue
		}
		if key == "" {
			return fmt.Errorf("%s line %d: empty variable name", path, lineNo)
		}
		if _, set := os.LookupEnv(key); set {
			continue // The real environment wins.
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("%s line %d: %w", path, lineNo, err)
		}
	}
	return scanner.Err()
}

// parseEnvLine splits one line into a key and value, reporting whether the line
// carried an assignment at all. Blank lines and # comments do not.
func parseEnvLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	// "export FOO=bar" is what an operator's muscle memory produces.
	line = strings.TrimPrefix(line, "export ")

	eq := strings.Index(line, "=")
	if eq < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:eq])
	value = strings.TrimSpace(line[eq+1:])

	// Strip one layer of matching quotes; a password with a # or a space in it
	// needs them, and leaving them in would silently corrupt the credential.
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	return key, value, true
}
