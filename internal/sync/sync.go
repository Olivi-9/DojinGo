package sync

import (
	"context"
	"fmt"
	"log"
	"strings"
	gosync "sync"
	"time"

	"DojinGo/internal/collector"
	"DojinGo/internal/storage"
	"DojinGo/internal/telegraph"
)

const (
	uploadConcurrency = 4
	pageSizeLimit     = 48 * 1024
)

type ProgressFunc func(stage string, done, total int)

// ImageHost stores one image and returns the public URL to embed in Telegraph.
type ImageHost interface {
	Upload(ctx context.Context, data []byte) (string, error)
}

type Synchronizer struct {
	telegraph  *telegraph.Client
	imageHost  ImageHost
	registry   *collector.Registry
	store      storage.Store
	authorName string
	authorURL  string
	cacheTTL   time.Duration
}

func New(tg *telegraph.Client, imageHost ImageHost, registry *collector.Registry, store storage.Store, authorName, authorURL string, cacheTTL time.Duration) *Synchronizer {
	return &Synchronizer{
		telegraph:  tg,
		imageHost:  imageHost,
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

	coll, err := s.registry.Resolve(rawURL)
	if err != nil {
		return "", err
	}

	cacheKey := strings.ReplaceAll(coll.Name()+"|"+rawURL, "exhentai", "e-hentai")
	if cached, ok, err := s.store.Get(ctx, cacheKey); err == nil && ok {
		if progress != nil {
			progress("cache", 1, 1)
		}
		return cached, nil
	}

	result, err := coll.Fetch(ctx, rawURL)
	if err != nil {
		return "", err
	}

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

	if progress != nil {
		progress("upload", 0, len(images))
	}

	urls, err := s.uploadImages(ctx, images, progress)
	if err != nil {
		return "", err
	}
	nodes := make([]telegraph.Node, 0, len(urls))
	for _, src := range urls {
		nodes = append(nodes, telegraph.Image(src))
	}

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

// uploadImages uploads images concurrently and returns their URLs in the
// original order. The first failure cancels the remaining uploads.
func (s *Synchronizer) uploadImages(ctx context.Context, images []collector.Image, progress ProgressFunc) ([]string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	urls := make([]string, len(images))
	jobs := make(chan int)
	var (
		wg       gosync.WaitGroup
		mu       gosync.Mutex
		done     int
		firstErr error
	)
	for range min(uploadConcurrency, len(images)) {
		wg.Go(func() {
			for idx := range jobs {
				src, err := s.imageHost.Upload(ctx, images[idx].Data)
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = fmt.Errorf("upload image %d: %w", idx+1, err)
						cancel()
					}
				} else {
					urls[idx] = src
					done++
					if progress != nil {
						progress("upload", done, len(images))
					}
				}
				mu.Unlock()
			}
		})
	}

feed:
	for idx := range images {
		select {
		case jobs <- idx:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return urls, nil
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
			telegraph.TextNode("Powered by "),
			telegraph.Link("https://github.com/Olivi-9/DojinGo", telegraph.TextNode("DojinGo")),
		),
		telegraph.Paragraph(
			telegraph.TextNode("Origin: "),
			telegraph.Link(originalLink, telegraph.TextNode(originalLink)),
		),
	)
}
