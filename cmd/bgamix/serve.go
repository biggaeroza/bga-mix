package main

// The browser shell: loopback only, a random session token in the URL, Host and Origin checked.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/biggaeroza/bga-mix/internal/app"
	"github.com/biggaeroza/bga-mix/internal/server"
	"github.com/biggaeroza/bga-mix/web"
)

func cmdServe(args []string, out io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, args, out, nil)
}

// serve runs the browser UI until ctx is done (stopping a running show cleanly); ready, if not
// nil, receives the URL.
func serve(ctx context.Context, args []string, out io.Writer, ready chan<- string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", 8740, "TCP port on 127.0.0.1 (0: any free port; a busy port falls back to any)")
	noBrowser := fs.Bool("no-browser", false, "do not open a browser")
	devWeb := fs.String("dev-web", "", "serve the UI from this directory instead of the embedded copy (development)")
	var paths []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		paths = append(paths, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(paths) > 1 {
		return errors.New("serve: at most one show file")
	}
	st := app.New()
	cfg := server.Config{Studio: st}
	if len(paths) == 1 {
		if _, err := st.Open(paths[0]); err != nil {
			return err
		}
		cfg.InitialPath = paths[0]
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)))
	if err != nil && *port != 0 {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return err
	}
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return err
	}
	addr := ln.Addr().String()
	cfg.Token = hex.EncodeToString(tok)
	cfg.AllowedHosts = []string{addr, "localhost:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)}
	if os.Getenv("BGAMIX_LOG_REQUESTS") != "" {
		cfg.Log = os.Stderr
	}
	var assets iofs.FS = web.Assets
	if *devWeb != "" {
		assets = os.DirFS(*devWeb)
	}
	cfg.Assets = assets
	srv := &http.Server{Handler: server.NewServer(cfg), ReadHeaderTimeout: 10 * time.Second}
	url := "http://" + addr + "/#token=" + cfg.Token
	fmt.Fprintf(out, "BGAMix UI: %s\ncontrol API: http://%s/api/ with the token %s (docs/api.md)\n(Ctrl+C to stop)\n", url, addr, cfg.Token)
	if ready != nil {
		ready <- url
	}
	if !*noBrowser {
		openBrowser(url)
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		if st.Show() != nil {
			fmt.Fprintln(out, "stopping the show…")
		}
		st.Stop()
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return srv.Shutdown(c)
	case err := <-errc:
		st.Stop()
		return err
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
