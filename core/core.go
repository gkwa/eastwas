package core

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"text/template"

	"github.com/atotto/clipboard"
	"golang.org/x/sync/errgroup"
	"mvdan.cc/xurls/v2"
)

func Run() {
	clipText, _ := clipboard.ReadAll()
	slog.Debug("clipboard content", "text", clipText)

	urls := extractURLs(clipText)
	slog.Debug("extracted URLs", "urls", urls)

	friendlyURLs := fetchURLsConcurrently(urls)

	output := generateOutput(friendlyURLs)

	writeToClipboard(output)
	slog.Debug("friendly URLs written to clipboard")
}

func extractURLs(text string) []string {
	rxStrict := xurls.Strict()
	return rxStrict.FindAllString(text, -1)
}

func fetchURLsConcurrently(urls []string) []string {
	var friendlyURLs []string
	var mu sync.Mutex
	g, ctx := errgroup.WithContext(context.Background())
	urlChan := make(chan string, 5)
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		g.Go(func() error {
			defer wg.Done()
			return fetchURL(ctx, urlChan, &friendlyURLs, &mu)
		})
	}

	for _, url := range urls {
		select {
		case urlChan <- url:
			slog.Debug("URL sent for fetching", "url", url)
		case <-ctx.Done():
			slog.Debug("context done, exiting")
			return friendlyURLs
		}
	}
	close(urlChan)
	slog.Debug("URL channel closed")

	wg.Wait()
	if err := g.Wait(); err != nil {
		slog.Error("error waiting for goroutines", "error", err)
	}

	return friendlyURLs
}

func fetchURL(ctx context.Context, urls chan string, friendlyURLs *[]string, mu *sync.Mutex) error {
	for {
		select {
		case url, ok := <-urls:
			if !ok {
				return nil
			}
			finalURL, err := getFinalURL(url)
			if err != nil {
				slog.Error("error getting final URL", "error", err, "url", url)
				continue
			}
			slog.Debug("fetching URL", "url", finalURL)

			title, err := getPageTitle(finalURL)
			if err != nil {
				slog.Error("error getting page title", "error", err, "url", finalURL)
				continue
			}
			slog.Debug("extracted title", "title", title, "url", finalURL)

			friendlyURL := fmt.Sprintf("- [%s](%s)", title, finalURL)
			slog.Debug("creating link for URL", "link", friendlyURL)

			mu.Lock()
			*friendlyURLs = append(*friendlyURLs, friendlyURL)
			mu.Unlock()
		case <-ctx.Done():
			slog.Debug("context done, exiting goroutine")
			return ctx.Err()
		}
	}
}

func getFinalURL(urlStr string) (string, error) {
	if strings.Contains(strings.ToLower(urlStr), "https://www.google.com/url?q=") {
		u, err := url.Parse(urlStr)
		if err != nil {
			return "", err
		}
		q := u.Query()
		return q.Get("q"), nil
	}
	return urlStr, nil
}

func getPageTitle(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return "", err
	}

	respBody := buf.String()
	titleRegex := regexp.MustCompile(`<title.*?>(.*?)</title>`)
	titleMatch := titleRegex.FindStringSubmatch(respBody)
	if len(titleMatch) < 2 {
		return "", fmt.Errorf("no title found in page")
	}

	return titleMatch[1], nil
}

func generateOutput(friendlyURLs []string) string {
	tmpl := `{{range .}}
{{.}}{{end}}`
	t := template.Must(template.New("friendlyURLs").Parse(tmpl))
	var output strings.Builder
	if err := t.Execute(&output, friendlyURLs); err != nil {
		slog.Error("error executing template", "error", err)
		return ""
	}
	return output.String()
}

func writeToClipboard(output string) {
	if err := clipboard.WriteAll(output); err != nil {
		slog.Error("error writing to clipboard", "error", err)
	}
}
