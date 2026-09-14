package dl

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

const (
	queueFile = "queue.json"
	queueTTL  = time.Hour
)

type QueueTask struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Response struct {
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
	Data       any    `json:"data"`
}

var queueMu sync.Mutex

func loadQueue() ([]QueueTask, error) {
	raw, err := os.ReadFile(queueFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}

	var tasks []QueueTask
	if err := json.Unmarshal(raw, &tasks); err != nil {
		return nil, err
	}

	now := time.Now()
	alive := tasks[:0]
	for _, t := range tasks {
		if t.ExpiresAt.After(now) {
			alive = append(alive, t)
		}
	}
	return alive, nil
}

func saveQueue(tasks []QueueTask) error {
	raw, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}

	tmp := queueFile + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, queueFile)
}

func collectURLs(values map[string][]string) ([]string, error) {
	var urls []string
	for _, key := range []string{"url", "urls"} {
		for _, v := range values[key] {
			for _, line := range strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				u, err := url.ParseRequestURI(line)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return nil, errors.New("invalid url: " + line)
				}
				urls = append(urls, line)
			}
		}
	}
	if len(urls) == 0 {
		return nil, errors.New("no urls provided")
	}
	return urls, nil
}

func AddToQueue(ctx fiber.Ctx) error {
	form, err := ctx.MultipartForm()
	if err != nil {
		return ctx.Status(http.StatusBadRequest).JSON(Response{
			StatusCode: http.StatusBadRequest,
			Message:    "expected a multipart form",
		})
	}

	urls, err := collectURLs(form.Value)
	if err != nil {
		return ctx.Status(http.StatusBadRequest).JSON(Response{
			StatusCode: http.StatusBadRequest,
			Message:    err.Error(),
		})
	}

	now := time.Now()
	added := make([]QueueTask, 0, len(urls))
	for _, u := range urls {
		added = append(added, QueueTask{
			ID:        uuid.NewString(),
			URL:       u,
			CreatedAt: now,
			ExpiresAt: now.Add(queueTTL),
		})
	}

	queueMu.Lock()
	defer queueMu.Unlock()

	tasks, err := loadQueue()
	if err != nil {
		return ctx.Status(http.StatusInternalServerError).JSON(Response{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to read queue",
		})
	}

	if err := saveQueue(append(tasks, added...)); err != nil {
		return ctx.Status(http.StatusInternalServerError).JSON(Response{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to save queue",
		})
	}

	return ctx.Status(http.StatusAccepted).JSON(Response{
		StatusCode: http.StatusAccepted,
		Message:    "queued",
		Data:       added,
	})
}
