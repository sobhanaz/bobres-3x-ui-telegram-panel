package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A master key rotation (`bobres rotate-key`) keeps the keys it replaced in .env as comments, so that a database
// copy or a backup older than the rotation can still be opened by hand. A comment is not an environment variable:
// Compose and the services never read these lines. But writeEnv renders .env from a map, which has no comments, so
// without help the next `bobres config set` would drop them. These functions recognise the lines and carry them
// over every rewrite until the owner deletes them or runs `rotate-key --forget-old`.

// retiredIntro starts the explanation line of a block of retired keys.
const retiredIntro = "# Retired by bobres rotate-key"

// retiredKeyRe matches one retired key: the master key a service had before a rotation.
var retiredKeyRe = regexp.MustCompile(`^#\s*(BOBRES_(?:CORE|PAYMENTS|PROVISIONER)_MASTER_KEY_PREVIOUS)=(\S+)$`)

// retiredKeyLines are the lines of an env file that keep retired keys: the explanation lines and the commented keys,
// in the order they are in.
func retiredKeyLines(env []byte) []string {
	var out []string
	for _, line := range strings.Split(string(env), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, retiredIntro) || retiredKeyRe.MatchString(line) {
			out = append(out, line)
		}
	}
	return out
}

// appendRetiredKeys puts the retired-key lines at the end of a rendered env file.
func appendRetiredKeys(rendered []byte, lines []string) []byte {
	if len(lines) == 0 {
		return rendered
	}
	return append(rendered, []byte("\n"+strings.Join(lines, "\n")+"\n")...)
}

// retiredKeyBlock is what one rotation adds: an explanation, then each replaced key as a comment, by env name.
func retiredKeyBlock(at time.Time, old map[string]string) []string {
	names := make([]string, 0, len(old))
	for n := range old {
		names = append(names, n)
	}
	sort.Strings(names)
	lines := []string{fmt.Sprintf("%s on %s: the master keys it replaced. Nothing reads the lines below. They are as secret as the keys in use; "+
		"delete them when no backup or database copy older than this is needed (or run: sudo bobres rotate-key --forget-old).",
		retiredIntro, at.UTC().Format("2006-01-02T15:04:05Z"))}
	for _, n := range names {
		lines = append(lines, "# "+n+"="+old[n])
	}
	return lines
}

// retiredKeys reads the retired keys out of an env file, by env name, the newest last when a service has several.
func retiredKeys(env []byte) map[string][]string {
	out := map[string][]string{}
	for _, line := range retiredKeyLines(env) {
		if m := retiredKeyRe.FindStringSubmatch(line); m != nil {
			out[m[1]] = append(out[m[1]], m[2])
		}
	}
	return out
}
