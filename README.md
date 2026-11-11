# ![mediadownloader](/icon.webp)

A small self-hosted web service for downloading media from links. It is written in Go and uses `yt-dlp` under the hood.

## Prerequisites

- Go
- yt-dlp
- ffmpeg

## Dependencies

- `fiber` for the web server.
- `modernc.org/sqlite` for the download history.

## Installation Guide

### 1. Clone the Git repository

```bash
git clone https://github.com/shungitescasa/mediadownloader.git
cd mediadownloader
```

### 2. Build the project

```bash
go build -o mediadownloader ./cmd
```

### 3. Create the configuration file

The configuration file is in INI format. If `mediadownloader.ini` does not exist, a default one is created on the first launch. You can also point to another file with `-config PATH`.

<details>
<summary>Full configuration file</summary>

```ini
[server]
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
```

</details>

### 4. Launch

```bash
./mediadownloader
```

The service listens on the address from `server.listen` (`:3056` by default). The download history is stored in `history.db` in the working directory.

## License

This project is under the [GNU Affero General Public License v3.0 with FORSEN modifier](LICENSE).
