package tgc

import (
	"embed"
	"io/fs"

	"github.com/govlog/ttyloom/internal/i18n"
)

//go:embed i18n/*.toml
var files embed.FS

// Catalog : the texts of the module, in the languages of the client.
var Catalog, _ = fs.Sub(files, "i18n")

// The catalogue joins the texts of the client when the package is linked:
// its own tests need it as much as the client does.
func init() { i18n.Register(Catalog) }
