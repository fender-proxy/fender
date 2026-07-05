package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/fender-proxy/fender/internal/config"
	"github.com/fender-proxy/fender/internal/image"
)

// Precompiled route matchers.
var (
	// POST /v*/containers/create
	reContainersCreate = regexp.MustCompile(`^(?:/v[\d.]+)?/containers/create$`)

	// POST /v*/images/create  (docker pull)
	reImagesCreate = regexp.MustCompile(`^(?:/v[\d.]+)?/images/create$`)

	// Any /v*/images/{name}[/suffix] path — used for inspect, push, delete, history, tag.
	// We explicitly exclude /images/create which has its own handler.
	reImagePath = regexp.MustCompile(`^(?:/v[\d.]+)?/images/(.+)$`)
)

// imageSuffixes are the path suffixes that appear after the image name in
// Docker API paths. Knowing these lets us correctly extract the name portion.
var imageSuffixes = []string{
	"/json",
	"/history",
	"/push",
	"/tag",
	"/changes",
}

// rewriteRequest inspects r and rewrites any Docker image references it finds
// according to the proxy configuration. The request is modified in-place.
func rewriteRequest(r *http.Request, cfg *config.Config) {
	// Short-circuit if no rewriting is configured.
	if cfg.DefaultRegistry == "" && len(cfg.RegistryMap) == 0 {
		return
	}

	path := r.URL.Path

	switch {
	// POST /containers/create — image name is in the JSON request body.
	case r.Method == http.MethodPost && reContainersCreate.MatchString(path):
		if err := rewriteContainersCreate(r, cfg); err != nil {
			slog.Warn("containers/create body rewrite failed", "err", err)
		}

	// POST /images/create — image name is in the `fromImage` query parameter.
	case r.Method == http.MethodPost && reImagesCreate.MatchString(path):
		rewriteImagesCreate(r, cfg)

	// GET|DELETE|POST /images/{name}[/suffix] — image name is in the URL path.
	// Exclude /images/create which is handled above.
	case reImagePath.MatchString(path) && !reImagesCreate.MatchString(path):
		rewriteImageInPath(r, cfg)
	}
}

// rewriteContainersCreate rewrites the "Image" field inside the JSON body of
// a POST /containers/create request.
func rewriteContainersCreate(r *http.Request, cfg *config.Config) error {
	if r.Body == nil {
		return nil
	}

	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		// Restore body so the request is still forwarded.
		r.Body = io.NopCloser(bytes.NewReader(body))
		return err
	}

	// Use RawMessage to preserve all other JSON fields exactly as-is.
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		// Not valid JSON or unexpected shape — pass through unchanged.
		r.Body = io.NopCloser(bytes.NewReader(body))
		return nil
	}

	if raw, ok := payload["Image"]; ok {
		var imgName string
		if err := json.Unmarshal(raw, &imgName); err == nil && imgName != "" {
			slog.Debug("containers/create Image received", "value", imgName)
			rewritten := image.Rewrite(imgName, cfg.DefaultRegistry, cfg.RegistryMap)
			if rewritten != imgName {
				slog.Debug("rewriting Image in containers/create",
					"original", imgName,
					"rewritten", rewritten,
				)
				encoded, _ := json.Marshal(rewritten)
				payload["Image"] = encoded
			}
		}
	}

	newBody, err := json.Marshal(payload)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		return err
	}

	r.Body = io.NopCloser(bytes.NewReader(newBody))
	r.ContentLength = int64(len(newBody))
	return nil
}

// rewriteImagesCreate rewrites the `fromImage` query parameter of a
// POST /images/create request (i.e. `docker pull`).
func rewriteImagesCreate(r *http.Request, cfg *config.Config) {
	q := r.URL.Query()
	fromImage := q.Get("fromImage")
	if fromImage == "" {
		return
	}

	slog.Debug("images/create fromImage received", "value", fromImage)

	rewritten := image.Rewrite(fromImage, cfg.DefaultRegistry, cfg.RegistryMap)
	if rewritten == fromImage {
		slog.Debug("images/create fromImage unchanged", "value", fromImage)
		return
	}

	slog.Debug("rewriting fromImage query param",
		"original", fromImage,
		"rewritten", rewritten,
	)
	q.Set("fromImage", rewritten)
	r.URL.RawQuery = q.Encode()
}

// rewriteImageInPath rewrites the image name embedded in Docker API URL paths
// such as:
//
//	GET  /v1.41/images/nginx:latest/json
//	POST /v1.41/images/myorg/app:v1/push
//	DELETE /v1.41/images/ghcr.io/org/app:v1
func rewriteImageInPath(r *http.Request, cfg *config.Config) {
	m := reImagePath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		return
	}

	// m[1] is everything after /images/ — may be "nginx:latest/json" etc.
	rest := m[1]

	// Split off any known suffix so we can isolate the image name.
	imgName := rest
	suffix := ""
	for _, sfx := range imageSuffixes {
		if strings.HasSuffix(rest, sfx) {
			imgName = rest[:len(rest)-len(sfx)]
			suffix = sfx
			break
		}
	}

	// URL-decode the image name in case the Docker CLI percent-encoded it.
	decoded, err := url.PathUnescape(imgName)
	if err != nil {
		decoded = imgName
	}

	rewritten := image.Rewrite(decoded, cfg.DefaultRegistry, cfg.RegistryMap)
	if rewritten == decoded {
		return
	}

	slog.Debug("rewriting image name in URL path",
		"original", decoded,
		"rewritten", rewritten,
	)

	// Reconstruct the path prefix (everything up to and including /images/).
	prefix := r.URL.Path[:len(r.URL.Path)-len(rest)]
	r.URL.Path = prefix + rewritten + suffix
	r.URL.RawPath = "" // clear so net/http uses the decoded Path
}
