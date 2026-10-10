// Package webui provides the compiled-in web dashboard assets for the
// telemetry hub. Placing this package at the module root allows the
// //go:embed directive to reference the sibling web/ directory without
// violating Go's restriction on ".." in embed paths.
//
// Usage in cmd/hub/main.go:
//
//	import "github.com/telemetry-agent/webui"
//	sub, _ := fs.Sub(webui.FS, "web")
//	http.FileServer(http.FS(sub))
package webui

import "embed"

// FS is the embedded filesystem containing the full web/ directory tree.
// Strip the leading "web" prefix before passing to http.FileServer.
//
//go:embed web
var FS embed.FS
