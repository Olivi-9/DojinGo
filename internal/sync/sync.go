package sync

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"DojinGo/internal/collector"
	"DojinGo/internal/storage"
	"DojinGo/internal/telegraph"
)

const (
	batchLenThreshold  = 20
	batchSizeThreshold = 5 * 1024 * 1024
	pageSizeLimit      = 48 * 1024
)

type ProgressFunc func(stage string, done, total int)

type Synchronizer struct {
	telegraph  *telegraph.Client
	registry   *collector.Registry
	store      storage.Store
	authorName string
	authorURL  string
	cacheTTL   time.Duration
}

func New(tg *telegraph.Client, registry *collector.Registry, store storage.Store, authorName, authorURL string, cacheTTL time.Duration) *Synchronizer {
	return &Synchronizer{
		telegraph:  tg,
		registry:   registry,
		store:      store,
		authorName: authorName,
		authorURL:  authorURL,
		cacheTTL:   cacheTTL,
	}
}

func MatchURLFromText(content string) string {
	return collector.MatchURLFromText(content)
}

func MatchURLFromURL(content string) string {
	return collector.MatchURLFromURL(content)
}

func (s *Synchronizer) DeleteCache(ctx context.Context, key string) error {
	return s.store.Delete(ctx, key)
}

func (s *Synchronizer) Sync(ctx context.Context, rawURL string, progress ProgressFunc) (string, error) {
	rawURL = strings.TrimRight(strings.TrimSpace(rawURL), "/")
	log.Printf("sync start url=%s", rawURL)

	coll, err := s.registry.Resolve(rawURL)
	if err != nil {
		return "", err
	}
	log.Printf("sync collector=%s", coll.Name())

	cacheKey := strings.ReplaceAll(coll.Name()+"|"+rawURL, "exhentai", "e-hentai")
	if cached, ok, err := s.store.Get(ctx, cacheKey); err == nil && ok {
		if progress != nil {
			progress("cache", 1, 1)
		}
		log.Printf("sync cache hit key=%s", cacheKey)
		return cached, nil
	}
	log.Printf("sync cache miss key=%s", cacheKey)

	result, err := coll.Fetch(ctx, rawURL)
	if err != nil {
		return "", err
	}
	log.Printf("sync collect complete title=%s", result.Meta.Name)

	if progress != nil {
		progress("collect", 0, 1)
	}
	images, err := result.Loader(ctx, func(done, total int, stage string) {
		if progress != nil {
			progress(stage, done, total)
		}
	})
	if err != nil {
		return "", err
	}
	log.Printf("sync download complete count=%d", len(images))

	if progress != nil {
		progress("upload", 0, len(images))
	}

	nodes := make([]telegraph.Node, 0, len(images))
	doneUploads := 0
	for start := 0; start < len(images); {
		chunk, size, next := nextUploadChunk(images, start)
		log.Printf("sync upload chunk start=%d count=%d size=%d", start, len(chunk), size)
		payload := make([][]byte, 0, len(chunk))
		for _, image := range chunk {
			payload = append(payload, image.Data)
		}

		uploaded, err := s.telegraph.Upload(ctx, payload)
		if err != nil {
			return "", err
		}
		_ = size
		for _, media := range uploaded {
			nodes = append(nodes, telegraph.Image(media.Src))
		}
		doneUploads += len(uploaded)
		if progress != nil {
			progress("upload", doneUploads, len(images))
		}
		start = next
	}

	log.Printf("sync upload complete count=%d", doneUploads)

	page, err := s.createPages(ctx, result.Meta, nodes)
	if err != nil {
		return "", err
	}
	log.Printf("sync page created url=%s", page.URL)
	if err := s.store.Set(ctx, cacheKey, page.URL, s.cacheTTL); err != nil {
		return "", err
	}
	return page.URL, nil
}

func nextUploadChunk(images []collector.Image, start int) ([]collector.Image, int, int) {
	size := 0
	end := start
	for end < len(images) {
		nextSize := size + len(images[end].Data)
		if end > start && (end-start >= batchLenThreshold || nextSize > batchSizeThreshold) {
			break
		}
		size = nextSize
		end++
	}
	return images[start:end], size, end
}

func (s *Synchronizer) createPages(ctx context.Context, meta collector.AlbumMeta, nodes []telegraph.Node) (*telegraph.Page, error) {
	chunks := [][]telegraph.Node{{}}
	lastChunkSize := 0
	for _, node := range nodes {
		size := node.EstimateSize()
		if lastChunkSize+size > pageSizeLimit {
			chunks = append(chunks, []telegraph.Node{})
			lastChunkSize = 0
		}
		lastChunkSize += size
		chunks[len(chunks)-1] = append(chunks[len(chunks)-1], node)
	}

	title := strings.ReplaceAll(meta.Name, "|", "")
	var lastPage *telegraph.Page
	for idx := len(chunks) - 1; idx >= 0; idx-- {
		content := append([]telegraph.Node(nil), chunks[idx]...)
		writeFooter(&content, meta.Link, lastPage)
		pageTitle := title
		if idx != 0 {
			pageTitle = fmt.Sprintf("%s-Page%d", title, idx+1)
		}
		page, err := s.telegraph.CreatePage(ctx, telegraph.PageCreate{
			Title:      pageTitle,
			Content:    content,
			AuthorName: s.authorName,
			AuthorURL:  s.authorURL,
		})
		if err != nil {
			return nil, err
		}
		lastPage = page
	}
	if lastPage == nil {
		return nil, fmt.Errorf("no page created")
	}
	return lastPage, nil
}

func writeFooter(content *[]telegraph.Node, originalLink string, nextPage *telegraph.Page) {
	if nextPage != nil {
		*content = append(*content, telegraph.Paragraph(telegraph.Link(nextPage.URL, telegraph.TextNode("Next Page"))))
	}
	*content = append(*content,
		telegraph.Paragraph(
			telegraph.TextNode("Generated by "),
			telegraph.Link("https://DojinGo", telegraph.TextNode("eh2telegraph")),
		),
		telegraph.Paragraph(
			telegraph.TextNode("Original link: "),
			telegraph.Link(originalLink, telegraph.TextNode(originalLink)),
		),
	)
}
