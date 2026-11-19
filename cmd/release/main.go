// Command release builds a BGAMix release for this platform:
//
//	go run ./cmd/release -version 1.0.0
//
// It runs the tests, builds bgamix and bgamix-gui stamped with the version, commit and date,
// and writes dist/<version>/:
//
//	bgamix-<version>-<os>-<arch>.tar.gz   the programs, docs, examples, licences, desktop entry
//	bgamix-<version>-src.tar.gz           the source at the commit (git archive)
//	bin/<os>-<arch>/                       the programs, unpacked
//	SHA256SUMS
//
// The working tree must be clean (-dirty to allow it). Other platforms are built on them: BGAMix
// links GStreamer through cgo, so it does not cross-compile.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "release: "+f+"\n", a...)
	os.Exit(1)
}

func run(env []string, name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		fail("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

func main() {
	ver := flag.String("version", "", "the version, e.g. 1.0.0")
	dirty := flag.Bool("dirty", false, "allow uncommitted changes")
	skipTests := flag.Bool("skip-tests", false, "do not run the tests first")
	noGUI := flag.Bool("no-gui", false, "do not build bgamix-gui (no GTK/WebKit development files here)")
	flag.Parse()
	if !regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`).MatchString(*ver) {
		fail("give -version as major.minor.patch")
	}
	if st := run(nil, "git", "status", "--porcelain"); st != "" && !*dirty {
		fail("the working tree has changes (commit them, or -dirty):\n%s", st)
	}
	commit := run(nil, "git", "rev-parse", "--short", "HEAD")
	date := time.Now().UTC().Format("2006-01-02")
	if !*skipTests {
		fmt.Println("testing …")
		run(nil, "go", "test", "./...")
	}
	plat := runtime.GOOS + "-" + runtime.GOARCH
	dist := filepath.Join("dist", *ver)
	name := "bgamix-" + *ver + "-" + plat
	stage := filepath.Join(dist, name)
	os.RemoveAll(stage)
	binDir := filepath.Join(dist, "bin", plat)
	for _, d := range []string{filepath.Join(stage, "bin"), binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			fail("%v", err)
		}
	}
	pkg := "github.com/biggaeroza/bga-mix/internal/version"
	ld := fmt.Sprintf("-s -w -X %s.Version=%s -X %s.Commit=%s -X %s.Date=%s", pkg, *ver, pkg, commit, pkg, date)
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	builds := [][]string{{"bgamix", "./cmd/bgamix", ""}}
	if !*noGUI {
		tags := "desktop,production"
		if runtime.GOOS == "linux" {
			tags += ",webkit2_41"
		}
		builds = append(builds, []string{"bgamix-gui", "./cmd/bgamix-gui", tags})
	}
	for _, b := range builds {
		fmt.Println("building", b[0], "…")
		out := filepath.Join(stage, "bin", b[0]+exe)
		args := []string{"build", "-trimpath", "-ldflags", ld, "-o", out}
		if b[2] != "" {
			args = append(args, "-tags", b[2])
		}
		run([]string{"CGO_ENABLED=1"}, "go", append(args, b[1])...)
		copyFile(out, filepath.Join(binDir, b[0]+exe), 0o755)
	}
	// docs, examples, licences, packaging
	for _, f := range []string{"README.md", "LICENSE", "THIRD-PARTY.md"} {
		copyFile(f, filepath.Join(stage, f), 0o644)
	}
	copyTree("docs", filepath.Join(stage, "docs"), nil)
	copyTree("examples", filepath.Join(stage, "examples"), func(p string) bool { return !strings.Contains(p, "recordings") })
	copyTree("packaging", filepath.Join(stage, "packaging"), func(p string) bool { return filepath.Base(p) != "install.sh" })
	copyFile(filepath.Join("packaging", "install.sh"), filepath.Join(stage, "install.sh"), 0o755)
	os.WriteFile(filepath.Join(stage, "VERSION"), []byte(fmt.Sprintf("BGAMix %s (%s, %s), %s\n", *ver, commit, date, plat)), 0o644)

	archive := filepath.Join(dist, name+".tar.gz")
	tarGz(stage, name, archive)
	os.RemoveAll(stage)
	src := filepath.Join(dist, "bgamix-"+*ver+"-src.tar.gz")
	run(nil, "git", "archive", "--format=tar.gz", "--prefix=bgamix-"+*ver+"/", "-o", src, "HEAD")
	sums(dist)
	fmt.Printf("done: %s\n", dist)
}

func copyFile(from, to string, mode fs.FileMode) {
	b, err := os.ReadFile(from)
	if err != nil {
		fail("%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		fail("%v", err)
	}
	if err := os.WriteFile(to, b, mode); err != nil {
		fail("%v", err)
	}
}

func copyTree(from, to string, keep func(string) bool) {
	filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || (keep != nil && !keep(p)) {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		copyFile(p, filepath.Join(to, rel), 0o644)
		return nil
	})
}

func tarGz(dir, prefix, out string) {
	f, err := os.Create(out)
	if err != nil {
		fail("%v", err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	var files []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return err
	})
	sort.Strings(files)
	for _, p := range files {
		fi, _ := os.Stat(p)
		rel, _ := filepath.Rel(dir, p)
		hdr := &tar.Header{Name: prefix + "/" + filepath.ToSlash(rel), Mode: int64(fi.Mode().Perm()), Size: fi.Size(), ModTime: fi.ModTime()}
		if err := tw.WriteHeader(hdr); err != nil {
			fail("%v", err)
		}
		in, _ := os.Open(p)
		io.Copy(tw, in)
		in.Close()
	}
	if err := tw.Close(); err != nil {
		fail("%v", err)
	}
	if err := gz.Close(); err != nil {
		fail("%v", err)
	}
}

// sums writes SHA256SUMS of the archives and the unpacked programs.
func sums(dist string) {
	var lines []string
	filepath.WalkDir(dist, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(p) == "SHA256SUMS" {
			return err
		}
		b, _ := os.ReadFile(p)
		h := sha256.Sum256(b)
		rel, _ := filepath.Rel(dist, p)
		lines = append(lines, hex.EncodeToString(h[:])+"  "+filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(lines)
	os.WriteFile(filepath.Join(dist, "SHA256SUMS"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}
