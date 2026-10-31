// Package ui embeds the HTML templates and static assets.
package ui

import "embed"

// FS holds templates/ and static/. In -dev mode the server reads the same
// directories from disk instead, so edits show up without a rebuild.
//
//go:embed templates static
var FS embed.FS
