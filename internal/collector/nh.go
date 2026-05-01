package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"

	"DojinGo/internal/httpclient"
)

const nhAPI = "https://nhapi.cat42.uk/gallery/"

var nhCDNList = []string{
	"https://i1.nhentai.net/galleries",
	"https://i2.nhentai.net/galleries",
	"https://i3.nhentai.net/galleries",
	"https://i4.nhentai.net/galleries",
}

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

	imageURLs := make([]imageURL, 0, len(album.Images.Pages))
	for idx, page := range album.Images.Pages {
		imageURLs = append(imageURLs, newImageURL(album.MediaID, idx+1, page.T))
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
	MediaID string   `json:"media_id"`
	Title   nhTitle  `json:"title"`
	Images  nhImages `json:"images"`
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

type nhImages struct {
	Pages []nhImage `json:"pages"`
}

type nhImage struct {
	T imageType `json:"t"`
}

type imageType string

func (t imageType) extension() string {
	switch t {
	case "j":
		return ".jpg"
	case "p":
		return ".png"
	case "g":
		return ".gif"
	case "w":
		return ".webp"
	default:
		return ".jpg"
	}
}

type imageURL struct {
	Raw     string
	MediaID string
	Page    int
	Type    imageType
}

func newImageURL(mediaID string, page int, typ imageType) imageURL {
	return imageURL{
		Raw:     randomCDNLink(mediaID, page, typ),
		MediaID: mediaID,
		Page:    page,
		Type:    typ,
	}
}

func (i imageURL) fallback() string {
	return randomCDNLink(i.MediaID, i.Page, i.Type)
}

func randomCDNLink(mediaID string, page int, typ imageType) string {
	return fmt.Sprintf("%s/%s/%d%s", nhCDNList[rand.Intn(len(nhCDNList))], mediaID, page, typ.extension())
}

func findFallback(urls []imageURL, current string) string {
	for _, imageURL := range urls {
		if imageURL.Raw == current {
			return imageURL.fallback()
		}
	}
	return ""
}
