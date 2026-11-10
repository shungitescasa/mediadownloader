package dl

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	queueFile         = "queue.json"
	queueTTL          = time.Hour
	maxURLsPerRequest = 10
	maxQueueSize      = 200
	maxURLLength      = 2048
	StatusPending     = "pending"
	StatusDownloading = "downloading"
	StatusDone        = "done"
	StatusFailed      = "failed"
	TypeVideo         = "video"
	TypeAudio         = "audio"
	TypeMeta          = "meta"
)

type QueueTask struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	File      string    `json:"file,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Response struct {
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
	Data       any    `json:"data"`
}

var queueMu sync.Mutex

func validType(t string) bool {
	return t == TypeVideo || t == TypeAudio || t == TypeMeta
}

func hashTask(kind, rawURL string) string {
	sum := sha256.Sum256([]byte(kind + "\n" + rawURL))
	return hex.EncodeToString(sum[:])
}

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

	kind := TypeVideo
	if v := form.Value["type"]; len(v) > 0 && v[0] != "" {
		kind = v[0]
	}
	if !validType(kind) {
		return ctx.Status(http.StatusBadRequest).JSON(Response{
			StatusCode: http.StatusBadRequest,
			Message:    "invalid type",
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

	existing := make(map[string]int, len(tasks))
	for i, t := range tasks {
		existing[t.ID] = i
	}

	now := time.Now()
	result := make([]QueueTask, 0, len(urls))
	seen := make(map[string]struct{}, len(urls))
	var fresh []QueueTask
	var retry []string

	for _, u := range urls {
		id := hashTask(kind, u)
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}

		if i, ok := existing[id]; ok {
			if tasks[i].Status != StatusFailed {
				result = append(result, tasks[i])
				continue
			}
			retry = append(retry, id)
		}

		task := QueueTask{
			ID:        id,
			URL:       u,
			Type:      kind,
			Status:    StatusPending,
			CreatedAt: now,
			ExpiresAt: now.Add(queueTTL),
		}
		fresh = append(fresh, task)
		result = append(result, task)
	}

	if len(tasks)-len(retry)+len(fresh) > maxQueueSize {
		return ctx.Status(http.StatusTooManyRequests).JSON(Response{
			StatusCode: http.StatusTooManyRequests,
			Message:    "queue is full",
		})
	}

	if len(fresh) > 0 {
		drop := make(map[string]struct{}, len(retry))
		for _, id := range retry {
			drop[id] = struct{}{}
		}
		kept := tasks[:0]
		for _, t := range tasks {
			if _, ok := drop[t.ID]; !ok {
				kept = append(kept, t)
			}
		}

		if err := saveQueue(append(kept, fresh...)); err != nil {
			return ctx.Status(http.StatusInternalServerError).JSON(Response{
				StatusCode: http.StatusInternalServerError,
				Message:    "failed to save queue",
			})
		}
	}

	return ctx.Status(http.StatusAccepted).JSON(Response{
		StatusCode: http.StatusAccepted,
		Message:    "queued",
		Data:       result,
	})
}
