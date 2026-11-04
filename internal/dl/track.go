package dl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	trackInterval  = 2 * time.Second
	trackHeartbeat = 15 * time.Second
)

type TrackEvent struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Status string `json:"status"`
}

func toTrackEvent(t QueueTask) TrackEvent {
	return TrackEvent{ID: t.ID, URL: t.URL, Status: t.Status}
}

func writeEvent(w *bufio.Writer, ev TrackEvent) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
		return err
	}
	return w.Flush()
}

func isTerminal(status string) bool {
	return status == StatusDone || status == StatusFailed
}

func Track(ctx fiber.Ctx) error {
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

	ctx.Set("Content-Type", "text/event-stream")
	ctx.Set("Cache-Control", "no-cache, no-transform")
	ctx.Set("Connection", "keep-alive")
	ctx.Set("X-Accel-Buffering", "no")

	return ctx.SendStreamWriter(func(w *bufio.Writer) {
		last := toTrackEvent(task)
		if err := writeEvent(w, last); err != nil || isTerminal(last.Status) {
			return
		}

		poll := time.NewTicker(trackInterval)
		defer poll.Stop()
		beat := time.NewTicker(trackHeartbeat)
		defer beat.Stop()

		for {
			select {
			case <-poll.C:
				current, found, err := findTask(id)
				if err != nil || !found {
					return
				}

				ev := toTrackEvent(current)
				if ev == last {
					continue
				}
				last = ev
				if err := writeEvent(w, ev); err != nil || isTerminal(ev.Status) {
					return
				}
			case <-beat.C:
				if _, err := w.WriteString(": ping\n\n"); err != nil {
					return
				}
				if err := w.Flush(); err != nil {
					return
				}
			}
		}
	})
}
