// Package main implements a CLI client for the URL shortener example.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage:")
		fmt.Println("  url-client shorten <url>")
		fmt.Println("  url-client resolve <code>")
		os.Exit(1)
	}

	apiURL := os.Getenv("API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8083"
	}

	command := os.Args[1]

	switch command {
	case "shorten":
		if len(os.Args) < 3 {
			log.Fatal("Missing URL argument")
		}
		url := os.Args[2]
		shorten(apiURL, url)

	case "resolve":
		if len(os.Args) < 3 {
			log.Fatal("Missing code argument")
		}
		code := os.Args[2]
		resolve(apiURL, code)

	default:
		log.Fatalf("Unknown command: %s", command)
	}
}

func shorten(apiURL, url string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reqURL := fmt.Sprintf("%s/api.v1.API/Shorten?url=%s", apiURL, url)
	req, _ := http.NewRequestWithContext(ctx, "POST", reqURL, nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Fatalf("API returned %d: %s", resp.StatusCode, body)
	}

	var result struct {
		ShortCode string `json:"short_code"`
		URL       string `json:"url"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Fatalf("Failed to parse response: %v", err)
	}

	fmt.Printf("✓ Shortened URL\n")
	fmt.Printf("  Original: %s\n", result.URL)
	fmt.Printf("  Code:     %s\n", result.ShortCode)
	fmt.Printf("  Resolve:  %s/api.v1.API/Resolve?code=%s\n", apiURL, result.ShortCode)
}

func resolve(apiURL, code string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reqURL := fmt.Sprintf("%s/api.v1.API/Resolve?code=%s", apiURL, code)
	req, _ := http.NewRequestWithContext(ctx, "GET", reqURL, nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Fatalf("API returned %d: %s", resp.StatusCode, body)
	}

	var result struct {
		URL string `json:"url"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Fatalf("Failed to parse response: %v", err)
	}

	fmt.Printf("✓ Resolved short code: %s\n", code)
	fmt.Printf("  URL: %s\n", result.URL)
}
