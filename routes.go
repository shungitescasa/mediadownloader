package dl

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/wader/goutubedl"
)

func StartDownload(ctx fiber.Ctx) error {
	fmt.Println(ctx.Queries())
	url := ctx.Query("url")
	if len(strings.TrimSpace(url)) == 0 {
		return fiber.ErrBadRequest
	}

	opts := goutubedl.Options{
		Type: goutubedl.TypeSingle,
	}

	result, err := goutubedl.New(ctx.Context(), url, opts)
	if err != nil {
		return err
	}

	dr, err := result.Download(ctx.Context(), "bv*+ba/b")
	if err != nil {
		return err
	}
	defer dr.Close()

	// sanitizing filename
	ext := result.Info.Ext
	title := strings.TrimSpace(result.Info.Title)
	if title == "" {
		title = "video"
	} else {
		re := regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
		title = re.ReplaceAllString(title, "_")
		title = strings.Trim(title, " .")
		if len(title) > 180 {
			title = title[:180]
		}
		if ext == "" {
			ext = "mp4"
		}
	}
	filename := title + "." + ext

	ctx.Set("Content-Type", "application/octet-stream")
	ctx.Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	return ctx.SendStream(dr)
}
