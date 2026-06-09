package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"

	"DojinGo/internal/httpclient"
)

const (
	nhAPI    = "https://nhentai.net/api/v2/galleries/"
	nhCDNAPI = "https://nhentai.net/api/v2/cdn"
)

type NHCollector struct {
	client *httpclient.Client
}

func NewNHCollector(client *httpclient.Client) *NHCollector {
	return &NHCollector{client: client}
}

func (c *NHCollector) Name() string {
	return "nhentai"
}

func (c *NHCollector) Match(rawURL string) bool {
	return strings.Contains(rawURL, "://nhentai.net/g/") || strings.Contains(rawURL, "://nhentai.to/g/")
}

func (c *NHCollector) Fetch(ctx context.Context, rawURL string) (*Result, error) {
	rawURL = normalizeGalleryURL(rawURL)
	parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://nhentai.net"), "https://nhentai.to"), "/")
	if len(parts) < 3 || parts[1] != "g" {
		return nil, fmt.Errorf("invalid input path(%s), gallery url is expected", rawURL)
	}
	albumID := parts[2]

	var payload []byte
	if err := retry(ctx, 5, func() error {
		var err error
		payload, err = c.client.GetBytes(ctx, nhAPI+albumID)
		return err
	}); err != nil {
		return nil, err
	}

	var album nhAlbum
	if err := json.Unmarshal(payload, &album); err != nil {
		return nil, fmt.Errorf("decode nhentai album: %w", err)
	}

	var cdnPayload []byte
	if err := retry(ctx, 5, func() error {
		var err error
		cdnPayload, err = c.client.GetBytes(ctx, nhCDNAPI)
		return err
	}); err != nil {
		return nil, err
	}

	var cdn nhCDN
	if err := json.Unmarshal(cdnPayload, &cdn); err != nil {
		return nil, fmt.Errorf("decode nhentai cdn: %w", err)
	}
	if len(cdn.ImageServers) == 0 {
		return nil, fmt.Errorf("nhentai cdn returned no image servers")
	}

	imageURLs := make([]imageURL, 0, len(album.Pages))
	for _, page := range album.Pages {
		imageURLs = append(imageURLs, newImageURL(cdn.ImageServers, page.Path))
	}

	return &Result{
		Meta: AlbumMeta{
			Link: "https://nhentai.net/g/" + albumID,
			Name: album.Title.bestTitle("nhentai-" + albumID),
		},
		Loader: func(ctx context.Context, report func(done, total int, stage string)) ([]Image, error) {
			links := make([]string, 0, len(imageURLs))
			for _, imageURL := range imageURLs {
				links = append(links, imageURL.Raw)
			}
			return loadImages(ctx, links, report, func(ctx context.Context, link string) (Image, error) {
				var data []byte
				err := retry(ctx, 5, func() error {
					var err error
					data, err = c.client.GetBytes(ctx, link)
					return err
				})
				if err != nil {
					fallback := findFallback(imageURLs, link)
					if fallback != "" {
						err = retry(ctx, 5, func() error {
							var inner error
							data, inner = c.client.GetBytes(ctx, fallback)
							return inner
						})
						link = fallback
					}
				}
				if err != nil {
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

type nhAlbum struct {
	Title nhTitle  `json:"title"`
	Pages []nhPage `json:"pages"`
}

type nhPage struct {
	Number    int    `json:"number"`
	Path      string `json:"path"`
	Thumbnail string `json:"thumbnail"`
}

type nhCDN struct {
	ImageServers []string `json:"image_servers"`
	ThumbServers []string `json:"thumb_servers"`
}

type nhTitle struct {
	Pretty   string `json:"pretty"`
	English  string `json:"english"`
	Japanese string `json:"japanese"`
}

func (t nhTitle) bestTitle(fallback string) string {
	switch {
	case t.Pretty != "":
		return t.Pretty
	case t.English != "":
		return t.English
	case t.Japanese != "":
		return t.Japanese
	default:
		return fallback
	}
}

type imageURL struct {
	Raw     string
	Path    string
	Servers []string
}

func newImageURL(servers []string, path string) imageURL {
	return imageURL{
		Raw:     joinCDN(servers[rand.Intn(len(servers))], path),
		Path:    path,
		Servers: servers,
	}
}

func (i imageURL) fallback() string {
	return joinCDN(i.Servers[rand.Intn(len(i.Servers))], i.Path)
}

func joinCDN(server, path string) string {
	return strings.TrimSuffix(server, "/") + "/" + strings.TrimPrefix(path, "/")
}

func findFallback(urls []imageURL, current string) string {
	for _, imageURL := range urls {
		if imageURL.Raw == current {
			return imageURL.fallback()
		}
	}
	return ""
}
