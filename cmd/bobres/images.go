package main

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// Image pins. A tag on ghcr.io can be moved by anyone who can push to the
// packages, so an install does not trust it: the release workflow checks
// each image it built (public, and cosign-signed by that workflow run),
// writes the digests into the CLI it builds, and signs that CLI's checksum
// with the Ed25519 release key. install.sh and bobres update verify that
// signature before they run the CLI, and the CLI pins every image to its
// digest (BOBRES_IMAGE_DIGEST_<SERVICE> in .env, appended to the image's tag
// in docker-compose.yml). Docker then refuses any other content.

// imageDigests is set by the release build:
//
//	-ldflags "-X main.imageDigests=core=sha256:<hex>,bot=sha256:<hex>,..."
//
// with the digest of each image of this CLI's version. It is empty in a
// development or CI build, whose images run by tag (built on the same host).
var imageDigests string

// imageRepo is where the released images are (one per service).
const imageRepo = "ghcr.io/sobhanaz/bobres-"

// imageServices are the services that run a BOBRES image.
var imageServices = []string{"core", "bot", "payments", "provisioner", "backup"}

var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// digestKey is the .env line that pins service's image: "@sha256:<hex>",
// which docker-compose.yml appends to the image's tag, or empty (the tag
// alone).
func digestKey(service string) string { return "BOBRES_IMAGE_DIGEST_" + strings.ToUpper(service) }

// releaseDigests reads imageDigests: nil for a build without any, else the
// digest of every image service. Anything else is an error (a broken
// release build is never half pinned).
func releaseDigests(s string) (map[string]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	pins := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		svc, d, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || !slices.Contains(imageServices, svc) || !digestRe.MatchString(d) || pins[svc] != "" {
			return nil, fmt.Errorf("bad image digest %q (want <service>=sha256:<64 hex>, each service once)", part)
		}
		pins[svc] = d
	}
	for _, svc := range imageServices {
		if pins[svc] == "" {
			return nil, fmt.Errorf("no image digest for %s", svc)
		}
	}
	return pins, nil
}

// pinImages pins each image to this CLI's release digest when the install
// runs this CLI's own version, and clears the pins otherwise: another
// version (--version), or a build without digests, runs its images by tag.
func pinImages(env map[string]string, pins map[string]string) {
	own := pins != nil && env["BOBRES_VERSION"] == imageTag(version.Version)
	for _, svc := range imageServices {
		env[digestKey(svc)] = ""
		if own {
			env[digestKey(svc)] = "@" + pins[svc]
		}
	}
}

// versionImages is `bobres version --images`: the images this build pins,
// one reference per line. The release workflow runs it on the CLI it built
// before it signs it. Exit 1 when the build pins none or its digests are
// broken.
func versionImages(out, errOut io.Writer) int {
	pins, err := releaseDigests(imageDigests)
	switch {
	case err != nil:
		fmt.Fprintf(errOut, "error: this build's image digests are broken: %v\n", err)
		return 1
	case pins == nil:
		fmt.Fprintln(errOut, "this build pins no image digests (a development build: its images run by tag)")
		return 1
	}
	for _, svc := range imageServices {
		fmt.Fprintf(out, "%s%s:%s@%s\n", imageRepo, svc, imageTag(version.Version), pins[svc])
	}
	return 0
}
