package localexec

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// loadEnvFile supplements only the secret resolver's snapshot. It deliberately
// does not modify os.Environ or the process runner's controlled environment.
func loadEnvFile(o options, inherited []string) ([]string, error) {
	if o.envFile == "" {
		if o.envFileSet {
			return nil, invalid("--env-file requires a nonempty path")
		}
		return inherited, nil
	}
	if err := checkEnvFileLocation(o); err != nil {
		return nil, err
	}
	info, err := os.Lstat(o.envFile)
	if err != nil || !info.Mode().IsRegular() {
		return nil, invalid("env file must be a readable regular file (no symlinks)")
	}

	f, err := os.Open(o.envFile)
	if err != nil {
		return nil, invalid("cannot open env file")
	}
	defer f.Close() //nolint:errcheck // Read-only input.
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, invalid("cannot read env file within 1 MiB limit")
	}
	values, err := parseEnvFile(string(data))
	if err != nil {
		return nil, err
	}
	for _, item := range inherited {
		name, value, found := strings.Cut(item, "=")
		if found {
			values[name] = value // An inherited empty value also takes precedence.
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	merged := make([]string, 0, len(names))
	for _, name := range names {
		merged = append(merged, name+"="+values[name])
	}
	return merged, nil
}

func checkEnvFileLocation(o options) error {
	path, err := canonicalEnvPath(o.envFile)
	if err != nil {
		return invalid("cannot resolve env file location")
	}
	for _, root := range []string{o.blueprint, o.workspace} {
		if root == "" {
			continue
		}
		directory, err := canonicalEnvPath(root)
		if err != nil {
			return invalid("cannot resolve Blueprint or workspace location")
		}
		rel, err := filepath.Rel(directory, path)
		if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return invalid("env file must be outside Blueprint and workspace")
		}
	}
	return nil
}

// Resolve existing ancestors too, since the workspace may not exist yet.
func canonicalEnvPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) || filepath.Dir(absolute) == absolute {
		return "", err
	}
	parent, err := canonicalEnvPath(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

func parseEnvFile(data string) (map[string]string, error) {
	values := make(map[string]string)
	for i, line := range strings.Split(data, "\n") {
		if !utf8.ValidString(line) || strings.ContainsRune(line, 0) {
			return nil, invalid(fmt.Sprintf("invalid env file at line %d", i+1))
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
			line = strings.TrimSpace(line[len("export"):])
		}
		key, raw, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !found || !envKey.MatchString(key) {
			return nil, invalid(fmt.Sprintf("invalid env file assignment at line %d", i+1))
		}
		value, ok := parseEnvValue(strings.TrimSpace(raw))
		if !ok {
			return nil, invalid(fmt.Sprintf("invalid env file value at line %d", i+1))
		}
		values[key] = value
	}
	return values, nil
}

func parseEnvValue(raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	quote := raw[0]
	if quote != '\'' && quote != '"' {
		for i := range len(raw) {
			if raw[i] == '#' && (i == 0 || raw[i-1] == ' ' || raw[i-1] == '\t') {
				return strings.TrimSpace(raw[:i]), true
			}
		}
		return raw, true
	}
	var value strings.Builder
	for i := 1; i < len(raw); i++ {
		c := raw[i]
		if c == quote {
			rest := raw[i+1:]
			if rest == "" {
				return value.String(), true
			}
			if rest[0] != ' ' && rest[0] != '\t' {
				return "", false
			}
			rest = strings.TrimSpace(rest)
			return value.String(), rest == "" || strings.HasPrefix(rest, "#")
		}
		if quote == '"' && c == '\\' && i+1 < len(raw) {
			i++
			c = raw[i]
			switch c {
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case '\\', '"', '$':
			default:
				value.WriteByte('\\')
			}
		}
		value.WriteByte(c)
	}
	return "", false
}
