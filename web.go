// Package tracereports exposes the embedded web UI assets.
package tracereports

import "embed"

// WebFS holds the frontend (HTML, CSS, JS) compiled into the binary.
//
//go:embed web/*
var WebFS embed.FS
