package version

import (
	"runtime/debug"
	"strings"
)

var (
	Version   = ""
	BuildTime = ""
)

func init() {
	if Version != "" && BuildTime != "" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	var rev, vcsTime string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			vcsTime = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if Version == "" && rev != "" {
		if len(rev) > 7 {
			rev = rev[:7]
		}
		if dirty {
			rev += "-dirty"
		}
		Version = rev
	}
	if BuildTime == "" {
		BuildTime = vcsTime
	}
}

func String() string {
	v, t := Version, BuildTime
	if v == "" {
		v = "dev"
	}
	if t == "" {
		return v
	}
	return v + " (" + t + ")"
}

func CommitURL() string {
	v := strings.TrimSuffix(Version, "-dirty")
	if v == "" {
		return ""
	}
	return "https://github.com/shungitescasa/mediadownloader/commit/" + v
}
