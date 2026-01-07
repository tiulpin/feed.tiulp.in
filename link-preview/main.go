package main

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/sync/singleflight"
)

type Preview struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Image       string `json:"image"`
	SiteName    string `json:"site_name"`
	Favicon     string `json:"favicon"`
	Domain      string `json:"domain"`
	Error       string `json:"error,omitempty"`
	OriginalURL string `json:"original_url,omitempty"`
}

type CacheMetrics struct {
	PreviewHits   int64 `json:"preview_hits"`
	PreviewMisses int64 `json:"preview_misses"`
	ImageHits     int64 `json:"image_hits"`
	ImageMisses   int64 `json:"image_misses"`
	PreviewSize   int   `json:"preview_cache_size"`
	ImageSize     int   `json:"image_cache_size"`
	MemoryUsageMB int64 `json:"memory_usage_mb"`
}

type ImageCacheEntry struct {
	Data        []byte
	ContentType string
}

var (
	metaPropertyContentRe = regexp.MustCompile(`(?i)<meta[^>]+property=["']([^"']+)["'][^>]+content=["']([^"']+)["']`)
	metaContentPropertyRe = regexp.MustCompile(`(?i)<meta[^>]+content=["']([^"']+)["'][^>]+property=["']([^"']+)["']`)
	metaNameContentRe     = regexp.MustCompile(`(?i)<meta[^>]+name=["']([^"']+)["'][^>]+content=["']([^"']+)["']`)
	metaContentNameRe     = regexp.MustCompile(`(?i)<meta[^>]+content=["']([^"']+)["'][^>]+name=["']([^"']+)["']`)
	titleRe               = regexp.MustCompile(`(?i)<title[^>]*>([^<]+)</title>`)
	faviconRe             = regexp.MustCompile(`(?i)<link[^>]+rel=["'][^"']*icon[^"']*["'][^>]+href=["']([^"']+)["']`)
)

var (
	previewCache *lru.Cache[string, Preview]
	imageCache   *lru.Cache[string, ImageCacheEntry]
	requestGroup singleflight.Group
	metrics      CacheMetrics
	metricsMu    sync.RWMutex

	client = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  false,
			ForceAttemptHTTP2:   true,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}

	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"

	maxPreviewCacheEntries = 5000
	maxImageCacheEntries   = 50
	imageCacheTTL          = 5 * time.Minute
	cleanupInterval        = 5 * time.Minute
)

func init() {
	var err error

	previewCache, err = lru.New[string, Preview](maxPreviewCacheEntries)
	if err != nil {
		log.Fatal("Failed to create preview cache:", err)
	}

	imageCache, err = lru.New[string, ImageCacheEntry](maxImageCacheEntries)
	if err != nil {
		log.Fatal("Failed to create image cache:", err)
	}

	go cleanupRoutine()

	log.Printf("Initialized with limits: %d preview entries, %d image entries", maxPreviewCacheEntries, maxImageCacheEntries)
}

func cleanupRoutine() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		metricsMu.Lock()
		metrics.MemoryUsageMB = int64(m.Alloc / 1024 / 1024)
		metrics.PreviewSize = previewCache.Len()
		metrics.ImageSize = imageCache.Len()
		metricsMu.Unlock()

		log.Printf("Cache status: %d previews, %d images, %dMB memory",
			previewCache.Len(), imageCache.Len(), m.Alloc/1024/1024)
	}
}

func hashURL(u string) string {
	h := md5.Sum([]byte(u))
	return hex.EncodeToString(h[:])
}

// extractMetaTags parses HTML line-by-line and stops early when meta tags are found
func extractMetaTags(reader io.Reader, maxBytes int) (title, description, image, siteName, favicon string) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxBytes)

	var htmlBuffer strings.Builder
	var foundTitle, foundDesc, foundImage, foundSite, foundFavicon bool
	bytesRead := 0
	const maxScan = 50000

	for scanner.Scan() {
		line := scanner.Text()
		bytesRead += len(line)
		htmlBuffer.WriteString(line)
		htmlBuffer.WriteString("\n")

		if !foundTitle && (strings.Contains(line, "og:title") || strings.Contains(line, "twitter:title") || strings.Contains(line, "<title")) {
			if t := extractMetaFromBuffer(htmlBuffer.String(), "og:title"); t != "" {
				title = t
				foundTitle = true
			} else if t := extractMetaFromBuffer(htmlBuffer.String(), "twitter:title"); t != "" {
				title = t
				foundTitle = true
			} else if m := titleRe.FindStringSubmatch(htmlBuffer.String()); len(m) > 1 {
				title = strings.TrimSpace(m[1])
				foundTitle = true
			}
		}

		if !foundDesc && (strings.Contains(line, "og:description") || strings.Contains(line, "twitter:description") || strings.Contains(line, `name="description"`)) {
			if d := extractMetaFromBuffer(htmlBuffer.String(), "og:description"); d != "" {
				description = d
				foundDesc = true
			} else if d := extractMetaFromBuffer(htmlBuffer.String(), "twitter:description"); d != "" {
				description = d
				foundDesc = true
			} else if d := extractMetaFromBuffer(htmlBuffer.String(), "description"); d != "" {
				description = d
				foundDesc = true
			}
		}

		if !foundImage && (strings.Contains(line, "og:image") || strings.Contains(line, "twitter:image")) {
			if i := extractMetaFromBuffer(htmlBuffer.String(), "og:image"); i != "" {
				image = i
				foundImage = true
			} else if i := extractMetaFromBuffer(htmlBuffer.String(), "twitter:image"); i != "" {
				image = i
				foundImage = true
			}
		}

		if !foundSite && strings.Contains(line, "og:site_name") {
			if s := extractMetaFromBuffer(htmlBuffer.String(), "og:site_name"); s != "" {
				siteName = s
				foundSite = true
			}
		}

		if !foundFavicon && strings.Contains(line, "icon") {
			if m := faviconRe.FindStringSubmatch(htmlBuffer.String()); len(m) > 1 {
				favicon = strings.TrimSpace(m[1])
				foundFavicon = true
			}
		}

		if (foundTitle && foundDesc && foundImage && foundSite && foundFavicon) || bytesRead > maxScan {
			break
		}
	}

	return
}

func extractMetaFromBuffer(htmlStr, property string) string {
	if matches := metaPropertyContentRe.FindAllStringSubmatch(htmlStr, -1); len(matches) > 0 {
		for _, m := range matches {
			if len(m) > 2 && strings.EqualFold(m[1], property) {
				return strings.TrimSpace(m[2])
			}
		}
	}

	if matches := metaContentPropertyRe.FindAllStringSubmatch(htmlStr, -1); len(matches) > 0 {
		for _, m := range matches {
			if len(m) > 2 && strings.EqualFold(m[2], property) {
				return strings.TrimSpace(m[1])
			}
		}
	}

	if matches := metaNameContentRe.FindAllStringSubmatch(htmlStr, -1); len(matches) > 0 {
		for _, m := range matches {
			if len(m) > 2 && strings.EqualFold(m[1], property) {
				return strings.TrimSpace(m[2])
			}
		}
	}

	if matches := metaContentNameRe.FindAllStringSubmatch(htmlStr, -1); len(matches) > 0 {
		for _, m := range matches {
			if len(m) > 2 && strings.EqualFold(m[2], property) {
				return strings.TrimSpace(m[1])
			}
		}
	}

	return ""
}

func resolveURL(href, base string) string {
	if strings.HasPrefix(href, "http") {
		return href
	}
	if u, err := url.Parse(base); err == nil {
		if ref, err := url.Parse(href); err == nil {
			return u.ResolveReference(ref).String()
		}
	}
	return href
}

func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}

func fetchPreview(targetURL string) Preview {
	cacheKey := hashURL(targetURL)

	if cached, ok := previewCache.Get(cacheKey); ok {
		metricsMu.Lock()
		metrics.PreviewHits++
		metricsMu.Unlock()
		return cached
	}

	metricsMu.Lock()
	metrics.PreviewMisses++
	metricsMu.Unlock()

	result, err, _ := requestGroup.Do(targetURL, func() (interface{}, error) {
		return fetchPreviewInternal(targetURL)
	})

	if err != nil {
		return Preview{URL: targetURL, Error: err.Error()}
	}

	preview := result.(Preview)
	previewCache.Add(cacheKey, preview)
	return preview
}

func fetchPreviewInternal(targetURL string) (Preview, error) {
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return Preview{URL: targetURL, Error: "Invalid URL"}, err
	}

	req, _ := http.NewRequest("GET", targetURL, nil)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return Preview{URL: targetURL, Error: "Failed to fetch"}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return Preview{URL: targetURL, Error: "HTTP " + resp.Status}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	title, description, image, siteName, favicon := extractMetaTags(resp.Body, 100000)

	if title == "" {
		title = parsed.Host
	}
	title = html.UnescapeString(title)

	if description != "" {
		description = html.UnescapeString(description)
	}

	if image != "" {
		image = resolveURL(image, targetURL)
	}

	if siteName == "" {
		siteName = parsed.Host
	}

	if favicon == "" {
		favicon = parsed.Scheme + "://" + parsed.Host + "/favicon.ico"
	} else {
		favicon = resolveURL(favicon, targetURL)
	}

	preview := Preview{
		URL:         targetURL,
		Title:       truncate(title, 200),
		Description: truncate(description, 300),
		Image:       image,
		SiteName:    siteName,
		Favicon:     favicon,
		Domain:      parsed.Host,
	}

	return preview, nil
}

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		if r.Method == "OPTIONS" {
			return
		}
		next(w, r)
	}
}

func cacheHeadersMiddleware(next http.HandlerFunc, maxAge int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", maxAge))
		next(w, r)
	}
}

func handlePreview(w http.ResponseWriter, r *http.Request) {
	targetURL := r.URL.Query().Get("url")
	if targetURL == "" {
		http.Error(w, "Missing url parameter", 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(fetchPreview(targetURL))
}

func handlePreviews(w http.ResponseWriter, r *http.Request) {
	urls := r.URL.Query()["url"]
	if len(urls) == 0 {
		http.Error(w, "Missing url parameter", 400)
		return
	}
	if len(urls) > 20 {
		http.Error(w, "Maximum 20 URLs", 400)
		return
	}

	results := make([]Preview, len(urls))
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Add(1)
		go func(idx int, targetURL string) {
			defer wg.Done()
			results[idx] = fetchPreview(targetURL)
		}(i, u)
	}
	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func handleProxyImage(w http.ResponseWriter, r *http.Request) {
	imageURL := r.URL.Query().Get("url")
	if imageURL == "" {
		http.Error(w, "Missing url parameter", 400)
		return
	}

	cacheKey := "img_" + hashURL(imageURL)

	if cached, ok := imageCache.Get(cacheKey); ok {
		metricsMu.Lock()
		metrics.ImageHits++
		metricsMu.Unlock()

		w.Header().Set("Content-Type", cached.ContentType)
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(imageCacheTTL.Seconds())))
		w.Write(cached.Data)
		return
	}

	metricsMu.Lock()
	metrics.ImageMisses++
	metricsMu.Unlock()

	req, _ := http.NewRequest("GET", imageURL, nil)
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to fetch image", 500)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		http.Error(w, "Image not found", resp.StatusCode)
		return
	}

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg"
	}

	// Only cache smaller images to save memory
	if len(data) < 500*1024 {
		imageCache.Add(cacheKey, ImageCacheEntry{
			Data:        data,
			ContentType: contentType,
		})
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(imageCacheTTL.Seconds())))
	w.Write(data)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	metricsMu.RLock()
	m := metrics
	metricsMu.RUnlock()

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	m.MemoryUsageMB = int64(memStats.Alloc / 1024 / 1024)
	m.PreviewSize = previewCache.Len()
	m.ImageSize = imageCache.Len()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}

func main() {
	http.HandleFunc("/preview", corsMiddleware(cacheHeadersMiddleware(handlePreview, 3600)))
	http.HandleFunc("/previews", corsMiddleware(cacheHeadersMiddleware(handlePreviews, 3600)))
	http.HandleFunc("/proxy-image", corsMiddleware(handleProxyImage))
	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/metrics", handleMetrics)

	log.Println("Link preview service starting on :5000")
	log.Printf("Memory limits: %d preview entries (~10MB), %d image entries (~20MB)",
		maxPreviewCacheEntries, maxImageCacheEntries)
	log.Fatal(http.ListenAndServe(":5000", nil))
}
