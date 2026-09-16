package dl

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	CookiesNone    = "none"
	CookiesFile    = "file"
	CookiesBrowser = "browser"
)

const defaultConfig = `[yt-dlp]
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
	Binary         string
	CookiesMode    string
	CookiesFile    string
	CookiesBrowser string
}

var cfg = Config{Binary: "yt-dlp", CookiesMode: CookiesNone}

func parseConfig(path string) (Config, error) {
	c := Config{Binary: "yt-dlp", CookiesMode: CookiesNone}

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
	}
	if err := scanner.Err(); err != nil {
		return c, err
	}

	return c, c.validate()
}

func (c Config) validate() error {
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
	if out, err := exec.Command(bin, "--version").CombinedOutput(); err != nil {
		panic(fmt.Errorf("yt-dlp failed to run: %w: %s", err, strings.TrimSpace(string(out))))
	}

	c.Binary = bin
	cfg = c
}
