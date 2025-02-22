package client

import (
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"time"
	"compress/gzip"
)

const (
	timeout = 30 * time.Second
)

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:89.0) Gecko/20100101 Firefox/89.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.1.1 Safari/605.1.15",
}

// CreateProxyHTTPClient creates an HTTP client with proxy support
func CreateProxyHTTPClient(proxyURL string) *http.Client {
	if proxyURL == "" {
		return CreateHTTPClient("")
	}

	proxy, err := url.Parse(proxyURL)
	if err != nil {
		return CreateHTTPClient("")
	}

	transport := &http.Transport{
		Proxy:           http.ProxyURL(proxy),
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
		},
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		MaxIdleConnsPerHost: 10,
		ForceAttemptHTTP2:   true,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}

// CreateHTTPClient creates a standard HTTP client
func CreateHTTPClient(proxyURL string) *http.Client {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
		},
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		MaxIdleConnsPerHost: 10,
		ForceAttemptHTTP2:   true,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}

// GetRandomHeaders returns a set of randomized HTTP headers that closely mimic a real browser
func GetRandomHeaders() http.Header {
	headers := http.Header{}
	
	// Get random user agent
	userAgent := userAgents[rand.Intn(len(userAgents))]
	headers.Set("User-Agent", userAgent)
	
	// Common headers
	headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	headers.Set("Accept-Language", "en-US,en;q=0.9")
	headers.Set("Accept-Encoding", "gzip, deflate, br")
	headers.Set("Connection", "keep-alive")
	headers.Set("DNT", "1")
	headers.Set("Upgrade-Insecure-Requests", "1")
	
	// Add random viewport and screen dimensions
	width := 1200 + rand.Intn(400)
	height := 800 + rand.Intn(400)
	headers.Set("Viewport-Width", fmt.Sprintf("%d", width))
	headers.Set("Sec-CH-UA-Platform", `"Windows"`)
	headers.Set("Sec-CH-UA", `"Google Chrome";v="91", "Chromium";v="91"`)
	headers.Set("Sec-CH-UA-Mobile", "?0")
	headers.Set("Sec-Fetch-Dest", "document")
	headers.Set("Sec-Fetch-Mode", "navigate")
	headers.Set("Sec-Fetch-Site", "none")
	headers.Set("Sec-Fetch-User", "?1")
	headers.Set("Device-Memory", "8")
	headers.Set("DPR", "2")
	headers.Set("Viewport-Height", fmt.Sprintf("%d", height))

	return headers
}

// ReadResponseBody reads the response body, handling gzip compression if necessary
func ReadResponseBody(resp *http.Response) ([]byte, error) {
	var reader io.ReadCloser
	var err error

	switch resp.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err = gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %v", err)
		}
		defer reader.Close()
	default:
		reader = resp.Body
	}

	return io.ReadAll(reader)
} 