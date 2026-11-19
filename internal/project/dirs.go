package project

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// ExpandHome turns a leading ~ or ~/ into the user's home folder.
func ExpandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}

// VideosDir is the user's videos folder: XDG_VIDEOS_DIR from ~/.config/user-dirs.dirs (the
// folder the desktop calls Videos, in the user's language), else ~/Videos.
func VideosDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	if f, err := os.Open(filepath.Join(cfg, "user-dirs.dirs")); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			v, ok := strings.CutPrefix(line, "XDG_VIDEOS_DIR=")
			if !ok {
				continue
			}
			v = strings.Trim(v, `"`)
			v = strings.Replace(v, "$HOME", home, 1)
			if filepath.IsAbs(v) && filepath.Clean(v) != filepath.Clean(home) {
				return v
			}
		}
	}
	return filepath.Join(home, "Videos")
}

// UnsavedRecordingsDir is where an unsaved show's relative recording paths go: BGAMix in the
// user's videos folder.
func UnsavedRecordingsDir() string { return filepath.Join(VideosDir(), "BGAMix") }
