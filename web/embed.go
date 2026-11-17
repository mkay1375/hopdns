// Package webassets contains the browser panel shipped in the native executable.
package webassets

import "embed"

// Files contains the panel's runtime assets.
//
//go:embed index.html app.js theme.js style.css
var Files embed.FS
