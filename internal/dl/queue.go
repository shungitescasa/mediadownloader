package dl

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	queueTTL          = time.Hour
	maxURLsPerRequest = 10
	maxQueueSize      = 200
	maxURLLength      = 2048
)

type Response struct {
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
	Data       any    `json:"data"`
}

var queueMu sync.Mutex

func hashTask(kind TaskType, rawURL string) string {
	sum := sha256.Sum256([]byte(kind.String() + "\n" + rawURL))
	return hex.EncodeToString(sum[:])
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
				if len(line) > maxURLLength {
					return nil, errors.New("url is too long")
				}
				u, err := url.ParseRequestURI(line)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
					return nil, errors.New("invalid url: " + line)
				}
				urls = append(urls, line)
			}
		}
	}
	if len(urls) == 0 {
		return nil, errors.New("no urls provided")
	}
	if len(urls) > maxURLsPerRequest {
		return nil, errors.New("too many urls in one request")
	}
	return urls, nil
}

func AddToQueue(ctx fiber.Ctx) error {
	form, err := ctx.MultipartForm()
	if err != nil {
		return reply(ctx, http.StatusBadRequest, "expected a multipart form")
	}

	urls, err := collectURLs(form.Value)
	if err != nil {
		return reply(ctx, http.StatusBadRequest, err.Error())
	}

	kind := TypeVideo
	if v := form.Value["type"]; len(v) > 0 && v[0] != "" {
		parsed, ok := parseType(v[0])
		if !ok {
			return reply(ctx, http.StatusBadRequest, "invalid type")
		}
		kind = parsed
	}

	queueMu.Lock()
	defer queueMu.Unlock()

	tx, err := db.Begin()
	if err != nil {
		return reply(ctx, http.StatusInternalServerError, "failed to read queue")
	}
	defer tx.Rollback()

	if err := purgeExpired(tx); err != nil {
		return reply(ctx, http.StatusInternalServerError, "failed to read queue")
	}

	created := time.Unix(time.Now().Unix(), 0)
	result := make([]QueueTask, 0, len(urls))
	seen := make(map[string]struct{}, len(urls))
	var fresh []QueueTask
	retries := 0

	for _, u := range urls {
		id := hashTask(kind, u)
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}

		existing, ok, err := getTask(tx, id)
		if err != nil {
			return reply(ctx, http.StatusInternalServerError, "failed to read queue")
		}
		if ok {
			if existing.Status != StatusFailed {
				result = append(result, existing)
				continue
			}
			retries++
		}

		task := QueueTask{
			ID:        id,
			URL:       u,
			Type:      kind,
			Status:    StatusPending,
			CreatedAt: created,
			ExpiresAt: created.Add(queueTTL),
		}
		fresh = append(fresh, task)
		result = append(result, task)
	}

	var total int
	if err := tx.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&total); err != nil {
		return reply(ctx, http.StatusInternalServerError, "failed to read queue")
	}
	if total-retries+len(fresh) > maxQueueSize {
		return reply(ctx, http.StatusTooManyRequests, "queue is full")
	}

	for _, t := range fresh {
		if _, err := tx.Exec("DELETE FROM tasks WHERE id = ?", t.ID); err != nil {
			return reply(ctx, http.StatusInternalServerError, "failed to save queue")
		}
		if _, err := tx.Exec(
			"INSERT INTO tasks (id, url, type, status, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
			t.ID, t.URL, t.Type, t.Status, t.CreatedAt.Unix(), t.ExpiresAt.Unix(),
		); err != nil {
			return reply(ctx, http.StatusInternalServerError, "failed to save queue")
		}
	}
	if err := tx.Commit(); err != nil {
		return reply(ctx, http.StatusInternalServerError, "failed to save queue")
	}

	return ctx.Status(http.StatusAccepted).JSON(Response{
		StatusCode: http.StatusAccepted,
		Message:    "queued",
		Data:       result,
	})
}
