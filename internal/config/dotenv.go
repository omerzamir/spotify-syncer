package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// LoadDotEnv sets environment variables from a .env file. Variables that are
// already set are left untouched, so the real environment always wins. A
// missing file is not an error.
//
// Supported syntax, one assignment per line:
//
//	# comment
//	KEY=value
//	export KEY=value
//	KEY="double quoted, with \n escapes"
//	KEY='single quoted, literal'
//	KEY=value # trailing comment (unquoted values only)
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		key, value, ok, parseErr := parseDotEnvLine(scanner.Text())
		if parseErr != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNo, parseErr)
		}
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if setErr := os.Setenv(key, value); setErr != nil {
			return fmt.Errorf("%s:%d: setting %s: %w", path, lineNo, key, setErr)
		}
	}

	if err = scanner.Err(); err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	return nil
}

// parseDotEnvLine parses one .env line. ok is false for blank and comment
// lines.
func parseDotEnvLine(line string) (string, string, bool, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false, nil
	}

	line = strings.TrimPrefix(line, "export ")

	key, raw, found := strings.Cut(line, "=")
	key = strings.TrimSpace(key)
	if !found || key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false, fmt.Errorf("invalid assignment %q", line)
	}

	value, err := parseDotEnvValue(strings.TrimSpace(raw))
	if err != nil {
		return "", "", false, fmt.Errorf("value of %s: %w", key, err)
	}

	return key, value, true, nil
}

func parseDotEnvValue(raw string) (string, error) {
	switch {
	case strings.HasPrefix(raw, `"`):
		value, err := strconv.Unquote(raw)
		if err != nil {
			return "", fmt.Errorf("malformed double-quoted value: %w", err)
		}
		return value, nil
	case strings.HasPrefix(raw, "'"):
		if len(raw) < 2 || !strings.HasSuffix(raw, "'") {
			return "", errors.New("unterminated single-quoted value")
		}
		return raw[1 : len(raw)-1], nil
	default:
		value, _, _ := strings.Cut(raw, " #")
		return strings.TrimSpace(value), nil
	}
}
