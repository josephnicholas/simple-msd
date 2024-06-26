package core

import (
	"fmt"
	"github.com/vbauerster/mpb"
	"github.com/vbauerster/mpb/decor"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
)

type Downloader struct {
	File              FileDetails
	ChunkSize         int64
	NumberOfChunks    int64
	SingleProgressBar bool
}

func (d *Downloader) Execute() {
	fileDetails := d.File.GetFileDetails()
	numberOfChunks := d.calculateConcurrentWorker(fileDetails.Size)

	wg := sync.WaitGroup{}
	chunks := make([]chan []byte, numberOfChunks)
	pb := mpb.New(mpb.WithWaitGroup(&wg))
	bars := &mpb.Bar{}

	if d.SingleProgressBar {
		host, _ := d.getHostName(-1)
		bars = d.createProgressBar(host, pb, fileDetails.Size)
	}

	for i := int64(0); i < numberOfChunks; i++ {
		chunks[i] = make(chan []byte)

		startByte, endByte, currentChunkSize := d.calculateChunk(i, numberOfChunks, fileDetails)
		if currentChunkSize <= 0 {
			continue
		}

		counter := int(i % fileDetails.Urls.GetSize())
		host, fileUrl := d.getHostName(counter)
		if !d.SingleProgressBar {
			bars = d.createProgressBar(host, pb, currentChunkSize)
		}

		wg.Add(1)
		go d.DownloadWorker(fileUrl, startByte, endByte, &wg, bars, &chunks[i])
	}

	go func() {
		wg.Wait()
		for i := range chunks {
			close(chunks[i])
		}
	}()

	const flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	WriteToFile(flag, fileDetails.Name, chunks)
}

func (d *Downloader) getHostName(counter int) (string, string) {
	fileUrl, _ := d.File.Urls.GetFirstUrl()
	if counter >= 0 {
		fileUrl = d.File.Urls.GetUrls()[counter]
	}

	parsedUrl, err := url.Parse(fileUrl)
	if err != nil {
		log.Fatal(err)
	}

	return parsedUrl.Host, fileUrl
}

func (d *Downloader) calculateChunk(i int64, numberOfChunks int64, fileDetails *FileDetails) (int64, int64, int64) {
	startByte := i * d.ChunkSize
	endByte := startByte + d.ChunkSize - 1
	if i+1 == numberOfChunks || endByte > fileDetails.Size-1 {
		endByte = fileDetails.Size - 1
	}

	currentChunkSize := endByte - startByte + 1
	return startByte, endByte, currentChunkSize
}

func (d *Downloader) createProgressBar(host string, pb *mpb.Progress, barSize int64) *mpb.Bar {
	bars := pb.AddBar(barSize,
		mpb.PrependDecorators(
			decor.CountersKibiByte("% .2f / % .2f"),
			decor.Name(fmt.Sprintf(" [%s]", host)),
		),
		mpb.AppendDecorators(
			decor.EwmaETA(decor.ET_STYLE_MMSS, float64(d.ChunkSize)/2048),
			decor.Name(" ] "),
			decor.AverageSpeed(decor.UnitKiB, "% .2f"),
		),
	)

	return bars
}

func (d *Downloader) DownloadWorker(fileUrl string, startByte int64, endByte int64, wg *sync.WaitGroup, bar *mpb.Bar, chunk *chan []byte) {
	defer wg.Done()

	maxRetry := 3
	for i := 0; i < maxRetry; i++ {
		req, err := http.NewRequest("GET", fileUrl, nil)
		if err != nil {
			log.Fatalf("Failed to create request: %v", err)
		}

		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", startByte, endByte))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Fatalf("Failed to do response: %v", err)
		}
		defer resp.Body.Close()

		reader := resp.Body
		if bar != nil {
			reader = bar.ProxyReader(resp.Body)
		}

		data, err := io.ReadAll(reader)
		if err != nil {
			log.Printf("Failed to read response body: %v", err)
			continue
		}

		*chunk <- data
		return
	}
}

func (d *Downloader) calculateConcurrentWorker(fileSize int64) int64 {
	if d.NumberOfChunks != 0 {
		return d.NumberOfChunks
	} else if fileSize < d.ChunkSize {
		return 1
	}

	return d.File.Size / d.ChunkSize
}
