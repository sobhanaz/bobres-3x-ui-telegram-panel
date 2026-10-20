package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// This file is the Docker side of the uninstall: it finds what the BOBRES project owns (by its
// compose project label and its names) and removes only that. Another container, volume,
// network or image is never touched: an image is removed with `docker rmi` and no -f, so one that
// another container uses stays.

const (
	// projectLabel is what docker compose puts on every object of a project.
	projectLabel = "com.docker.compose.project=" + composeProject
	// workdirLabel names the directory a container's compose file was in.
	workdirLabel = "com.docker.compose.project.working_dir"
	// projectPrefix is how compose names a project's volumes and networks.
	projectPrefix = composeProject + "_"
)

var projectVolumeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type dockerCtr struct{ ID, Name, Image, WorkDir string }

type dockerVol struct {
	Name, Mountpoint string
	Size             sizeInfo
	SizeKnown        bool
}

type dockerNet struct{ ID, Name string }

type dockerImg struct {
	Ref, ID string
	// Size is what docker prints for it (25.7MB); Why says why the image is BOBRES's.
	Size, Why string
}

// dockerInventory is what the BOBRES project owns in Docker.
type dockerInventory struct {
	Present, Reachable bool
	Containers         []dockerCtr
	Volumes            []dockerVol
	Networks           []dockerNet
	Images             []dockerImg
	// Skipped says what carries BOBRES's label or name but was left alone, and why.
	Skipped []string
}

// docker runs a docker command that needs no compose file.
func (u *uninstaller) docker(ctx context.Context, args ...string) ([]byte, error) {
	return u.ops.run(ctx, "/", args...)
}

// imageRepoPrefix is where BOBRES's own images are.
const imageRepoPrefix = "ghcr.io/sobhanaz/bobres-"

// normRef cuts a digest off an image reference: caddy:2.11-alpine@sha256:... is caddy:2.11-alpine.
func normRef(ref string) string {
	ref, _, _ = strings.Cut(strings.TrimSpace(ref), "@")
	return ref
}

// refKeys are the names an image reference answers to in `docker images`: repo:tag, and repo@digest
// (Docker 29's image store lists an image that was pulled by digest with no tag at all).
func refKeys(ref string) []string {
	name, digest, hasDigest := strings.Cut(strings.TrimSpace(ref), "@")
	repo, tag := name, ""
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		repo, tag = name[:i], name[i+1:]
	}
	var keys []string
	if repo == "" {
		return nil
	}
	if tag != "" {
		keys = append(keys, repo+":"+tag)
	}
	if hasDigest && digest != "" {
		keys = append(keys, repo+"@"+digest)
	}
	return keys
}

// shortID is the first twelve hex digits of an image id (sha256:abc... or abc...).
func shortID(id string) string {
	id = strings.TrimPrefix(strings.TrimSpace(id), "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	return id
}

// dockerInventory finds the project's containers, volumes, networks and images. Nothing is changed.
func (u *uninstaller) dockerInventory(ctx context.Context, dir *dirState) dockerInventory {
	var inv dockerInventory
	inv.Present = u.h.has("docker")
	if !inv.Present {
		return inv
	}
	if _, err := u.docker(ctx, "info", "--format", "{{.ServerVersion}}"); err != nil {
		return inv
	}
	inv.Reachable = true

	// Containers.
	out, _ := u.docker(ctx, "ps", "-a", "--filter", "label="+projectLabel,
		"--format", `{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Label "`+workdirLabel+`"}}`)
	var ctrIDs []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) < 3 || !projectVolumeRe.MatchString(strings.TrimSpace(f[0])) {
			continue
		}
		c := dockerCtr{ID: strings.TrimSpace(f[0]), Name: strings.TrimSpace(f[1]), Image: strings.TrimSpace(f[2])}
		if len(f) > 3 {
			c.WorkDir = strings.TrimSpace(f[3])
		}
		inv.Containers = append(inv.Containers, c)
		ctrIDs = append(ctrIDs, c.ID)
	}
	// The images the project's containers run are BOBRES's (unless the receipt says they were on the
	// server before). The ids are kept for the later looks: once the containers are gone nothing else
	// says which image they ran.
	if len(ctrIDs) > 0 {
		if out, err := u.docker(ctx, append([]string{"inspect", "--format", "{{.Image}}"}, ctrIDs...)...); err == nil {
			if u.imageIDs == nil {
				u.imageIDs = map[string]bool{}
			}
			for _, l := range strings.Split(string(out), "\n") {
				if id := shortID(l); id != "" {
					u.imageIDs[id] = true
				}
			}
		}
	}

	// Volumes: the label is the proof, and the name must carry the project's prefix too.
	out, _ = u.docker(ctx, "volume", "ls", "--quiet", "--filter", "label="+projectLabel)
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case !projectVolumeRe.MatchString(line):
		case !strings.HasPrefix(line, projectPrefix):
			inv.Skipped = append(inv.Skipped, "volume "+line+": it carries the label of the bobres project but not its name prefix")
		default:
			names = append(names, line)
		}
	}
	mounts := map[string]string{}
	if len(names) > 0 {
		args := append([]string{"volume", "inspect", "--format", `{{.Name}}|{{.Mountpoint}}`}, names...)
		if out, err := u.docker(ctx, args...); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if f := strings.Split(strings.TrimRight(line, "\r"), "|"); len(f) == 2 {
					mounts[strings.TrimSpace(f[0])] = strings.TrimSpace(f[1])
				}
			}
		}
	}
	for _, n := range names {
		v := dockerVol{Name: n, Mountpoint: mounts[n]}
		if mp := mounts[n]; mp != "" && strings.HasPrefix(mp, "/var/lib/docker/volumes/") {
			if fi, err := os.Stat(u.h.p(mp)); err == nil && fi.IsDir() {
				v.Size, v.SizeKnown = treeSize(u.h.p(mp)), true
			}
		}
		inv.Volumes = append(inv.Volumes, v)
	}

	// Networks.
	out, _ = u.docker(ctx, "network", "ls", "--filter", "label="+projectLabel, "--format", `{{.ID}}\t{{.Name}}`)
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) != 2 || !projectVolumeRe.MatchString(strings.TrimSpace(f[0])) {
			continue
		}
		if name := strings.TrimSpace(f[1]); strings.HasPrefix(name, projectPrefix) {
			inv.Networks = append(inv.Networks, dockerNet{ID: strings.TrimSpace(f[0]), Name: name})
		} else {
			inv.Skipped = append(inv.Skipped, "network "+name+": it carries the label of the bobres project but not its name prefix")
		}
	}

	// Images: BOBRES's own by their repository; the others only when the project's containers ran
	// them or the receipt says an install pulled them, and never one that was on the server before
	// (the receipt). A reference is matched by repo:tag and by repo@digest.
	preKeys := map[string]bool{}
	owned := map[string]string{}
	if u.rcpt.Images != nil {
		for _, r := range u.rcpt.Images.Preexisting {
			for _, k := range refKeys(r) {
				preKeys[k] = true
			}
		}
		for _, r := range u.rcpt.Images.Refs {
			for _, k := range refKeys(r) {
				owned[k] = "pulled by a BOBRES install (the receipt)"
			}
		}
	}
	out, _ = u.docker(ctx, "images", "--digests", "--format", `{{.Repository}}\t{{.Tag}}\t{{.Digest}}\t{{.ID}}\t{{.Size}}`)
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) < 5 {
			continue
		}
		repo, tag, digest, id, size := strings.TrimSpace(f[0]), strings.TrimSpace(f[1]), strings.TrimSpace(f[2]), strings.TrimSpace(f[3]), strings.TrimSpace(f[4])
		if repo == "" || repo == "<none>" {
			continue
		}
		hasTag, hasDigest := tag != "" && tag != "<none>", digest != "" && digest != "<none>"
		var keys []string
		ref := ""
		if hasTag {
			keys, ref = append(keys, repo+":"+tag), repo+":"+tag
		}
		if hasDigest {
			keys = append(keys, repo+"@"+digest)
			if ref == "" {
				ref = repo + "@" + digest
			}
		}
		if ref == "" {
			ref = id
		}
		why, pre := "", false
		for _, k := range keys {
			pre = pre || preKeys[k]
			if owned[k] != "" && why == "" {
				why = owned[k]
			}
		}
		switch {
		case strings.HasPrefix(repo, imageRepoPrefix):
			why = "BOBRES's own image"
		case pre:
			continue
		case u.imageIDs[shortID(id)]:
			why = "run by a BOBRES container"
		case why == "":
			continue
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		inv.Images = append(inv.Images, dockerImg{Ref: ref, ID: id, Size: size, Why: why})
	}
	return inv
}

// humanBytes parses what docker prints for a size (25.7MB, 1.2GB, 812kB) into bytes.
func humanBytes(s string) int64 {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		mult   float64
	}{{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3}, {"B", 1}}
	for _, u := range units {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil {
				return int64(f * u.mult)
			}
		}
	}
	return 0
}

// foreignWorkDir returns a project container whose compose file was in another directory than the
// install being removed ("" when all agree): the stack on this server then belongs to a different
// install, and removing it would not be what the operator asked.
func foreignWorkDir(containers []dockerCtr, dir *dirState) (string, string) {
	for _, c := range containers {
		w := filepath.Clean(c.WorkDir)
		if c.WorkDir == "" || w == filepath.Clean(dir.Given) || w == filepath.Clean(dir.Real) {
			continue
		}
		return c.Name, c.WorkDir
	}
	return "", ""
}

// removeDocker removes the project's objects. dir is the install (compose is asked to bring the
// stack down only when its files are there).
func (u *uninstaller) removeDocker(ctx context.Context, dir *dirState) (kept []string, err error) {
	inv := u.dockerInventory(ctx, dir)
	if !inv.Present {
		u.say(markSkip, "Docker is not installed: no containers, volumes, networks or images to remove")
		return nil, nil
	}
	if !inv.Reachable {
		if u.h.has("systemctl") {
			_ = u.h.do(ctx, "systemctl", "start", "docker")
		}
		if inv = u.dockerInventory(ctx, dir); !inv.Reachable {
			return nil, errors.New("the Docker daemon does not answer, so the containers and volumes of BOBRES cannot be removed: start it (systemctl start docker) and run this again")
		}
	}
	if name, w := foreignWorkDir(inv.Containers, dir); name != "" {
		return nil, fmt.Errorf("the container %s belongs to a compose project in %s, not in %s: nothing was removed", name, w, dir.Real)
	}
	if dir.Install {
		// Every profile is on, so that a container of a service whose profile is off now (the
		// tls-agent of a release the install went back from) goes too.
		if out, err := u.ops.run(ctx, dir.Real, composeArgs("--profile", "*", "down", "--remove-orphans", "--volumes")...); err != nil {
			return nil, fmt.Errorf("docker compose down failed: %w: %s", err, lastLines(string(out), 3))
		}
		u.say(markOK, "docker compose down: the containers and the networks and volumes the file declares are gone")
	}
	// What down left: a container created outside it, a volume or network an older compose file
	// does not declare (tls_request, tls_result and caddy_admin after a rollback).
	inv = u.dockerInventory(ctx, dir)
	if name, w := foreignWorkDir(inv.Containers, dir); name != "" {
		return nil, fmt.Errorf("the container %s belongs to a compose project in %s, not in %s: nothing more was removed", name, w, dir.Real)
	}
	if len(inv.Containers) > 0 {
		args := []string{"rm", "-f"}
		for _, c := range inv.Containers {
			args = append(args, c.ID)
		}
		if out, err := u.docker(ctx, args...); err != nil {
			return nil, fmt.Errorf("docker rm failed: %w: %s", err, lastLines(string(out), 3))
		}
		u.say(markOK, "Removed %d container(s) that docker compose down left", len(inv.Containers))
	}
	if len(inv.Volumes) > 0 {
		var names []string
		for _, v := range inv.Volumes {
			names = append(names, v.Name)
		}
		if out, err := u.docker(ctx, append([]string{"volume", "rm"}, names...)...); err != nil {
			return nil, fmt.Errorf("docker volume rm %s failed: %w: %s", strings.Join(names, " "), err, lastLines(string(out), 3))
		}
		u.say(markOK, "Also removed volumes of this install that docker compose down left (this docker-compose.yml may be from an older release): %s", strings.Join(names, ", "))
	}
	if len(inv.Networks) > 0 {
		args := []string{"network", "rm"}
		for _, n := range inv.Networks {
			args = append(args, n.ID)
		}
		if out, err := u.docker(ctx, args...); err != nil {
			return nil, fmt.Errorf("docker network rm failed: %w: %s", err, lastLines(string(out), 3))
		}
		u.say(markOK, "Removed %d network(s) that docker compose down left", len(inv.Networks))
	}
	// The images last, one by one: an image another container uses stays.
	inv = u.dockerInventory(ctx, dir)
	removed := 0
	for _, im := range inv.Images {
		if out, err := u.docker(ctx, "rmi", im.Ref); err != nil {
			kept = append(kept, fmt.Sprintf("image %s: %s", im.Ref, lastLines(string(out), 1)))
			u.say(markWarn, "Image %s was not removed (%s)", im.Ref, lastLines(string(out), 1))
			continue
		}
		removed++
	}
	if removed > 0 {
		u.say(markOK, "Removed %d image(s) BOBRES pulled", removed)
	}
	return kept, nil
}

// dockerInUse lists why Docker cannot be removed: what is left on it that is not BOBRES's.
func (u *uninstaller) dockerInUse(ctx context.Context) []string {
	var why []string
	count := func(label string, args ...string) {
		out, err := u.docker(ctx, args...)
		if err != nil {
			why = append(why, "cannot list the "+label+": "+lastLines(string(out), 1))
			return
		}
		if n := len(nonEmptyLines(string(out))); n > 0 {
			why = append(why, fmt.Sprintf("%d %s", n, label))
		}
	}
	count("container(s)", "ps", "-aq")
	count("image(s)", "images", "-q")
	count("volume(s)", "volume", "ls", "-q")
	count("plugin(s)", "plugin", "ls", "-q")
	out, err := u.docker(ctx, "network", "ls", "--format", "{{.Name}}")
	if err != nil {
		why = append(why, "cannot list the networks")
	}
	extra := 0
	for _, n := range nonEmptyLines(string(out)) {
		if n != "bridge" && n != "host" && n != "none" {
			extra++
		}
	}
	if extra > 0 {
		why = append(why, fmt.Sprintf("%d network(s) besides Docker's own", extra))
	}
	if out, err := u.docker(ctx, "info", "--format", "{{.Swarm.LocalNodeState}}"); err == nil {
		if st := strings.TrimSpace(string(out)); st != "" && st != "inactive" {
			why = append(why, "swarm mode ("+st+")")
		}
	}
	return why
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
