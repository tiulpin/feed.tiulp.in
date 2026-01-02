package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
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

type CacheEntry struct {
	Data      interface{}
	ExpiresAt time.Time
}

var (
	cache     = make(map[string]CacheEntry)
	cacheMu   sync.RWMutex
	cacheTTL  = time.Hour
	client    = &http.Client{Timeout: 10 * time.Second}
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"
)

func getCache(key string) (interface{}, bool) {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	if entry, ok := cache[key]; ok && time.Now().Before(entry.ExpiresAt) {
		return entry.Data, true
	}
	return nil, false
}

func setCache(key string, data interface{}) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cache[key] = CacheEntry{Data: data, ExpiresAt: time.Now().Add(cacheTTL)}
}

func hashURL(u string) string {
	h := md5.Sum([]byte(u))
	return hex.EncodeToString(h[:])
}

func extractMeta(html, property string) string {
	patterns := []string{
		`<meta[^>]+property=["']` + property + `["'][^>]+content=["']([^"']+)["']`,
		`<meta[^>]+content=["']([^"']+)["'][^>]+property=["']` + property + `["']`,
		`<meta[^>]+name=["']` + property + `["'][^>]+content=["']([^"']+)["']`,
		`<meta[^>]+content=["']([^"']+)["'][^>]+name=["']` + property + `["']`,
	}
	for _, p := range patterns {
		re := regexp.MustCompile("(?i)" + p)
		if m := re.FindStringSubmatch(html); len(m) > 1 {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

func extractTitle(html string) string {
	re := regexp.MustCompile(`(?i)<title[^>]*>([^<]+)</title>`)
	if m := re.FindStringSubmatch(html); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func extractFavicon(html, baseURL string) string {
	re := regexp.MustCompile(`(?i)<link[^>]+rel=["'][^"']*icon[^"']*["'][^>]+href=["']([^"']+)["']`)
	if m := re.FindStringSubmatch(html); len(m) > 1 {
		return resolveURL(m[1], baseURL)
	}
	// Fallback to /favicon.ico
	if u, err := url.Parse(baseURL); err == nil {
		return u.Scheme + "://" + u.Host + "/favicon.ico"
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
	if cached, ok := getCache(cacheKey); ok {
		return cached.(Preview)
	}

	parsed, err := url.Parse(targetURL)
	if err != nil {
		return Preview{URL: targetURL, Error: "Invalid URL"}
	}

	req, _ := http.NewRequest("GET", targetURL, nil)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return Preview{URL: targetURL, Error: "Failed to fetch"}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return Preview{URL: targetURL, Error: "HTTP " + resp.Status}
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 100000))
	pageHTML := string(body)

	title := extractMeta(pageHTML, "og:title")
	if title == "" {
		title = extractMeta(pageHTML, "twitter:title")
	}
	if title == "" {
		title = extractTitle(pageHTML)
	}
	if title == "" {
		title = parsed.Host
	}
	title = html.UnescapeString(title)

	description := extractMeta(pageHTML, "og:description")
	if description == "" {
		description = extractMeta(pageHTML, "twitter:description")
	}
	if description == "" {
		description = extractMeta(pageHTML, "description")
	}
	description = html.UnescapeString(description)

	image := extractMeta(pageHTML, "og:image")
	if image == "" {
		image = extractMeta(pageHTML, "twitter:image")
	}
	if image != "" {
		image = resolveURL(image, targetURL)
	}

	siteName := extractMeta(pageHTML, "og:site_name")
	if siteName == "" {
		siteName = parsed.Host
	}

	preview := Preview{
		URL:         targetURL,
		Title:       truncate(title, 200),
		Description: truncate(description, 300),
		Image:       image,
		SiteName:    siteName,
		Favicon:     extractFavicon(pageHTML, targetURL),
		Domain:      parsed.Host,
	}

	setCache(cacheKey, preview)
	return preview
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
	if cached, ok := getCache(cacheKey); ok {
		entry := cached.(map[string]interface{})
		w.Header().Set("Content-Type", entry["type"].(string))
		w.Write(entry["data"].([]byte))
		return
	}

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

	setCache(cacheKey, map[string]interface{}{"data": data, "type": contentType})

	w.Header().Set("Content-Type", contentType)
	w.Write(data)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func main() {
	http.HandleFunc("/preview", corsMiddleware(handlePreview))
	http.HandleFunc("/previews", corsMiddleware(handlePreviews))
	http.HandleFunc("/proxy-image", corsMiddleware(handleProxyImage))
	http.HandleFunc("/health", handleHealth)

	log.Println("Link preview service starting on :5000")
	log.Fatal(http.ListenAndServe(":5000", nil))
}
