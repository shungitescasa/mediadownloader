package dl

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	CookiesNone    = "none"
	CookiesFile    = "file"
	CookiesBrowser = "browser"
)

const defaultConfig = `[server]
; address to listen on, host:port (empty host means all interfaces)
listen = :3056

[instance]
; name shown in the page title
name = mediadownloader

[maintainer]
; shown in the footer; the link is optional
name =
link =

[downloads]
; maximum number of downloads running at the same time
max_parallel = 4
; maximum size of a downloaded file: bytes or a number with K, M or G suffix
max_filesize = 2G
; only the first N seconds of the media are downloaded
max_media_seconds = 600
; directory where finished files are stored
directory = downloads
; yt-dlp format selector for video downloads
format = bv*+ba/b

[queue]
; how long a task is kept (e.g. 30m, 1h, 90m)
ttl = 1h
max_urls_per_request = 10
max_size = 200
max_url_length = 2048

[yt-dlp]
binary = yt-dlp

[cookies]
; none, file or browser
mode = none
; used when mode = file: path to a Netscape-format cookies file
file =
; used when mode = browser: BROWSER[+KEYRING][:PROFILE][::CONTAINER], e.g. firefox or chrome:Default
browser =
`

var supportedBrowsers = map[string]struct{}{
	"brave": {}, "chrome": {}, "chromium": {}, "edge": {}, "firefox": {},
	"opera": {}, "safari": {}, "vivaldi": {}, "whale": {},
}

type Config struct {
	MaxParallel    int
	MaxFileSize    int64
	MaxMediaSecs   int
	DownloadsDir   string
	FormatSelector string
	QueueTTL       time.Duration
	MaxURLsPerReq  int
	MaxQueueSize   int
	MaxURLLength   int
	Listen         string
	DatabasePath   string
	InstanceName   string
	MaintainerName string
	MaintainerLink string
	YtdlpVersion   string
	Binary         string
	CookiesMode    string
	CookiesFile    string
	CookiesBrowser string
}

var cfg = defaultSettings()

func defaultSettings() Config {
	return Config{
		MaxParallel:    4,
		MaxFileSize:    2 << 30,
		MaxMediaSecs:   600,
		DownloadsDir:   "downloads",
		FormatSelector: "bv*+ba/b",
		QueueTTL:       time.Hour,
		MaxURLsPerReq:  10,
		MaxQueueSize:   200,
		MaxURLLength:   2048,
		Listen:         ":3056",
		DatabasePath:   "history.db",
		InstanceName:   "mediadownloader",
		Binary:         "yt-dlp",
		CookiesMode:    CookiesNone,
	}
}

func Settings() Config {
	return cfg
}

func positiveInt(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return 0, errors.New("must be a positive integer")
	}
	return n, nil
}

func parseSize(value string) (int64, error) {
	mult := int64(1)
	if value != "" {
		switch value[len(value)-1] {
		case 'k', 'K':
			mult = 1 << 10
		case 'm', 'M':
			mult = 1 << 20
		case 'g', 'G':
			mult = 1 << 30
		}
		if mult > 1 {
			value = value[:len(value)-1]
		}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || n < 1 || n > (1<<62)/mult {
		return 0, errors.New("must be a positive size, e.g. 500M or 2G")
	}
	return n * mult, nil
}

func parseConfig(path string) (Config, error) {
	c := defaultSettings()

	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()

	section := ""
	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == ';' || line[0] == '#' {
			continue
		}
		if line[0] == '[' && line[len(line)-1] == ']' {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return c, fmt.Errorf("%s:%d: expected key = value", path, n)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		switch section + "." + key {
		case "server.listen":
			if value != "" {
				c.Listen = value
			}
		case "database.path":
			if value != "" {
				c.DatabasePath = value
			}
		case "downloads.max_parallel":
			c.MaxParallel, err = positiveInt(value)
		case "downloads.max_filesize":
			c.MaxFileSize, err = parseSize(value)
		case "downloads.max_media_seconds":
			c.MaxMediaSecs, err = positiveInt(value)
		case "downloads.directory":
			if value != "" {
				c.DownloadsDir = value
			}
		case "downloads.format":
			if value != "" {
				c.FormatSelector = value
			}
		case "queue.ttl":
			c.QueueTTL, err = time.ParseDuration(value)
			if err == nil && c.QueueTTL <= 0 {
				err = errors.New("must be positive")
			}
		case "queue.max_urls_per_request":
			c.MaxURLsPerReq, err = positiveInt(value)
		case "queue.max_size":
			c.MaxQueueSize, err = positiveInt(value)
		case "queue.max_url_length":
			c.MaxURLLength, err = positiveInt(value)
		case "instance.name":
			if value != "" {
				c.InstanceName = value
			}
		case "maintainer.name":
			c.MaintainerName = value
		case "maintainer.link":
			c.MaintainerLink = value
		case "yt-dlp.binary":
			if value != "" {
				c.Binary = value
			}
		case "cookies.mode":
			c.CookiesMode = strings.ToLower(value)
		case "cookies.file":
			c.CookiesFile = value
		case "cookies.browser":
			c.CookiesBrowser = value
		default:
			return c, fmt.Errorf("%s:%d: unknown option %q in section %q", path, n, key, section)
		}
		if err != nil {
			return c, fmt.Errorf("%s:%d: %s.%s: %w", path, n, section, key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return c, err
	}

	return c, c.validate()
}

func (c Config) validate() error {
	if c.MaintainerLink != "" {
		u, err := url.Parse(c.MaintainerLink)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto") || (u.Scheme != "mailto" && u.Host == "") {
			return errors.New("maintainer.link must be an http(s) or mailto link")
		}
	}
	switch c.CookiesMode {
	case CookiesNone:
	case CookiesFile:
		if c.CookiesFile == "" {
			return errors.New("cookies.file is required when cookies.mode is file")
		}
		st, err := os.Stat(c.CookiesFile)
		if err != nil {
			return fmt.Errorf("cookies.file: %w", err)
		}
		if !st.Mode().IsRegular() {
			return errors.New("cookies.file is not a regular file")
		}
	case CookiesBrowser:
		if c.CookiesBrowser == "" {
			return errors.New("cookies.browser is required when cookies.mode is browser")
		}
		name := c.CookiesBrowser
		if i := strings.IndexAny(name, "+:"); i >= 0 {
			name = name[:i]
		}
		if _, ok := supportedBrowsers[strings.ToLower(name)]; !ok {
			return fmt.Errorf("cookies.browser: unsupported browser %q", name)
		}
	default:
		return fmt.Errorf("cookies.mode must be none, file or browser, got %q", c.CookiesMode)
	}
	return nil
}

func Init(path string) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte(defaultConfig), 0o600); err != nil {
			panic(fmt.Errorf("creating %s: %w", path, err))
		}
	}

	c, err := parseConfig(path)
	if err != nil {
		panic(fmt.Errorf("invalid config: %w", err))
	}

	bin, err := exec.LookPath(c.Binary)
	if err != nil {
		panic(fmt.Errorf("yt-dlp is not available: %w", err))
	}
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		panic(fmt.Errorf("yt-dlp failed to run: %w: %s", err, strings.TrimSpace(string(out))))
	}

	c.Binary = bin
	c.YtdlpVersion = strings.TrimSpace(string(out))
	cfg = c
}
