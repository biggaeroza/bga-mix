//go:build desktop

// Command bgamix-gui is the desktop shell: the same web UI and API as `bgamix serve`, in a
// native window by Wails v2 (WebKitGTK on Linux). No network listener: the webview's asset
// server calls the API handler directly, so no token is needed. Open, save and media use native
// file dialogs. Closing the window with a show running asks first, and stops it cleanly.
//
// Build (Linux needs libgtk-3-dev and libwebkit2gtk-4.1-dev):
//
//	go build -tags desktop,production,webkit2_41 -o bgamix-gui ./cmd/bgamix-gui
//
// Usage: bgamix-gui [show.bgamix]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/biggaeroza/bga-mix/internal/app"
	"github.com/biggaeroza/bga-mix/internal/server"
	"github.com/biggaeroza/bga-mix/internal/version"
	"github.com/biggaeroza/bga-mix/web"
)

func filters(kind string) []runtime.FileFilter {
	switch kind {
	case "show":
		return []runtime.FileFilter{{DisplayName: "BGAMix shows (*.bgamix)", Pattern: "*.bgamix"}}
	case "image":
		return []runtime.FileFilter{{DisplayName: "Images", Pattern: "*.png;*.jpg;*.jpeg;*.bmp;*.gif;*.webp;*.tif;*.tiff"}}
	case "media":
		return []runtime.FileFilter{
			{DisplayName: "Video files and images", Pattern: "*.mp4;*.mkv;*.mov;*.webm;*.avi;*.ts;*.m2ts;*.mts;*.mpg;*.mpeg;*.m4v;*.flv;*.ogv;*.wmv;*.mxf;*.png;*.jpg;*.jpeg;*.bmp;*.gif;*.webp;*.tif;*.tiff"},
			{DisplayName: "All files", Pattern: "*"},
		}
	}
	return nil // programs: any file
}

type dialogs struct{ ctx context.Context }

func (d *dialogs) OpenFile(kind string) (string, error) {
	return runtime.OpenFileDialog(d.ctx, runtime.OpenDialogOptions{Title: "Choose " + kind, Filters: filters(kind)})
}

func (d *dialogs) SaveFile(kind, suggested string) (string, error) {
	return runtime.SaveFileDialog(d.ctx, runtime.SaveDialogOptions{Title: "Save the " + kind, DefaultFilename: suggested, Filters: filters(kind)})
}

func (d *dialogs) OpenFolder(title string) (string, error) {
	return runtime.OpenDirectoryDialog(d.ctx, runtime.OpenDialogOptions{Title: title})
}

func main() {
	ver := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *ver {
		fmt.Println(version.Program())
		return
	}
	if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: bgamix-gui [show.bgamix]")
		os.Exit(2)
	}
	st := app.New()
	d := &dialogs{}
	cfg := server.Config{Studio: st, Dialogs: d}
	if flag.NArg() == 1 {
		if _, err := st.Open(flag.Arg(0)); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		cfg.InitialPath = flag.Arg(0)
	}
	err := wails.Run(&options.App{
		Title:       "BGAMix — live video mixer",
		Width:       1500,
		Height:      950,
		MinWidth:    900,
		MinHeight:   600,
		AssetServer: &assetserver.Options{Assets: web.Assets, Handler: server.NewServer(cfg)},
		OnStartup:   func(ctx context.Context) { d.ctx = ctx },
		OnBeforeClose: func(ctx context.Context) bool {
			if st.Show() == nil {
				return false
			}
			r, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{Type: runtime.QuestionDialog, Title: "The show is running",
				Message: "Stop the show (finishing its recordings) and quit?", Buttons: []string{"Stop and quit", "Cancel"}, DefaultButton: "Cancel", CancelButton: "Cancel"})
			return err != nil || r != "Stop and quit" && r != "Yes"
		},
		OnShutdown: func(ctx context.Context) { st.Stop() },
	})
	st.Stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
