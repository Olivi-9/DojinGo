package collector

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"DojinGo/internal/config"
	"DojinGo/internal/httpclient"
)

var (
	URLFromTextRE = regexp.MustCompile(`((https://exhentai\.org/g/\w+/[\w-]+)|(https://e-hentai\.org/g/\w+/[\w-]+)|(https://nhentai\.(net|to)/g/\d+)|(https://www\.pixiv\.net/artworks/\d+))`)
	URLFromURLRE  = regexp.MustCompile(`^((https://exhentai\.org/g/\w+/[\w-]+)|(https://e-hentai\.org/g/\w+/[\w-]+)|(https://nhentai\.(net|to)/g/\d+)|(https://www\.pixiv\.net/artworks/\d+))`)
)

type AlbumMeta struct {
	Link string
	Name string
}

type ImageMeta struct {
	ID  string
	URL string
}

type Image struct {
	Meta ImageMeta
	Data []byte
}

type PageLoader func(context.Context, func(done, total int, stage string)) ([]Image, error)

type Result struct {
	Meta   AlbumMeta
	Loader PageLoader
}

type Collector interface {
	Name() string
	Match(rawURL string) bool
	Fetch(ctx context.Context, rawURL string) (*Result, error)
}

type Registry struct {
	collectors []Collector
}

func NewRegistry(cfg *config.Config) (*Registry, error) {
	ehHeaders := http.Header{}
	ehHeaders.Set("Cookie", "nw=1")
	ehClient, err := httpclient.New(cfg, ehHeaders)
	if err != nil {
		return nil, err
	}
	rawClient, err := httpclient.New(cfg, nil)
	if err != nil {
		return nil, err
	}

	nhClient, err := httpclient.New(cfg, nil)
	if err != nil {
		return nil, err
	}

	pixivHeaders := http.Header{}
	pixivHeaders.Set("Referer", "https://www.pixiv.net/")
	if cookie := strings.TrimSpace(cfg.Collectors.Pixiv.Cookie); cookie != "" {
		pixivHeaders.Set("Cookie", fmt.Sprintf("%s", cookie))
	}
	pixivClient, err := httpclient.New(cfg, pixivHeaders)
	if err != nil {
		return nil, err
	}

	exHeaders := http.Header{}
	if cfg.Collectors.Exhentai.IPBPassHash != "" &&
		cfg.Collectors.Exhentai.IPBMemberID != "" &&
		cfg.Collectors.Exhentai.Igneous != "" {
		exHeaders.Set(
			"Cookie",
			fmt.Sprintf(
				"ipb_pass_hash=%s;ipb_member_id=%s;igneous=%s;nw=1",
				cfg.Collectors.Exhentai.IPBPassHash,
				cfg.Collectors.Exhentai.IPBMemberID,
				cfg.Collectors.Exhentai.Igneous,
			),
		)
	}
	exClient, err := httpclient.NewWithOptions(cfg, exHeaders, httpclient.Options{ForceIPv4: true})
	if err != nil {
		return nil, err
	}
	exRawClient, err := httpclient.NewWithOptions(cfg, nil, httpclient.Options{ForceIPv4: true})
	if err != nil {
		return nil, err
	}

	return &Registry{
		collectors: []Collector{
			NewEHCollector(ehClient, rawClient),
			NewNHCollector(nhClient),
			NewEXCollector(exClient, exRawClient),
			NewPixivCollector(pixivClient),
		},
	}, nil
}

func (r *Registry) Resolve(rawURL string) (Collector, error) {
	for _, collector := range r.collectors {
		if collector.Match(rawURL) {
			return collector, nil
		}
	}
	return nil, fmt.Errorf("unsupported URL %q", rawURL)
}

func MatchURLFromText(content string) string {
	return firstSubmatch(URLFromTextRE, content)
}

func MatchURLFromURL(content string) string {
	return firstSubmatch(URLFromURLRE, content)
}

func firstSubmatch(re *regexp.Regexp, content string) string {
	match := re.FindStringSubmatch(content)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func retry(ctx context.Context, attempts int, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := fn(); err != nil {
			lastErr = err
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(200*(attempt+1)) * time.Millisecond):
			}
			continue
		}
		return nil
	}
	return lastErr
}

func normalizeGalleryURL(rawURL string) string {
	return strings.TrimRight(strings.TrimSpace(rawURL), "/")
}
