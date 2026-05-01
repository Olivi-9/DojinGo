package collector

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"DojinGo/internal/httpclient"
)

var (
	ehPageRE  = regexp.MustCompile(`<a href="(https://e-hentai\.org/s/\w+/[\w-]+)">`)
	ehImageRE = regexp.MustCompile(`<img id="img" src="(.*?)"`)
	ehTitleRE = regexp.MustCompile(`<h1 id="gn">(.*?)</h1>`)
)

type EHCollector struct {
	client    *httpclient.Client
	rawClient *httpclient.Client
}

func NewEHCollector(client, rawClient *httpclient.Client) *EHCollector {
	return &EHCollector{client: client, rawClient: rawClient}
}

func (c *EHCollector) Name() string {
	return "e-hentai"
}

func (c *EHCollector) Match(rawURL string) bool {
	return strings.Contains(rawURL, "://e-hentai.org/g/")
}

func (c *EHCollector) Fetch(ctx context.Context, rawURL string) (*Result, error) {
	log.Printf("collector e-hentai fetch start url=%s", rawURL)
	albumURL, albumID, err := normalizeEHGalleryURL(rawURL, "e-hentai.org")
	if err != nil {
		return nil, err
	}

	pages, err := pagedFetch(ctx, c.client, albumURL)
	if err != nil {
		return nil, err
	}
	log.Printf("collector e-hentai pages fetched count=%d", len(pages))

	title := firstSubmatch(ehTitleRE, pages[0])
	if title == "" {
		title = "e-hentai-" + albumID
	}

	imagePages := collectMatches(ehPageRE, pages)
	if len(imagePages) == 0 {
		return nil, fmt.Errorf("invalid url, maybe resource has been deleted")
	}
	log.Printf("collector e-hentai image pages count=%d", len(imagePages))

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
				imageURL := firstSubmatch(ehImageRE, page)
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

func normalizeEHGalleryURL(rawURL, host string) (string, string, error) {
	rawURL = normalizeGalleryURL(rawURL)
	parts := strings.Split(strings.TrimPrefix(rawURL, "https://"+host), "/")
	if len(parts) < 4 || parts[1] != "g" {
		return "", "", fmt.Errorf("invalid input path(%s), gallery url is expected", rawURL)
	}
	return fmt.Sprintf("https://%s/g/%s/%s", host, parts[2], parts[3]), parts[2], nil
}

func collectMatches(re *regexp.Regexp, pages []string) []string {
	var matches []string
	for _, page := range pages {
		for _, matched := range re.FindAllStringSubmatch(page, -1) {
			if len(matched) > 1 {
				matches = append(matches, matched[1])
			}
		}
	}
	return matches
}

func pagedFetch(ctx context.Context, client *httpclient.Client, baseURL string) ([]string, error) {
	var pages []string
	log.Printf("collector paged fetch start base=%s", baseURL)
	for page := 0; ; page++ {
		url := fmt.Sprintf("%s/?p=%d", baseURL, page)
		content, err := client.GetString(ctx, url)
		if err != nil {
			return nil, err
		}
		pages = append(pages, content)
		nextHTML := fmt.Sprintf("<a href=\"%s/?p=%d\" onclick=\"return false\">", baseURL, page+1)
		if !strings.Contains(content, nextHTML) {
			log.Printf("collector paged fetch done base=%s pages=%d", baseURL, len(pages))
			return pages, nil
		}
	}
}
