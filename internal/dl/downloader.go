package dl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	downloadsDir     = "downloads"
	maxConcurrent    = 2
	maxFileSize      = 2 << 30
	maxMediaSeconds  = 600
	probeTimeout     = 60 * time.Second
	downloadTimeout  = 30 * time.Minute
	maxProbeOutput   = 4 << 20
	maxStderrCapture = 8 << 10
	workerInterval   = 5 * time.Second
	formatSelector   = "bv*+ba/b"
	cookiesCopyName  = "cookies.txt"
)

var (
	errPlaylist = errors.New("playlists are not supported")
	errLive     = errors.New("live streams are not supported")
	errTooLarge = errors.New("media is too large")
	errNoOutput = errors.New("download produced no file")
)

var slots = make(chan struct{}, maxConcurrent)

type probeInfo struct {
	Type   string `json:"_type"`
	IsLive bool   `json:"is_live"`
}

type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if remaining := l.max - l.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			l.buf.Write(p[:remaining])
		} else {
			l.buf.Write(p)
		}
	}
	return len(p), nil
}

func cookieArgs(workDir string) ([]string, error) {
	switch cfg.CookiesMode {
	case CookiesFile:
		raw, err := os.ReadFile(cfg.CookiesFile)
		if err != nil {
			return nil, err
		}
		copyPath := filepath.Join(workDir, cookiesCopyName)
		if err := os.WriteFile(copyPath, raw, 0o600); err != nil {
			return nil, err
		}
		return []string{"--cookies", copyPath}, nil
	case CookiesBrowser:
		return []string{"--cookies-from-browser", cfg.CookiesBrowser}, nil
	}
	return nil, nil
}

func baseArgs(workDir string) ([]string, error) {
	cookies, err := cookieArgs(workDir)
	if err != nil {
		return nil, err
	}

	args := []string{
		"--ignore-config",
		"--no-plugin-dirs",
		"--no-exec",
		"--no-playlist",
		"--no-cache-dir",
		"--no-write-info-json",
		"--no-write-thumbnail",
		"--no-write-subs",
		"--no-write-comments",
		"--no-mtime",
		"--no-colors",
		"--no-warnings",
		"--socket-timeout", "30",
		"--retries", "5",
		"--fragment-retries", "5",
		"--extractor-retries", "3",
		"--paths", "temp:" + workDir,
	}
	return append(args, cookies...), nil
}

func ytdlpEnv(workDir string) []string {
	if cfg.CookiesMode == CookiesBrowser {
		return os.Environ()
	}
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + workDir,
		"LANG=C.UTF-8",
	}
}

func runYtdlp(ctx context.Context, workDir string, stdout io.Writer, args []string) error {
	cmd := exec.CommandContext(ctx, cfg.Binary, args...)
	cmd.Dir = workDir
	cmd.Env = ytdlpEnv(workDir)
	cmd.Stdin = nil
	cmd.Stdout = stdout
	stderr := &limitedBuffer{max: maxStderrCapture}
	cmd.Stderr = stderr
	cmd.WaitDelay = 5 * time.Second

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.buf.String()))
	}
	return nil
}

func probe(ctx context.Context, workDir string, base []string, rawURL string) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	out := &limitedBuffer{max: maxProbeOutput}
	args := append(append([]string{}, base...),
		"--skip-download",
		"--dump-single-json",
		"--",
		rawURL,
	)
	if err := runYtdlp(ctx, workDir, out, args); err != nil {
		return err
	}

	var info probeInfo
	if err := json.Unmarshal(out.buf.Bytes(), &info); err != nil {
		return err
	}

	switch {
	case info.Type == "playlist" || info.Type == "multi_video" || info.Type == "multi_video_list":
		return errPlaylist
	case info.IsLive:
		return errLive
	}
	return nil
}

func DownloadTask(ctx context.Context, task QueueTask) (string, error) {
	root, err := filepath.Abs(downloadsDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", err
	}

	workDir, err := os.MkdirTemp(root, ".tmp-"+task.ID+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(workDir)

	base, err := baseArgs(workDir)
	if err != nil {
		return "", err
	}

	if err := probe(ctx, workDir, base, task.URL); err != nil {
		return "", err
	}

	dlCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	args := append(append([]string{}, base...),
		"--max-filesize", fmt.Sprint(maxFileSize),
		"--playlist-items", "1",
		"--download-sections", fmt.Sprintf("*0-%d", maxMediaSeconds),
		"--restrict-filenames",
		"--merge-output-format", "mp4",
		"-f", formatSelector,
		"-o", filepath.Join(workDir, task.ID+".%(ext)s"),
		"--",
		task.URL,
	)
	if err := runYtdlp(dlCtx, workDir, io.Discard, args); err != nil {
		return "", err
	}

	matches, err := filepath.Glob(filepath.Join(workDir, task.ID+".*"))
	if err != nil {
		return "", err
	}

	var produced string
	for _, m := range matches {
		ext := strings.ToLower(filepath.Ext(m))
		if ext == ".part" || ext == ".ytdl" || ext == ".temp" {
			continue
		}
		st, err := os.Lstat(m)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		if st.Size() > maxFileSize {
			return "", errTooLarge
		}
		produced = m
		break
	}
	if produced == "" {
		return "", errNoOutput
	}

	name := task.ID + strings.ToLower(filepath.Ext(produced))
	if err := os.Rename(produced, filepath.Join(root, name)); err != nil {
		return "", err
	}
	return name, nil
}

func safeError(err error) string {
	for _, known := range []error{errPlaylist, errLive, errTooLarge, errNoOutput} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "download timed out"
	}
	return "download failed"
}

func updateTask(id string, fn func(*QueueTask)) {
	queueMu.Lock()
	defer queueMu.Unlock()

	tasks, err := loadQueue()
	if err != nil {
		log.Println("queue read failed:", err)
		return
	}
	for i := range tasks {
		if tasks[i].ID == id {
			fn(&tasks[i])
			break
		}
	}
	if err := saveQueue(tasks); err != nil {
		log.Println("queue save failed:", err)
	}
}

func runTask(ctx context.Context, task QueueTask) {
	defer func() { <-slots }()

	name, err := DownloadTask(ctx, task)
	if err != nil {
		log.Printf("task %s failed: %v", task.ID, err)
		if ctx.Err() != nil {
			return
		}
		updateTask(task.ID, func(t *QueueTask) {
			t.Status = StatusFailed
			t.Error = safeError(err)
		})
		return
	}

	updateTask(task.ID, func(t *QueueTask) {
		t.Status = StatusDone
		t.File = name
		t.Error = ""
	})
}

func cleanupFiles(tasks []QueueTask) {
	entries, err := os.ReadDir(downloadsDir)
	if err != nil {
		return
	}

	live := make(map[string]struct{}, len(tasks))
	for _, t := range tasks {
		live[t.ID] = struct{}{}
	}

	for _, e := range entries {
		id := strings.TrimPrefix(e.Name(), ".tmp-")
		if len(id) >= 64 {
			id = id[:64]
		}
		if _, ok := live[id]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(downloadsDir, e.Name())); err != nil {
			log.Println("cleanup failed:", err)
		}
	}
}

func claimPending() []QueueTask {
	queueMu.Lock()
	defer queueMu.Unlock()

	tasks, err := loadQueue()
	if err != nil {
		log.Println("queue read failed:", err)
		return nil
	}

	cleanupFiles(tasks)

	var claimed []QueueTask
	for i := range tasks {
		if tasks[i].Status != StatusPending {
			continue
		}
		select {
		case slots <- struct{}{}:
			tasks[i].Status = StatusDownloading
			claimed = append(claimed, tasks[i])
		default:
		}
	}

	if len(claimed) > 0 {
		if err := saveQueue(tasks); err != nil {
			log.Println("queue save failed:", err)
			for range claimed {
				<-slots
			}
			return nil
		}
	}
	return claimed
}

func resetInterrupted() {
	queueMu.Lock()
	defer queueMu.Unlock()

	tasks, err := loadQueue()
	if err != nil {
		return
	}
	changed := false
	for i := range tasks {
		if tasks[i].Status == StatusDownloading {
			tasks[i].Status = StatusPending
			changed = true
		}
	}
	if changed {
		_ = saveQueue(tasks)
	}
}

func ProcessQueue(ctx context.Context) {
	for _, task := range claimPending() {
		go runTask(ctx, task)
	}
}

func StartWorker(ctx context.Context) {
	resetInterrupted()

	go func() {
		ticker := time.NewTicker(workerInterval)
		defer ticker.Stop()
		for {
			ProcessQueue(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
