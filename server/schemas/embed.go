// Package schemas embeds the JSON Schemas for bus events (see MESSAGE_FORMATS.md).
package schemas

import "embed"

//go:embed *.json
var FS embed.FS
