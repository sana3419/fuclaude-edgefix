package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/html/charset"
)

// This proxy sits in front of fuclaude (v0.5.1) and fixes the /edge-api/ issue.
// Claude.ai moved /api/bootstrap to /edge-api/bootstrap, but fuclaude crashes
// when proxying /edge-api/bootstrap responses. Two fixes:
//
// 1. Rewrite /edge-api/* requests to /api/* (so fuclaude's auth handler processes them)
// 2. Rewrite JS responses to replace "edge-api" with "api" (so the browser uses /api/ paths)

func main() {
	upstream := "http://127.0.0.1:8182"
	listen := "0.0.0.0:8181"

	log.Printf("Fuclaude Edge-API Fix v1.0")
	log.Printf("Listening on %s, upstream: %s", listen, upstream)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		query := r.URL.RawQuery

		// Fix 1: Rewrite /edge-api/* to /api/*
		if strings.HasPrefix(path, "/edge-api/") {
			newPath := "/api/" + strings.TrimPrefix(path, "/edge-api/")
			log.Printf("Path rewrite: %s -> %s", path, newPath)
			path = newPath
		}

		targetURL := upstream + path
		if query != "" {
			targetURL += "?" + query
		}

		upReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
		if err != nil {
			http.Error(w, "Bad request", http.StatusBadGateway)
			return
		}

		for k, vs := range r.Header {
			for _, v := range vs {
				upReq.Header.Add(k, v)
			}
		}

		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		resp, err := client.Do(upReq)
		if err != nil {
			log.Printf("Upstream error: %v", err)
			http.Error(w, "Bad gateway", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		ct := resp.Header.Get("Content-Type")

		// Fix 2: For JS responses, rewrite "edge-api" -> "api" in the body
		// so the browser requests /api/* paths instead of /edge-api/*
		if shouldRewriteBody(r.URL.Path, ct) {
			rewriteJSResponse(w, resp)
			return
		}

		// Default: pass through
		copyHeaders(w, resp)
		w.WriteHeader(resp.StatusCode)
		streamBody(w, resp)
	})

	log.Fatal(http.ListenAndServe(listen, nil))
}

func shouldRewriteBody(path, contentType string) bool {
	// Rewrite JS bundles and HTML pages that might reference edge-api
	if strings.HasSuffix(path, ".js") {
		return true
	}
	if strings.Contains(contentType, "javascript") {
		return true
	}
	if strings.Contains(contentType, "text/html") {
		return true
	}
	return false
}

func rewriteJSResponse(w http.ResponseWriter, resp *http.Response) {
	// Read the full body
	var reader io.Reader = resp.Body
	encoding := resp.Header.Get("Content-Encoding")

	switch encoding {
	case "gzip":
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			copyHeaders(w, resp)
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
			return
		}
		defer gr.Close()
		reader = gr
	case "br":
		// For brotli, just pass through - we can't easily decode it
		// The browser will handle it
		copyHeaders(w, resp)
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		copyHeaders(w, resp)
		w.WriteHeader(resp.StatusCode)
		return
	}

	// Check charset
	_, name, _ := charset.DetermineEncoding(body, resp.Header.Get("Content-Type"))
	_ = name

	// Perform the replacement
	original := body
	// Replace edge-api path references with api
	// Be careful to only replace API path references, not arbitrary text
	body = bytes.ReplaceAll(body, []byte(`/edge-api/`), []byte(`/api/`))
	body = bytes.ReplaceAll(body, []byte(`"edge-api/`), []byte(`"api/`))
	body = bytes.ReplaceAll(body, []byte(`'edge-api/`), []byte(`'api/`))
	body = bytes.ReplaceAll(body, []byte(`edge-api/bootstrap`), []byte(`api/bootstrap`))

	if !bytes.Equal(original, body) {
		log.Printf("Rewrote edge-api references in %s response (%d bytes)", resp.Request.URL.Path, len(body))
	}

	// Copy headers, but fix Content-Length and remove Content-Encoding (we decoded it)
	for k, vs := range resp.Header {
		lk := strings.ToLower(k)
		if lk == "content-length" || lk == "content-encoding" {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

func copyHeaders(w http.ResponseWriter, resp *http.Response) {
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
}

func streamBody(w http.ResponseWriter, resp *http.Response) {
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		flusher, ok := w.(http.Flusher)
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				if ok {
					flusher.Flush()
				}
			}
			if err != nil {
				break
			}
		}
	} else {
		io.Copy(w, resp.Body)
	}
}
