package web

import (
	"embed"
	"html/template"
	"io/fs"
	"strings"

	"tools/mediadownloader/internal/dl"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/static"
)

//go:embed templates static
var files embed.FS

var templates = template.Must(template.ParseFS(files, "templates/*.html"))

func render(ctx fiber.Ctx, name, path, pageTitle, description string) error {
	ctx.Set("Content-Type", fiber.MIMETextHTMLCharsetUTF8)
	c := dl.Settings()
	maintainer := c.MaintainerName
	if maintainer == "" {
		maintainer = c.MaintainerLink
	}
	if pageTitle == "" {
		pageTitle = c.InstanceName
	} else {
		pageTitle += " - " + c.InstanceName
	}
	return templates.ExecuteTemplate(ctx.Response().BodyWriter(), name, map[string]any{
		"Title":          c.InstanceName,
		"PageTitle":      pageTitle,
		"Description":    strings.ReplaceAll(description, "{{title}}", c.InstanceName),
		"Path":           path,
		"Origin":         ctx.Scheme() + "://" + ctx.Host(),
		"Maintainer":     maintainer,
		"MaintainerLink": c.MaintainerLink,
		"YtdlpVersion":   c.YtdlpVersion,
	})
}

func Index(ctx fiber.Ctx) error {
	return render(ctx, "index.html", "/", "media downloader", "download any type of media directly to your computer")
}

func Developers(ctx fiber.Ctx) error {
	return render(ctx, "developers.html", "/developers", "for developers", "how to send API requests to {{title}}")
}

func Static() fiber.Handler {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	return static.New("", static.Config{FS: sub})
}
