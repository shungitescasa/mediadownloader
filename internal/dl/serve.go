package dl

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

func isHashID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func reply(ctx fiber.Ctx, status int, message string) error {
	return ctx.Status(status).JSON(Response{StatusCode: status, Message: message})
}

func Download(ctx fiber.Ctx) error {
	id := ctx.Query("id")
	if !isHashID(id) {
		return reply(ctx, http.StatusBadRequest, "invalid id")
	}

	task, found, err := findTask(id)
	if err != nil {
		return reply(ctx, http.StatusInternalServerError, "failed to read queue")
	}
	if !found {
		return reply(ctx, http.StatusNotFound, "not found")
	}

	switch task.Status {
	case StatusDone:
	case StatusFailed:
		return reply(ctx, http.StatusGone, "download failed")
	default:
		return reply(ctx, http.StatusConflict, "media is not ready yet")
	}

	if !isSafeExt(task.Extension) {
		return reply(ctx, http.StatusNotFound, "not found")
	}
	name := task.ID + "." + task.Extension

	root, err := filepath.Abs(downloadsDir)
	if err != nil {
		return reply(ctx, http.StatusInternalServerError, "failed to open file")
	}

	f, err := os.Open(filepath.Join(root, name))
	if err != nil {
		return reply(ctx, http.StatusNotFound, "not found")
	}

	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return reply(ctx, http.StatusNotFound, "not found")
	}

	mimeType := task.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	ctx.Set("Content-Type", mimeType)
	ctx.Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
	ctx.Set("X-Content-Type-Options", "nosniff")
	ctx.Set("Cache-Control", "private, no-store")

	return ctx.SendStream(f, int(st.Size()))
}

func Stop(ctx fiber.Ctx) error {
	id := ctx.Query("id")
	if !isHashID(id) {
		return reply(ctx, http.StatusBadRequest, "invalid id")
	}

	queueMu.Lock()
	res, err := db.Exec("DELETE FROM tasks WHERE id = ?", id)
	queueMu.Unlock()
	if err != nil {
		return reply(ctx, http.StatusInternalServerError, "failed to save queue")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return reply(ctx, http.StatusNotFound, "not found")
	}

	cancelRunning(id)

	return reply(ctx, http.StatusOK, "stopped")
}
