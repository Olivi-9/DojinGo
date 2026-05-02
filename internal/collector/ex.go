package collector

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"DojinGo/internal/httpclient"
)

var (
	exPageRE  = regexp.MustCompile(`<a href="(https://exhentai\.org/s/\w+/[\w-]+)">`)
	exImageRE = regexp.MustCompile(`<img id="img" src="(.*?)"`)
	exTitleRE = regexp.MustCompile(`<h1 id="gn">(.*?)</h1>`)
)

type EXCollector struct {
	client    *httpclient.Client
	rawClient *httpclient.Client
}

func NewEXCollector(client, rawClient *httpclient.Client) *EXCollector {
	return &EXCollector{client: client, rawClient: rawClient}
}

func (c *EXCollector) Name() string {
	return "exhentai"
}

func (c *EXCollector) Match(rawURL string) bool {
	return strings.Contains(rawURL, "://exhentai.org/g/")
}

func (c *EXCollector) Fetch(ctx context.Context, rawURL string) (*Result, error) {
	albumURL, albumID, err := normalizeEHGalleryURL(rawURL, "exhentai.org")
	if err != nil {
		return nil, err
	}
	pages, err := pagedFetch(ctx, c.client, albumURL)
	if err != nil {
		return nil, err
	}

	title := firstSubmatch(exTitleRE, pages[0])
	if title == "" {
		title = "exhentai-" + albumID
	}
	imagePages := collectMatches(exPageRE, pages)
	if len(imagePages) == 0 {
		return nil, fmt.Errorf("invalid url, maybe resource has been deleted, or our ip is blocked")
	}

	return &Result{
		Meta: AlbumMeta{
			Link: albumURL,
			Name: title,
		},
		Loader: func(ctx context.Context, report func(done, total int, stage string)) ([]Image, error) {
			return loadImages(ctx, imagePages, report, func(ctx context.Context, link string) (Image, error) {
				var page string
				if err := retry(ctx, 5, func() error {
					var err error
					page, err = c.client.GetString(ctx, link)
					return err
				}); err != nil {
					return Image{}, err
				}
				imageURL := firstSubmatch(exImageRE, page)
				if imageURL == "" {
					return Image{}, fmt.Errorf("unable to find image in page %s", link)
				}
				var data []byte
				if err := retry(ctx, 5, func() error {
					var err error
					data, err = c.rawClient.GetBytes(ctx, imageURL)
					return err
				}); err != nil {
					return Image{}, err
				}
				return Image{
					Meta: ImageMeta{ID: link, URL: imageURL},
					Data: data,
				}, nil
			})
		},
	}, nil
}
