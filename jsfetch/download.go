package main

import (
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// RunDownloads fans jobs out to numWorkers goroutines and collects results.
// Each worker honours an optional rateLimit delay between requests.
func RunDownloads(
	client *http.Client,
	buildReq func(string) (*http.Request, error),
	jobs []Job,
	numWorkers int,
	rateLimit time.Duration,
	maxRetries int,
	log func(string, ...any),
) []DownloadResult {
	if numWorkers < 1 {
		numWorkers = 1
	}
	if len(jobs) > 0 && numWorkers > len(jobs) {
		numWorkers = len(jobs)
	}

	jobCh    := make(chan Job, len(jobs))
	resultCh := make(chan DownloadResult, len(jobs))

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				if rateLimit > 0 {
					time.Sleep(rateLimit)
				}

				resp, err := fetchWithRetry(client, buildReq, job.srcURL, maxRetries, log)
				if err != nil {
					resultCh <- DownloadResult{URL: job.srcURL, Err: err}
					continue
				}

				data, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					resultCh <- DownloadResult{URL: job.srcURL, Err: err}
					continue
				}

				if err := os.WriteFile(job.outPath, data, 0644); err != nil {
					resultCh <- DownloadResult{URL: job.srcURL, Err: err}
					continue
				}

				resultCh <- DownloadResult{
					URL:      job.srcURL,
					SavePath: job.outPath,
					Bytes:    len(data),
				}
			}
		}()
	}

	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var results []DownloadResult
	for r := range resultCh {
		results = append(results, r)
	}
	return results
}
