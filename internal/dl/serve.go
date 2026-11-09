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

func findTask(id string) (QueueTask, bool, error) {
	queueMu.Lock()
	defer queueMu.Unlock()

	tasks, err := loadQueue()
	if err != nil {
		return QueueTask{}, false, err
	}
	for _, t := range tasks {
		if t.ID == id {
			return t, true, nil
		}
	}
	return QueueTask{}, false, nil
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

	if task.File == "" || task.File != filepath.Base(task.File) {
		return reply(ctx, http.StatusNotFound, "not found")
	}

	root, err := filepath.Abs(downloadsDir)
	if err != nil {
		return reply(ctx, http.StatusInternalServerError, "failed to open file")
	}

	f, err := os.Open(filepath.Join(root, task.File))
	if err != nil {
		return reply(ctx, http.StatusNotFound, "not found")
	}

	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return reply(ctx, http.StatusNotFound, "not found")
	}

	ctx.Set("Content-Type", "application/octet-stream")
	ctx.Set("Content-Disposition", "attachment; filename="+strconv.Quote(task.File))
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
	tasks, err := loadQueue()
	if err != nil {
		queueMu.Unlock()
		return reply(ctx, http.StatusInternalServerError, "failed to read queue")
	}

	kept := make([]QueueTask, 0, len(tasks))
	found := false
	for _, t := range tasks {
		if t.ID == id {
			found = true
			continue
		}
		kept = append(kept, t)
	}
	if !found {
		queueMu.Unlock()
		return reply(ctx, http.StatusNotFound, "not found")
	}

	if err := saveQueue(kept); err != nil {
		queueMu.Unlock()
		return reply(ctx, http.StatusInternalServerError, "failed to save queue")
	}
	queueMu.Unlock()

	cancelRunning(id)

	return reply(ctx, http.StatusOK, "stopped")
}
