package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"DojinGo/internal/httpclient"
)

const (
	pixivBaseURL = "https://www.pixiv.net/artworks/"
	pixivAPIURL  = "https://www.pixiv.net/ajax/illust/"
)

var pixivURLRE = regexp.MustCompile(`^https://www\.pixiv\.net/artworks/(\d+)$`)

type PixivCollector struct {
	client *httpclient.Client
}

func NewPixivCollector(client *httpclient.Client) *PixivCollector {
	return &PixivCollector{client: client}
}

func (c *PixivCollector) Name() string {
	return "pixiv"
}

func (c *PixivCollector) Match(rawURL string) bool {
	return pixivURLRE.MatchString(strings.TrimSpace(rawURL))
}

// TODO: auto update cookie
func (c *PixivCollector) Fetch(ctx context.Context, rawURL string) (*Result, error) {
	pid, err := parsePixivID(rawURL)
	if err != nil {
		return nil, err
	}
	apiURL := fmt.Sprintf("%s%s/pages", pixivAPIURL, pid)

	var payload []byte
	if err := retry(ctx, 5, func() error {
		var err error
		payload, err = c.client.GetBytes(ctx, apiURL)
		return err
	}); err != nil {
		return nil, err
	}

	var response pixivPagesResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, fmt.Errorf("decode pixiv response: %w", err)
	}
	if response.Error {
		msg := strings.TrimSpace(response.Message)
		if msg == "" {
			msg = "pixiv api error"
		}
		return nil, fmt.Errorf("pixiv api error: %s", msg)
	}
	if len(response.Body) == 0 {
		return nil, fmt.Errorf("pixiv api returned empty body")
	}

	imageLinks := make([]string, 0, len(response.Body))
	for idx, page := range response.Body {
		link := strings.TrimSpace(page.URLs.ThumbMini)
		if link == "" {
			return nil, fmt.Errorf("pixiv page %d missing thumb_mini url", idx+1)
		}
		imageLinks = append(imageLinks, link)
	}

	return &Result{
		Meta: AlbumMeta{
			Link: pixivBaseURL + pid,
			Name: "pixiv-" + pid,
		},
		Loader: func(ctx context.Context, report func(done, total int, stage string)) ([]Image, error) {
			return loadImages(ctx, imageLinks, report, func(ctx context.Context, link string) (Image, error) {
				var data []byte
				if err := retry(ctx, 5, func() error {
					var err error
					data, err = c.client.GetBytes(ctx, link)
					return err
				}); err != nil {
					return Image{}, err
				}
				return Image{
					Meta: ImageMeta{ID: link, URL: link},
					Data: data,
				}, nil
			})
		},
	}, nil
}

type pixivPagesResponse struct {
	Error   bool        `json:"error"`
	Message string      `json:"message"`
	Body    []pixivPage `json:"body"`
}

type pixivPage struct {
	URLs pixivPageURLs `json:"urls"`
}

type pixivPageURLs struct {
	ThumbMini string `json:"regular"`
}

func parsePixivID(rawURL string) (string, error) {
	match := pixivURLRE.FindStringSubmatch(strings.TrimSpace(rawURL))
	if len(match) < 2 {
		return "", fmt.Errorf("invalid pixiv url %q", rawURL)
	}
	return match[1], nil
}
