// Package web embeds the compiled single-page UI (web/dist) into the server.
package web

import "embed"

// Dist holds the Vite build output. Run `npm run build` in web/ first;
// scripts/build.sh does this.
//
//go:embed all:dist
var Dist embed.FS
