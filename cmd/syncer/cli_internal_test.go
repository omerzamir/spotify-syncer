package main

import (
	"bytes"
	"flag"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestParseInterspersed(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantPositional []string
		wantName       string
		wantDryRun     bool
	}{
		{
			name:           "flags first",
			args:           []string{"--name", "X", "a", "b"},
			wantPositional: []string{"a", "b"},
			wantName:       "X",
		},
		{
			name:           "flags last",
			args:           []string{"a", "b", "--name=X", "--dry-run"},
			wantPositional: []string{"a", "b"},
			wantName:       "X",
			wantDryRun:     true,
		},
		{name: "flags between", args: []string{"a", "-n", "X", "b"}, wantPositional: []string{"a", "b"}, wantName: "X"},
		{
			name:           "double dash ends flags",
			args:           []string{"a", "--", "--name", "b"},
			wantPositional: []string{"a", "--name", "b"},
		},
		{name: "no args", args: nil, wantPositional: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				configPath string
				name       string
				dryRun     bool
			)
			fs := newFlagSet("test", "test", io.Discard, &configPath)
			fs.StringVar(&name, "name", "", "")
			fs.StringVar(&name, "n", "", "")
			fs.BoolVar(&dryRun, "dry-run", false, "")

			got, err := parseInterspersed(fs, tt.args)
			if err != nil {
				t.Fatalf("parseInterspersed: %v", err)
			}
			if !slices.Equal(got, tt.wantPositional) || name != tt.wantName || dryRun != tt.wantDryRun {
				t.Errorf("got positional=%q name=%q dryRun=%t, want %q %q %t",
					got, name, dryRun, tt.wantPositional, tt.wantName, tt.wantDryRun)
			}
		})
	}
}

func TestStringList(t *testing.T) {
	var list stringList
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Var(&list, "source", "")

	if err := fs.Parse([]string{"--source", "a", "--source", "b, c,,", "--source=d"}); err != nil {
		t.Fatal(err)
	}

	if want := []string{"a", "b", "c", "d"}; !slices.Equal(list, want) {
		t.Errorf("list = %q, want %q", list, want)
	}
}

func TestRunExitCodes(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{name: "no command", args: nil, wantCode: exitUsage, wantStderr: "no command given"},
		{name: "unknown command", args: []string{"sync"}, wantCode: exitUsage, wantStderr: `unknown command "sync"`},
		{name: "help", args: []string{"help"}, wantCode: exitSuccess},
		{
			name:       "command help",
			args:       []string{"merge", "--help"},
			wantCode:   exitSuccess,
			wantStderr: "Usage: syncer merge",
		},
		{
			name:       "unknown flag",
			args:       []string{"run", "--bogus"},
			wantCode:   exitUsage,
			wantStderr: "flag provided but not defined",
		},
		{
			name:       "invalid mode",
			args:       []string{"run", "--mode=turbo"},
			wantCode:   exitUsage,
			wantStderr: `invalid mode "turbo"`,
		},
		{
			name:       "merge needs two sources",
			args:       []string{"merge", "only-one", "-n", "X"},
			wantCode:   exitUsage,
			wantStderr: "at least two source playlists",
		},
		{
			name:       "merge needs a name",
			args:       []string{"merge", "a", "b"},
			wantCode:   exitUsage,
			wantStderr: "target playlist name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			if code := run(t.Context(), tt.args, &stdout, &stderr); code != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d (stderr: %s)", tt.args, code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want containing %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
