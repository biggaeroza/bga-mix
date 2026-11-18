// Package web embeds the GUI: plain ES modules and CSS, no build step.
package web

import "embed"

//go:embed index.html styles.css *.js
var Assets embed.FS
