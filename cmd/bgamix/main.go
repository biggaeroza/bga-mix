// Command bgamix runs BGAMix shows from the command line:
//
//	bgamix version
//	bgamix validate <show.bgamix>
//	bgamix run      <show.bgamix> [-t seconds] [-listen addr] [-token t] [-previews=false]
package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/biggaeroza/bga-mix/internal/engine"
	"github.com/biggaeroza/bga-mix/internal/project"
	"github.com/biggaeroza/bga-mix/internal/server"
	"github.com/biggaeroza/bga-mix/internal/version"
)

const usage = `usage:
  bgamix version
  bgamix validate <show.bgamix>                  check a show file
  bgamix run      <show.bgamix> [flags]          run a show headless until Ctrl-C, controlled by its API
      -t seconds       stop after this long
      -listen addr     the control API (default 127.0.0.1:8740; "none" for none); see docs/api.md
      -token t         a token the API asks for (needed when it listens beyond this computer)
      -previews=false  no JPEG previews
`

func fail(err error) {
	fmt.Fprintln(os.Stderr, "bgamix:", err)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "-version", "--version":
		fmt.Println(version.Program())
	case "validate":
		if len(os.Args) < 3 {
			fail(fmt.Errorf("validate needs a show file"))
		}
		p, err := project.Load(os.Args[2])
		if err != nil {
			fail(err)
		}
		fmt.Printf("%s: valid: %d feed(s), %d layout(s), %d recording(s)\n", os.Args[2], len(p.Feeds), len(p.Layouts), len(p.Recordings))
	case "run":
		if err := run(os.Args[2:]); err != nil {
			fail(err)
		}
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("run needs a show file")
	}
	path := args[0]
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	dur := fs.Float64("t", 0, "stop after this many seconds (0: run until Ctrl-C)")
	listen := fs.String("listen", "127.0.0.1:8740", `the control API's address ("none": no API)`)
	token := fs.String("token", os.Getenv("BGAMIX_TOKEN"), "a token the API asks for (default $BGAMIX_TOKEN)")
	previews := fs.Bool("previews", true, "keep JPEG previews for the API")
	fs.Parse(args[1:])
	p, err := project.Load(path)
	if err != nil {
		return err
	}
	var ln net.Listener
	if *listen != "none" {
		host, _, err := net.SplitHostPort(*listen)
		if err != nil {
			return fmt.Errorf("-listen: %v", err)
		}
		if ip := net.ParseIP(host); (ip == nil || !ip.IsLoopback()) && host != "localhost" && *token == "" {
			return errors.New("-listen beyond this computer needs a -token (or $BGAMIX_TOKEN)")
		}
		if ln, err = net.Listen("tcp", *listen); err != nil {
			return fmt.Errorf("the control API: %v", err)
		}
	}
	s, err := engine.Start(p, filepath.Dir(path), engine.Options{Previews: *previews && ln != nil})
	if err != nil {
		return err
	}
	if ln != nil {
		go http.Serve(ln, server.New(s, *token))
		fmt.Printf("control API on http://%s/api/status\n", ln.Addr())
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	var timer <-chan time.Time
	if *dur > 0 {
		timer = time.After(time.Duration(*dur * float64(time.Second)))
	}
	start := time.Now()
	for {
		select {
		case e := <-s.Events():
			who := "mixer"
			if e.Feed != "" {
				who = "feed " + e.Feed
			} else if e.Recording != "" {
				who = e.Recording
			}
			fmt.Printf("%6.1f s  %-14s %s %s\n", time.Since(start).Seconds(), who, e.Kind, e.Text)
			if e.Fatal {
				s.Stop()
				return fmt.Errorf("the mixer failed: %s", e.Text)
			}
		case <-stop:
			fmt.Println("stopping…")
			s.Stop()
			return nil
		case <-timer:
			s.Stop()
			fmt.Printf("stopped after %.1f s\n", time.Since(start).Seconds())
			return nil
		}
	}
}
