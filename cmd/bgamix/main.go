// Command bgamix runs BGAMix shows from the command line:
//
//	bgamix version
//	bgamix validate <show.bgamix>
//	bgamix run      <show.bgamix> [-t seconds]
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/biggaeroza/bga-mix/internal/engine"
	"github.com/biggaeroza/bga-mix/internal/project"
	"github.com/biggaeroza/bga-mix/internal/version"
)

const usage = `usage:
  bgamix version
  bgamix validate <show.bgamix>                  check a show file
  bgamix run      <show.bgamix> [-t seconds]     run a show headless until Ctrl-C (or -t)
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
	fs.Parse(args[1:])
	p, err := project.Load(path)
	if err != nil {
		return err
	}
	s, err := engine.Start(p, filepath.Dir(path))
	if err != nil {
		return err
	}
	for _, r := range s.Recordings {
		fmt.Println("recording to", r)
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
