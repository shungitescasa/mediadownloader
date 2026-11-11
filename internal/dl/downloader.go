package dl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	probeTimeout     = 60 * time.Second
	downloadTimeout  = 30 * time.Minute
	maxProbeOutput   = 4 << 20
	maxStderrCapture = 8 << 10
	workerInterval   = 5 * time.Second
	cookiesCopyName  = "cookies.txt"
)

var (
	errPlaylist = errors.New("playlists are not supported")
	errLive     = errors.New("live streams are not supported")
	errTooLarge = errors.New("media is too large")
	errNoOutput = errors.New("download produced no file")
)

var slots chan struct{}

var (
	runningMu sync.Mutex
	running   = map[string]context.CancelFunc{}
)

type claimedTask struct {
	task QueueTask
	ctx  context.Context
}

func cancelRunning(id string) {
	runningMu.Lock()
	cancel := running[id]
	runningMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

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
		"--extractor-args", "youtube:player_client=default,-web_embedded",
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.buf.String()))
	}
	return nil
}

func probe(ctx context.Context, workDir string, base []string, rawURL string) ([]byte, error) {
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
		return nil, err
	}

	var info probeInfo
	if err := json.Unmarshal(out.buf.Bytes(), &info); err != nil {
		return nil, err
	}

	switch {
	case info.Type == "playlist" || info.Type == "multi_video" || info.Type == "multi_video_list":
		return nil, errPlaylist
	case info.IsLive:
		return nil, errLive
	}
	return out.buf.Bytes(), nil
}

func downloadArgs(task QueueTask) []string {
	if task.Type == TypeAudio {
		return []string{"-f", "ba/b", "-x", "--audio-format", "mp3"}
	}
	return []string{"--merge-output-format", "mp4", "-f", cfg.FormatSelector}
}

func DownloadTask(ctx context.Context, task QueueTask) (string, error) {
	root, err := filepath.Abs(cfg.DownloadsDir)
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

	info, err := probe(ctx, workDir, base, task.URL)
	if err != nil {
		return "", err
	}

	if task.Type == TypeMeta {
		if err := os.WriteFile(filepath.Join(workDir, task.ID+".json"), info, 0o600); err != nil {
			return "", err
		}
	} else {
		dlCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
		defer cancel()

		args := append(append([]string{}, base...),
			"--max-filesize", fmt.Sprint(cfg.MaxFileSize),
			"--playlist-items", "1",
			"--download-sections", fmt.Sprintf("*0-%d", cfg.MaxMediaSecs),
			"--restrict-filenames",
		)
		args = append(args, downloadArgs(task)...)
		args = append(args,
			"-o", filepath.Join(workDir, task.ID+".%(ext)s"),
			"--",
			task.URL,
		)
		if err := runYtdlp(dlCtx, workDir, io.Discard, args); err != nil {
			return "", err
		}
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
		if st.Size() > cfg.MaxFileSize {
			return "", errTooLarge
		}
		produced = m
		break
	}
	if produced == "" {
		return "", errNoOutput
	}

	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(produced)), ".")
	if !isSafeExt(ext) {
		return "", errNoOutput
	}
	if err := os.Rename(produced, filepath.Join(root, task.ID+"."+ext)); err != nil {
		return "", err
	}
	return ext, nil
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

func isSafeExt(ext string) bool {
	if ext == "" || len(ext) > 16 {
		return false
	}
	for _, r := range ext {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func mimeFor(ext string) string {
	if m := mime.TypeByExtension("." + ext); m != "" {
		return m
	}
	return "application/octet-stream"
}

func updateTask(id string, status TaskStatus, description, ext, mimeType string) {
	queueMu.Lock()
	defer queueMu.Unlock()

	_, err := db.Exec(
		"UPDATE tasks SET status = ?, description = ?, extension = ?, mime_type = ? WHERE id = ?",
		status, description, ext, mimeType, id,
	)
	if err != nil {
		log.Println("queue save failed:", err)
	}
}

func runTask(ctx context.Context, task QueueTask) {
	defer func() { <-slots }()
	defer func() {
		runningMu.Lock()
		delete(running, task.ID)
		runningMu.Unlock()
	}()

	ext, err := DownloadTask(ctx, task)
	if err != nil {
		log.Printf("task %s failed: %v", task.ID, err)
		if ctx.Err() != nil {
			return
		}
		updateTask(task.ID, StatusFailed, safeError(err), "", "")
		return
	}

	updateTask(task.ID, StatusDone, "", ext, mimeFor(ext))
}

func cleanupFiles(tasks []QueueTask) {
	entries, err := os.ReadDir(cfg.DownloadsDir)
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
		if err := os.RemoveAll(filepath.Join(cfg.DownloadsDir, e.Name())); err != nil {
			log.Println("cleanup failed:", err)
		}
	}
}

func claimPending(parent context.Context) []claimedTask {
	queueMu.Lock()
	defer queueMu.Unlock()

	tx, err := db.Begin()
	if err != nil {
		log.Println("queue read failed:", err)
		return nil
	}
	defer tx.Rollback()

	if err := purgeExpired(tx); err != nil {
		log.Println("queue purge failed:", err)
		return nil
	}
	tasks, err := listTasks(tx)
	if err != nil {
		log.Println("queue read failed:", err)
		return nil
	}

	cleanupFiles(tasks)

	var claimed []claimedTask
	release := func() {
		for _, c := range claimed {
			<-slots
			cancelRunning(c.task.ID)
			runningMu.Lock()
			delete(running, c.task.ID)
			runningMu.Unlock()
		}
	}

	for _, t := range tasks {
		if t.Status != StatusPending {
			continue
		}
		select {
		case slots <- struct{}{}:
			if _, err := tx.Exec("UPDATE tasks SET status = ? WHERE id = ?", StatusDownloading, t.ID); err != nil {
				<-slots
				log.Println("queue save failed:", err)
				release()
				return nil
			}
			t.Status = StatusDownloading
			ctx, cancel := context.WithCancel(parent)
			runningMu.Lock()
			running[t.ID] = cancel
			runningMu.Unlock()
			claimed = append(claimed, claimedTask{task: t, ctx: ctx})
		default:
		}
	}

	if err := tx.Commit(); err != nil {
		log.Println("queue save failed:", err)
		release()
		return nil
	}
	return claimed
}

func resetInterrupted() {
	queueMu.Lock()
	defer queueMu.Unlock()

	_, err := db.Exec("UPDATE tasks SET status = ? WHERE status = ?", StatusPending, StatusDownloading)
	if err != nil {
		log.Println("queue reset failed:", err)
	}
}

func ProcessQueue(ctx context.Context) {
	for _, c := range claimPending(ctx) {
		go runTask(c.ctx, c.task)
	}
}

func StartWorker(ctx context.Context) {
	slots = make(chan struct{}, cfg.MaxParallel)
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
