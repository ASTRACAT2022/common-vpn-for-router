package webui

import "embed"

//go:embed *.html *.css *.js *.png
var files embed.FS

func Files() embed.FS { return files }
