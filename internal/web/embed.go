// Package web embeds the dashboard's static files.
package web

import "embed"

//go:embed static
var FS embed.FS
