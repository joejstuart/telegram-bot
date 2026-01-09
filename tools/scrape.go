package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	scrapeTimeout  = 30 * time.Second
	maxContentLen  = 50000 // Max chars to send to summarizer
	scrapeLogPrefix = "[scrape]"
)

// ScrapeTool fetches web pages, extracts main content, and summarizes them.
type ScrapeTool struct {
	ollamaURL   string
	ollamaModel string
	httpClient  *http.Client
}

// NewScrapeTool creates a new scrape tool.
func NewScrapeTool(ollamaURL, ollamaModel string) *ScrapeTool {
	return &ScrapeTool{
		ollamaURL:   ollamaURL,
		ollamaModel: ollamaModel,
		httpClient: &http.Client{
			Timeout: scrapeTimeout,
		},
	}
}

func (s *ScrapeTool) Name() string {
	return "scrape"
}

func (s *ScrapeTool) Description() string {
	return `Scrape a website and summarize its main content.

Input: A URL
Output: A concise summary of the main topics/ideas on the page

Use this to quickly understand what a webpage is about without reading the whole thing.`
}

func (s *ScrapeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL of the webpage to scrape and summarize",
			},
		},
		"required": []string{"url"},
	}
}

func (s *ScrapeTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	url, ok := args["url"].(string)
	if !ok || url == "" {
		return "", fmt.Errorf("url is required")
	}

	// Ensure URL has scheme
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}

	log.Printf("%s fetching %s", scrapeLogPrefix, url)

	// Fetch the page
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; telegram-bot/1.0)")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Read body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	log.Printf("%s fetched %d bytes", scrapeLogPrefix, len(body))

	// Extract text content
	text := s.extractText(string(body))
	if text == "" {
		return "Could not extract text content from the page.", nil
	}

	log.Printf("%s extracted %d chars of text", scrapeLogPrefix, len(text))

	// Truncate if too long
	if len(text) > maxContentLen {
		text = text[:maxContentLen] + "..."
	}

	// Summarize using Ollama
	summary, err := s.summarize(ctx, text, url)
	if err != nil {
		log.Printf("%s summarization failed: %v", scrapeLogPrefix, err)
		// Return extracted text if summarization fails
		return fmt.Sprintf("Failed to summarize, here's the extracted text:\n\n%s", truncateText(text, 2000)), nil
	}

	log.Printf("%s summary: %s", scrapeLogPrefix, truncateText(summary, 100))
	return summary, nil
}

func (s *ScrapeTool) extractText(htmlContent string) string {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		// Fallback: strip HTML tags with regex
		return s.stripTags(htmlContent)
	}

	var textBuilder strings.Builder
	s.extractTextFromNode(doc, &textBuilder)

	// Clean up whitespace
	text := textBuilder.String()
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	return text
}

func (s *ScrapeTool) extractTextFromNode(n *html.Node, sb *strings.Builder) {
	// Skip script, style, nav, footer, header elements
	if n.Type == html.ElementNode {
		switch n.Data {
		case "script", "style", "nav", "footer", "header", "aside", "noscript":
			return
		}
	}

	if n.Type == html.TextNode {
		text := strings.TrimSpace(n.Data)
		if text != "" {
			sb.WriteString(text)
			sb.WriteString(" ")
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		s.extractTextFromNode(c, sb)
	}
}

func (s *ScrapeTool) stripTags(html string) string {
	// Simple regex fallback
	re := regexp.MustCompile(`<[^>]*>`)
	text := re.ReplaceAllString(html, " ")
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

func (s *ScrapeTool) summarize(ctx context.Context, text, url string) (string, error) {
	prompt := fmt.Sprintf(`Summarize the main topics and ideas from this webpage in 2-3 concise bullet points.

URL: %s

Content:
%s

Provide only the summary, no preamble:`, url, text)

	reqBody := map[string]any{
		"model":  s.ollamaModel,
		"prompt": prompt,
		"stream": false,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	// Use generate endpoint for simple completion
	generateURL := strings.Replace(s.ollamaURL, "/api/chat", "/api/generate", 1)
	
	req, err := http.NewRequestWithContext(ctx, "POST", generateURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling Ollama: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Ollama error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	return strings.TrimSpace(result.Response), nil
}

func truncateText(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
