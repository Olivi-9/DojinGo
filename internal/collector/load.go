package collector

import (
	"context"
	"sync"
)

const workerCount = 6

func loadImages(
	ctx context.Context,
	links []string,
	report func(done, total int, stage string),
	loadOne func(context.Context, string) (Image, error),
) ([]Image, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if report != nil {
		report(0, len(links), "downloading")
	}
	type job struct {
		index int
		link  string
	}
	type result struct {
		index int
		image Image
		err   error
	}

	jobs := make(chan job)
	results := make(chan result, len(links))
	var workers sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				image, err := loadOne(ctx, job.link)
				select {
				case results <- result{index: job.index, image: image, err: err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for idx, link := range links {
			select {
			case jobs <- job{index: idx, link: link}:
			case <-ctx.Done():
				return
			}
		}
	}()

	images := make([]Image, len(links))
	completed := 0
	for completed < len(links) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-results:
			if result.err != nil {
				cancel()
				return nil, result.err
			}
			images[result.index] = result.image
			completed++
			if report != nil {
				report(completed, len(links), "downloading")
			}
		}
	}
	workers.Wait()
	return images, nil
}
