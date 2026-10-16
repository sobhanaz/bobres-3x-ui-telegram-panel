package deploy

import (
	"os"
	"regexp"
	"testing"
)

// The images the stack pulls from Docker Hub (Postgres, Redis, Caddy) and
// the Postgres image the backup service is built on are pinned by digest, so
// a moved tag never changes what runs: Docker checks the content it pulls
// against the digest. The images of this project are the ones that carry
// ${BOBRES_IMAGE_DIGEST_*} and are not listed here.
func TestThirdPartyImagesArePinnedByDigest(t *testing.T) {
	pinned := regexp.MustCompile(`@sha256:[0-9a-f]{64}`)
	own := regexp.MustCompile(`(?m)^\s*image:\s*(\S+)`)
	third := regexp.MustCompile(`^(postgres|redis|caddy):`)
	for _, file := range []string{"docker-compose.yml", "vendor/docker-compose.yml"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, m := range own.FindAllStringSubmatch(string(raw), -1) {
			if !third.MatchString(m[1]) {
				continue
			}
			n++
			if !pinned.MatchString(m[1]) {
				t.Errorf("%s: image %s is not pinned by digest", file, m[1])
			}
		}
		if n == 0 {
			t.Errorf("%s: no third-party image found; is the test out of date?", file)
		}
	}
	raw, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	from := regexp.MustCompile(`(?m)^FROM\s+(postgres:\S+)`).FindStringSubmatch(string(raw))
	if from == nil || !pinned.MatchString(from[1]) {
		t.Errorf("Dockerfile: the backup stage's postgres image is not pinned by digest (%v)", from)
	}
}
