package web

import (
	"embed"
	"html/template"
	"io/fs"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/static"
)

//go:embed templates static
var files embed.FS

var templates = template.Must(template.ParseFS(files, "templates/*.html"))

func Index(ctx fiber.Ctx) error {
	ctx.Set("Content-Type", fiber.MIMETextHTMLCharsetUTF8)
	return templates.ExecuteTemplate(ctx.Response().BodyWriter(), "index.html", map[string]any{
		"Title": "mediadownloader",
	})
}

func Static() fiber.Handler {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	return static.New("", static.Config{FS: sub})
}
