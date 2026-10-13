package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// tlsAgentService is the compose service that applies core's HTTPS requests to
// Caddy. It is the core image's `tls-agent` command, which images from before
// firstTLSRelease do not have.
const tlsAgentService = "tls-agent"

// firstTLSRelease is the first release whose images include the tls-agent
// (the core image's `tls-agent` subcommand, which the compose file runs).
// 0.1.0-rc.1 predates it; a release candidate after it, or 0.1.0, has it.
const firstTLSRelease = "0.1.0-rc.2"

// tlsAgentImage reports whether the images tagged v have the tls-agent. Only a
// tag that names a release from before firstTLSRelease says no (0.1.0-rc.1,
// 0.1.0-beta.1, 0.0.9). Every other tag says yes: a later release, this CLI's
// own, `latest`, an empty tag, a development build of the CLI, and a tag that is
// no release (`ci`, 0.1.0-lab.3): those images are built from the source that
// has it.
func tlsAgentImage(v string) bool {
	if v == imageTag(version.Version) || version.Version == "dev" {
		return true
	}
	rel := strings.TrimPrefix(strings.TrimSpace(v), "v")
	return !releaseTag(rel) || release.Compare(rel, firstTLSRelease) >= 0
}

// releaseTag reports whether v names a release (or a release candidate) as
// the project publishes them: X.Y.Z, or X.Y.Z-alpha|beta|rc... A development
// tag such as 0.1.0-lab.3 is a valid version too, but no release.
func releaseTag(v string) bool {
	if !release.Valid(v) {
		return false
	}
	if !release.Prerelease(v) {
		return true
	}
	_, pre, _ := strings.Cut(strings.SplitN(v, "+", 2)[0], "-")
	kind, _, _ := strings.Cut(pre, ".")
	return kind == "alpha" || kind == "beta" || kind == "rc"
}

// composeDefines reports whether dir's docker-compose.yml defines service. A
// file that is not there says no; one that cannot be read (a user who is not
// root) says yes, as a tag that is not known does: nothing can be said against
// it, and the callers that need a verdict run as root.
func composeDefines(dir, service string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml")) //nolint:gosec // the install's own compose file
	if err != nil {
		return !errors.Is(err, fs.ErrNotExist)
	}
	return composeTextDefines(string(b), service)
}

// composeTextDefines reports whether the compose file text defines service
// under its top-level services key: a key at the indentation of the first one.
// The file is the installer's own (or an older release's), so a scan of its
// lines is enough; it needs neither docker nor a YAML parser.
func composeTextDefines(text, service string) bool {
	inServices, indent := false, -1
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, " \t\r")
		body := strings.TrimLeft(line, " ")
		if body == "" || strings.HasPrefix(body, "#") {
			continue
		}
		n := len(line) - len(body)
		key, _, isKey := strings.Cut(body, ":")
		switch {
		case n == 0:
			inServices, indent = isKey && key == "services", -1
		case !inServices:
		case indent < 0:
			indent = n
			fallthrough
		case n == indent:
			if isKey && strings.Trim(key, `"'`) == service {
				return true
			}
		}
	}
	return false
}

// tlsAgentOn is the one test of whether this install has a working tls-agent:
// the compose file in dir defines the service, and the images it runs have it.
// A rollback to a compose file from before the agent, an install pinned to
// 0.1.0-rc.1, and an install that is not there all say no; everything that
// waits for the agent, lists it, restarts it or runs `ssl` in core asks this.
func tlsAgentOn(dir string, env map[string]string) bool {
	return tlsAgentImage(env["BOBRES_VERSION"]) && composeDefines(dir, tlsAgentService)
}

// predatesHTTPS is the message of the commands that need the HTTPS manager
// when this install does not have it, or "" when it has.
func predatesHTTPS(dir string, env map[string]string) string {
	if tlsAgentOn(dir, env) {
		return ""
	}
	return "this install predates the HTTPS manager (release " + orDash(env["BOBRES_VERSION"]) + "): run `sudo bobres update`"
}
