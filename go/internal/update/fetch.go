package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// download fetches the release archive into memory and returns its bytes.
//
// The read is capped at MaxBytes+1 so a wrong-content or hostile URL costs a
// fixed amount, and going over the cap is an error rather than a silently
// truncated archive. `release-go.yml` publishes ~20 MB gzipped per asset;
// MaxBytes defaults to 128 MiB, which bounds the download and (with the archive
// already in RAM) the peak.
//
// ponytail: MaxBytes is a fixed ceiling, not an adaptive one — the upgrade path
// is sizing it from the asset's declared `size` in the API response, which the
// workflow already uploads implicitly. Override with LEANKG_UPDATE_MAX_MB.
func download(ctx context.Context, o *Options, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "leankg-update")
	// The token is scoped to the API host: browser_download_url redirects to a
	// CDN that must never see it.
	if o.Token != "" && strings.HasPrefix(url, strings.TrimSuffix(o.APIBase, "/")) {
		req.Header.Set("Authorization", "Bearer "+o.Token)
	}
	res, err := o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: HTTP %d", url, res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, o.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	if int64(len(data)) > o.MaxBytes {
		return nil, fmt.Errorf("%s: the archive is larger than the %d byte limit (override with LEANKG_UPDATE_MAX_MB)", url, o.MaxBytes)
	}
	return data, nil
}
