package main

import (
	"bytes"
	"errors"
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
		wantUsageErr   bool
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
		{
			name:         "double dash is never a flag value",
			args:         []string{"--name", "--", "A", "B", "--dry-run"},
			wantUsageErr: true,
		},
		{
			name:           "flag value after double dash stays positional",
			args:           []string{"a", "--", "--name", "b"},
			wantPositional: []string{"a", "--name", "b"},
		},
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
			if tt.wantUsageErr {
				if !errors.Is(err, errUsage) {
					t.Fatalf("parseInterspersed error = %v, want usage error", err)
				}
				return
			}
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
		{
			name:       "reverse needs a playlist",
			args:       []string{"reverse"},
			wantCode:   exitUsage,
			wantStderr: "reverse needs a playlist: syncer reverse <playlist> or --playlist <playlist> (to reverse the mirror playlist, name it explicitly)",
		},
		{
			name:       "reverse takes one playlist",
			args:       []string{"reverse", "a", "b"},
			wantCode:   exitUsage,
			wantStderr: "exactly one playlist",
		},
		{
			name:       "reverse conflicting playlists",
			args:       []string{"reverse", "a", "--playlist", "b"},
			wantCode:   exitUsage,
			wantStderr: "conflicting playlists",
		},
		{
			name:       "run takes no positional arguments",
			args:       []string{"run", "extra"},
			wantCode:   exitUsage,
			wantStderr: "no positional arguments",
		},
		{
			name:       "run reverse needs a playlist",
			args:       []string{"run", "--reverse"},
			wantCode:   exitUsage,
			wantStderr: "run --reverse needs a playlist",
		},
		{
			name:       "run validates mode before reverse",
			args:       []string{"run", "--mode", "bogus", "--reverse", "--playlist", "x"},
			wantCode:   exitUsage,
			wantStderr: `invalid mode "bogus"`,
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

func TestMergeSources(t *testing.T) {
	tests := []struct {
		name       string
		positional []string
		flagged    []string
		want       []string
	}{
		{name: "positional only", positional: []string{"a", "b"}, want: []string{"a", "b"}},
		{name: "flagged only", flagged: []string{"a", "b"}, want: []string{"a", "b"}},
		{
			name:       "combined, positional first",
			positional: []string{"a", "b"},
			flagged:    []string{"c", "d"},
			want:       []string{"a", "b", "c", "d"},
		},
		{name: "none", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeSources(tt.positional, tt.flagged); !slices.Equal(got, tt.want) {
				t.Errorf("mergeSources = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReversePlaylist(t *testing.T) {
	tests := []struct {
		name       string
		flagged    string
		positional []string
		want       string
		wantErr    bool
	}{
		{name: "positional", positional: []string{"a"}, want: "a"},
		{name: "flag", flagged: "a", want: "a"},
		{name: "both agree", flagged: "a", positional: []string{"a"}, want: "a"},
		{name: "both differ", flagged: "a", positional: []string{"b"}, wantErr: true},
		{name: "two positionals", positional: []string{"a", "b"}, wantErr: true},
		{name: "neither", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := reversePlaylist(tt.flagged, tt.positional)
			if tt.wantErr {
				if !errors.Is(err, errUsage) {
					t.Fatalf("reversePlaylist error = %v, want usage error", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("reversePlaylist = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}
