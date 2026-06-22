package service

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

var imageGenerationMetrics imageGenerationPrometheusMetrics

var imageGenerationDurationBuckets = [...]float64{1, 2, 5, 10, 20, 30, 45, 60, 90, 120, 180, 300, 600, 900}

type imageGenerationPrometheusMetrics struct {
	activeRequests        atomic.Int64
	activeImages          atomic.Int64
	requestsTotal         atomic.Int64
	imagesRequested       atomic.Int64
	imagesCompleted       atomic.Int64
	errorsTotal           atomic.Int64
	durationSecondsSum    atomic.Uint64
	durationBuckets       [len(imageGenerationDurationBuckets) + 1]atomic.Int64
	durationCount         atomic.Int64
	imageDurationSum      atomic.Uint64
	imageDurationBuckets  [len(imageGenerationDurationBuckets) + 1]atomic.Int64
	imageDurationCount    atomic.Int64
	splitRequestsTotal    atomic.Int64
	splitImagesTotal      atomic.Int64
	partialRequestsTotal  atomic.Int64
	partialImagesReturned atomic.Int64
}

func StartImageGenerationMetrics(requestedImages int) func(completedImages int, failed bool, imageDurationObserved bool) {
	if requestedImages <= 0 {
		requestedImages = 1
	}
	startedAt := time.Now()
	imageGenerationMetrics.activeRequests.Add(1)
	imageGenerationMetrics.activeImages.Add(int64(requestedImages))
	imageGenerationMetrics.requestsTotal.Add(1)
	imageGenerationMetrics.imagesRequested.Add(int64(requestedImages))

	var finished atomic.Bool
	return func(completedImages int, failed bool, imageDurationObserved bool) {
		if !finished.CompareAndSwap(false, true) {
			return
		}
		durationSeconds := time.Since(startedAt).Seconds()
		imageGenerationMetrics.activeRequests.Add(-1)
		imageGenerationMetrics.activeImages.Add(-int64(requestedImages))
		if completedImages > 0 {
			imageGenerationMetrics.imagesCompleted.Add(int64(completedImages))
			if !imageDurationObserved {
				for i := 0; i < completedImages; i++ {
					ObserveImageGenerationSingleImageDuration(durationSeconds)
				}
			}
		}
		if failed {
			imageGenerationMetrics.errorsTotal.Add(1)
		}
		addAtomicFloat64(&imageGenerationMetrics.durationSecondsSum, durationSeconds)
		imageGenerationMetrics.durationBuckets[imageGenerationDurationBucketIndex(durationSeconds)].Add(1)
		imageGenerationMetrics.durationCount.Add(1)
	}
}

func ObserveImageGenerationSingleImageDuration(durationSeconds float64) {
	if durationSeconds < 0 {
		return
	}
	addAtomicFloat64(&imageGenerationMetrics.imageDurationSum, durationSeconds)
	imageGenerationMetrics.imageDurationBuckets[imageGenerationDurationBucketIndex(durationSeconds)].Add(1)
	imageGenerationMetrics.imageDurationCount.Add(1)
}

func RecordImageGenerationSplitStorage(requestedImages int) {
	if requestedImages <= 0 {
		requestedImages = 1
	}
	imageGenerationMetrics.splitRequestsTotal.Add(1)
	imageGenerationMetrics.splitImagesTotal.Add(int64(requestedImages))
}

func RecordImageGenerationPartialSuccess(returnedImages int) {
	if returnedImages <= 0 {
		return
	}
	imageGenerationMetrics.partialRequestsTotal.Add(1)
	imageGenerationMetrics.partialImagesReturned.Add(int64(returnedImages))
}

func PrometheusMetricsText() string {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	var b strings.Builder
	writeGauge(&b, "sub2api_image_generation_active_requests", "Current active image generation requests.", imageGenerationMetrics.activeRequests.Load())
	writeGauge(&b, "sub2api_image_generation_active_images_requested", "Current requested image count across active image generation requests.", imageGenerationMetrics.activeImages.Load())
	writeCounter(&b, "sub2api_image_generation_requests_total", "Total image generation requests.", imageGenerationMetrics.requestsTotal.Load())
	writeCounter(&b, "sub2api_image_generation_images_requested_total", "Total requested images from image generation requests.", imageGenerationMetrics.imagesRequested.Load())
	writeCounter(&b, "sub2api_image_generation_images_completed_total", "Total completed image outputs from image generation requests.", imageGenerationMetrics.imagesCompleted.Load())
	writeCounter(&b, "sub2api_image_generation_errors_total", "Total failed image generation requests.", imageGenerationMetrics.errorsTotal.Load())
	writeCounter(&b, "sub2api_image_generation_split_storage_requests_total", "Total multi-image requests split into sequential storage uploads.", imageGenerationMetrics.splitRequestsTotal.Load())
	writeCounter(&b, "sub2api_image_generation_split_storage_images_total", "Total images requested through split storage mode.", imageGenerationMetrics.splitImagesTotal.Load())
	writeCounter(&b, "sub2api_image_generation_partial_success_requests_total", "Total split image requests that returned a partial success response.", imageGenerationMetrics.partialRequestsTotal.Load())
	writeCounter(&b, "sub2api_image_generation_partial_success_images_returned_total", "Total images returned by partial success responses.", imageGenerationMetrics.partialImagesReturned.Load())
	writeDurationHistogram(&b, "sub2api_image_generation_duration_seconds", "Image generation request duration seconds.", &imageGenerationMetrics.durationBuckets, &imageGenerationMetrics.durationSecondsSum, &imageGenerationMetrics.durationCount)
	writeDurationHistogram(&b, "sub2api_image_generation_single_image_duration_seconds", "Single image end-to-end generation duration seconds.", &imageGenerationMetrics.imageDurationBuckets, &imageGenerationMetrics.imageDurationSum, &imageGenerationMetrics.imageDurationCount)
	writeGauge(&b, "sub2api_process_memory_alloc_bytes", "Current Go allocated heap bytes.", int64(mem.Alloc))
	writeGauge(&b, "sub2api_process_memory_heap_alloc_bytes", "Current Go heap allocated bytes.", int64(mem.HeapAlloc))
	writeGauge(&b, "sub2api_process_memory_heap_inuse_bytes", "Current Go heap in-use bytes.", int64(mem.HeapInuse))
	writeGauge(&b, "sub2api_process_memory_sys_bytes", "Total bytes obtained from the OS by Go runtime.", int64(mem.Sys))
	writeCounter(&b, "sub2api_process_memory_gc_total", "Total completed Go GC cycles.", int64(mem.NumGC))
	return b.String()
}

func writeGauge(b *strings.Builder, name string, help string, value int64) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, help, name, name, value)
}

func writeGaugeFloat(b *strings.Builder, name string, help string, value float64) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s gauge\n%s %.6f\n", name, help, name, name, value)
}

func writeCounter(b *strings.Builder, name string, help string, value int64) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, value)
}

func writeDurationHistogram(b *strings.Builder, name string, help string, buckets *[len(imageGenerationDurationBuckets) + 1]atomic.Int64, sum *atomic.Uint64, count *atomic.Int64) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	cumulative := int64(0)
	for i, bucket := range imageGenerationDurationBuckets {
		cumulative += buckets[i].Load()
		fmt.Fprintf(b, "%s_bucket{le=\"%.0f\"} %d\n", name, bucket, cumulative)
	}
	cumulative += buckets[len(imageGenerationDurationBuckets)].Load()
	fmt.Fprintf(b, "%s_bucket{le=\"+Inf\"} %d\n", name, cumulative)
	fmt.Fprintf(b, "%s_sum %.6f\n", name, math.Float64frombits(sum.Load()))
	fmt.Fprintf(b, "%s_count %d\n", name, count.Load())
}

func imageGenerationDurationBucketIndex(durationSeconds float64) int {
	for i, bucket := range imageGenerationDurationBuckets {
		if durationSeconds <= bucket {
			return i
		}
	}
	return len(imageGenerationDurationBuckets)
}

func addAtomicFloat64(target *atomic.Uint64, delta float64) {
	for {
		oldBits := target.Load()
		oldValue := math.Float64frombits(oldBits)
		newBits := math.Float64bits(oldValue + delta)
		if target.CompareAndSwap(oldBits, newBits) {
			return
		}
	}
}
