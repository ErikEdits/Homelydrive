// Package version holds the build version and the GitHub release check that
// the server and the PC app use to show "Neue Version verfügbar".
package version

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Version is set at build time via -ldflags "-X github.com/erikedits/homelydrive/internal/version.Version=v1.2.3".
var Version = "dev"

// Repo is the GitHub repository releases are published to.
const Repo = "ErikEdits/Homelydrive"

// Release describes the newest published GitHub release.
type Release struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Newer   bool   `json:"newer"`
}

// CheckLatest asks the GitHub API for the newest release and reports whether
// it is newer than the running build.
func CheckLatest(ctx context.Context) (*Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "homelydrive/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return &Release{}, nil // no release published yet
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return &Release{
		Version: body.TagName,
		URL:     body.HTMLURL,
		Newer:   IsNewer(body.TagName, Version),
	}, nil
}

// IsNewer reports whether version a is newer than version b. Versions look
// like "v1.2.3"; a development build ("dev") is never considered outdated.
func IsNewer(a, b string) bool {
	if b == "dev" || a == "" {
		return false
	}
	pa, pb := parse(a), parse(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parse(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}
