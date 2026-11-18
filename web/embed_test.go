package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// Every module the UI imports, and the page's stylesheet and script, are embedded.
func TestAssets(t *testing.T) {
	imp := regexp.MustCompile(`from "\./([\w-]+\.js)"`)
	page, err := fs.ReadFile(Assets, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"styles.css", "app.js"} {
		if !strings.Contains(string(page), want) {
			t.Errorf("index.html lacks %s", want)
		}
		if _, err := fs.Stat(Assets, want); err != nil {
			t.Errorf("%s not embedded", want)
		}
	}
	files, _ := fs.Glob(Assets, "*.js")
	for _, f := range files {
		b, _ := fs.ReadFile(Assets, f)
		for _, m := range imp.FindAllStringSubmatch(string(b), -1) {
			if _, err := fs.Stat(Assets, m[1]); err != nil {
				t.Errorf("%s imports %s, which is not embedded", f, m[1])
			}
		}
	}
}
