package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultAPIBase = "https://api.github.com"

// ghRelease is the subset of the GitHub release object we consume.
type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
	Size        int64  `json:"size"`
}

// asset returns the release asset with the given name, or false.
func (r *ghRelease) asset(name string) (ghAsset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return ghAsset{}, false
}

// ghClient is a minimal read-only GitHub releases client. It needs no
// credentials for a public repo; GITHUB_TOKEN, when set, is forwarded
// only to lift the 60-req/hour unauthenticated rate limit.
type ghClient struct {
	repo    string // "owner/name"
	apiBase string // overridable in tests
	http    *http.Client
	token   string
}

func newGHClient(repo string) *ghClient {
	if repo == "" {
		repo = DefaultRepo
	}
	return &ghClient{
		repo:    repo,
		apiBase: defaultAPIBase,
		http:    &http.Client{Timeout: 120 * time.Second},
		token:   os.Getenv("GITHUB_TOKEN"),
	}
}

// resolveRelease picks the release to install. An explicit version
// wins; otherwise the channel selects from the 30 most recent releases
// IN THIS BINARY'S LANE — "stable" skips prereleases, "alpha" includes
// them. GitHub returns the list newest-first, so the first match is the
// latest. `tagPrefix` (hub-v / host-v) confines the search to the binary's
// own lane so a host never resolves a mobile/desktop release that carries no
// server tarball.
func (c *ghClient) resolveRelease(ctx context.Context, version, channel, tagPrefix string) (*ghRelease, error) {
	if version != "" {
		tag := normalizeVersionTag(version, tagPrefix)
		var rel ghRelease
		if err := c.getJSON(ctx, "/repos/"+c.repo+"/releases/tags/"+tag, &rel); err != nil {
			return nil, fmt.Errorf("resolve release %s: %w", tag, err)
		}
		return &rel, nil
	}
	var rels []ghRelease
	if err := c.getJSON(ctx, "/repos/"+c.repo+"/releases?per_page=30", &rels); err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	switch channel {
	case "", "stable":
		for i := range rels {
			if !strings.HasPrefix(rels[i].TagName, tagPrefix) {
				continue
			}
			if !rels[i].Draft && !rels[i].Prerelease {
				return &rels[i], nil
			}
		}
		return nil, fmt.Errorf("no stable %s* release in %s — the project currently ships "+
			"only -alpha tags; pass --channel alpha or an explicit --version", tagPrefix, c.repo)
	case "alpha":
		for i := range rels {
			if !strings.HasPrefix(rels[i].TagName, tagPrefix) {
				continue
			}
			if !rels[i].Draft {
				return &rels[i], nil
			}
		}
		return nil, fmt.Errorf("no published %s* release in %s", tagPrefix, c.repo)
	default:
		return nil, fmt.Errorf("unknown channel %q (want stable|alpha)", channel)
	}
}

func (c *ghClient) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("GitHub API %s: %s — %s",
			path, resp.Status, strings.TrimSpace(string(snippet)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// download GETs an asset URL; the caller closes the returned body.
func (c *ghClient) download(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

// normalizeTag ensures a leading "v" so "1.2.3" and "v1.2.3" both work.
// normalizeVersionTag maps an operator-supplied --version onto this lane's
// release tag. Accepts the full tag (`hub-v2026.724.305-alpha` → as-is), a bare
// version (`2026.724.305-alpha` → `hub-v2026.724.305-alpha`), or a legacy
// `v`-prefixed version (`v2026…` → strip the `v`, then prefix). The lane prefix
// itself ends in `v`, so an already-prefixed tag is detected before the bare-v
// strip.
func normalizeVersionTag(v, tagPrefix string) string {
	if strings.HasPrefix(v, tagPrefix) {
		return v
	}
	v = strings.TrimPrefix(v, "v")
	return tagPrefix + v
}
