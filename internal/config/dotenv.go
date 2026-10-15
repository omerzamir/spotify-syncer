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
//
// In an unquoted value a comment starts at a '#' preceded by a space or tab,
// so KEY=a#b keeps "a#b".
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

	value, err := parseDotEnvValue(raw)
	if err != nil {
		return "", "", false, fmt.Errorf("value of %s: %w", key, err)
	}

	return key, value, true, nil
}

// parseDotEnvValue parses the text after the '=' of an assignment. raw must
// not be trimmed, so a '#' directly after the '=' is told apart from one after
// whitespace.
func parseDotEnvValue(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)

	switch {
	case strings.HasPrefix(trimmed, `"`):
		value, err := strconv.Unquote(trimmed)
		if err != nil {
			return "", fmt.Errorf("malformed double-quoted value: %w", err)
		}
		return value, nil
	case strings.HasPrefix(trimmed, "'"):
		if len(trimmed) < 2 || !strings.HasSuffix(trimmed, "'") {
			return "", errors.New("unterminated single-quoted value")
		}
		return trimmed[1 : len(trimmed)-1], nil
	default:
		return strings.TrimSpace(stripDotEnvComment(raw)), nil
	}
}

// stripDotEnvComment removes a trailing comment from an unquoted value. A
// comment starts at the first '#' that is preceded by a space or tab.
func stripDotEnvComment(raw string) string {
	for i := 1; i < len(raw); i++ {
		if raw[i] == '#' && (raw[i-1] == ' ' || raw[i-1] == '\t') {
			return raw[:i]
		}
	}
	return raw
}
