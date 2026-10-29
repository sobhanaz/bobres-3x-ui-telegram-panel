package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type imageSize struct {
	Service  string           `json:"service"`
	Ref      string           `json:"ref"`
	Digest   string           `json:"digest,omitempty"`
	Platform map[string]int64 `json:"compressed_bytes,omitempty"` // linux/amd64 → layers + config
	Layers   map[string]int   `json:"layers,omitempty"`
	Error    string           `json:"error,omitempty"`
}

type ghcrResult struct {
	Kind    string      `json:"kind"`
	Version string      `json:"version"`
	At      string      `json:"at"`
	Images  []imageSize `json:"images"`
}

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

// ghcr reads the release images' manifests from ghcr.io anonymously (the
// registry's public token endpoint, as a store pulls them) and adds up each
// platform's compressed layer sizes: the download a server makes.
func ghcr(args []string) error {
	fs := flag.NewFlagSet("ghcr", flag.ContinueOnError)
	owner := fs.String("owner", "sobhanaz", "image owner")
	version := fs.String("version", "", "image tag, e.g. 0.19.0")
	svcs := fs.String("services", "core,bot,payments,provisioner,backup", "images")
	out := fs.String("out", "", "JSON result")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *version == "" || *out == "" {
		return errors.New("usage: ghcr -version 0.19.0 -out F.json")
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	res := ghcrResult{Kind: "ghcr", Version: *version, At: time.Now().UTC().Format(time.RFC3339)}
	for _, svc := range strings.Split(*svcs, ",") {
		img := imageSize{Service: svc, Ref: fmt.Sprintf("ghcr.io/%s/bobres-%s:%s", *owner, svc, *version)}
		if err := inspect(hc, *owner+"/bobres-"+svc, *version, &img); err != nil {
			img.Error = err.Error()
			fmt.Fprintf(os.Stderr, "  %s: %v\n", img.Ref, err)
		}
		res.Images = append(res.Images, img)
	}
	return writeJSON(*out, res)
}

func inspect(hc *http.Client, repo, tag string, img *imageSize) error {
	var tok struct {
		Token string `json:"token"`
	}
	q := url.Values{"scope": {"repository:" + repo + ":pull"}, "service": {"ghcr.io"}}
	if err := getJSON(hc, "https://ghcr.io/token?"+q.Encode(), "", "", &tok, nil); err != nil {
		return fmt.Errorf("token: %w", err)
	}
	var index struct {
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				OS, Architecture string
			} `json:"platform"`
		} `json:"manifests"`
	}
	var digest string
	if err := getJSON(hc, "https://ghcr.io/v2/"+repo+"/manifests/"+tag, tok.Token, manifestAccept, &index, &digest); err != nil {
		return err
	}
	img.Digest, img.Platform, img.Layers = digest, map[string]int64{}, map[string]int{}
	for _, m := range index.Manifests {
		if m.Platform.OS == "unknown" || m.Platform.OS == "" { // attestations
			continue
		}
		var man struct {
			Config struct {
				Size int64 `json:"size"`
			} `json:"config"`
			Layers []struct {
				Size int64 `json:"size"`
			} `json:"layers"`
		}
		if err := getJSON(hc, "https://ghcr.io/v2/"+repo+"/manifests/"+m.Digest, tok.Token, manifestAccept, &man, nil); err != nil {
			return err
		}
		plat := m.Platform.OS + "/" + m.Platform.Architecture
		total := man.Config.Size
		for _, l := range man.Layers {
			total += l.Size
		}
		img.Platform[plat], img.Layers[plat] = total, len(man.Layers)
	}
	return nil
}

func getJSON(hc *http.Client, u, bearer, accept string, v any, digest *string) error {
	req, err := http.NewRequest(http.MethodGet, u, nil) //nolint:noctx // bounded by the client's timeout
	if err != nil {
		return err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // read only
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("%s: %s %s", u, resp.Status, strings.TrimSpace(string(b)))
	}
	if digest != nil {
		*digest = resp.Header.Get("Docker-Content-Digest")
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}
