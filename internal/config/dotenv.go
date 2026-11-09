package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// loadDotEnv sets variables from KEY=VALUE lines of the file at path. Variables
// that are already set in the environment are left alone. A missing file is
// not an error.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, value, ok := parseDotEnvLine(sc.Text())
		if !ok {
			continue
		}
		if _, set := os.LookupEnv(key); set {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s from %s: %w", key, path, err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

// parseDotEnvLine splits one line into a key and an unquoted value. It
// reports false for blank lines, comments and lines without "=".
func parseDotEnvLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	key, value, ok = strings.Cut(strings.TrimPrefix(line, "export "), "=")
	if !ok {
		return "", "", false
	}
	key, value = strings.TrimSpace(key), strings.TrimSpace(value)
	return key, unquote(value), true
}

// unquote strips one pair of matching single or double quotes.
func unquote(s string) string {
	if len(s) < 2 {
		return s
	}
	if q := s[0]; (q == '"' || q == '\'') && s[len(s)-1] == q {
		return s[1 : len(s)-1]
	}
	return s
}
