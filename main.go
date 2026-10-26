package main

// --------------------------------------------------
// Imports and the like
// --------------------------------------------------

import (
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// --------------------------------------------------
// JSON shi
// --------------------------------------------------

type URLItem struct {
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Links     []string `json:"links"`
	ScrapedAt int64    `json:"scrapedAt"`
}

// --------------------------------------------------
// The actual main code stuff
// --------------------------------------------------

var httpClient = &http.Client{
	Timeout: 15 * time.Second,
}

func main() {
	printHeader()

	initDB()
	loadProgress()

	// Only seed on a truly fresh run. On resume, the queue comes from disk;
	// on a completed crawl both are empty and there's nothing to do.
	if len(URLs) == 0 && len(doneURLs) == 0 {
		URLs = []string{seed}
	}

	lastSave := time.Now()

	for !stop.Load() && len(URLs) > 0 {
		URL := URLs[0]
		URLs = URLs[1:]

		if !strings.HasPrefix(URL, "http://") && !strings.HasPrefix(URL, "https://") {
			continue
		}
		if slices.Contains(doneURLs, URL) {
			continue
		}

		title, foundURLs, err := Scrape(URL)
		if err {
			print("error scraping: " + URL)
			doneURLs = append(doneURLs, URL)
			continue
		}

		// Build a fresh slice of only the links we queued.
		// Do NOT mutate foundURLs while ranging over it.
		newLinks := []string{}
		for _, link := range foundURLs {
			link, ok := normalizeURL(link)
			if !ok {
				continue
			}
			if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
				continue
			}
			if slices.Contains(doneURLs, link) || slices.Contains(URLs, link) {
				continue
			}
			if isBlacklisted(link) || strings.Contains(link, URL) {
				continue
			}
			URLs = append(URLs, link)
			newLinks = append(newLinks, link)
		}

		doneURLs = append(doneURLs, URL)

		if err := saveUrlItem(URLItem{
			Title:     title,
			URL:       URL,
			Links:     newLinks,
			ScrapedAt: time.Now().Unix(),
		}); err != nil {
			// One bad append isn't fatal — log it and keep crawling.
			printl("failed to save URLItem: " + err.Error())
		}

		clear()
		printStats()

		// Persist crawl state periodically so a SIGKILL loses at most
		// the last window of work. urls.jsonl already survives line-by-line.
		if time.Since(lastSave) > 30*time.Second {
			saveProgress(doneURLs, URLs)
			lastSave = time.Now()
		}

		sleep(200)
	}

	clear()
	printStats()
	saveProgress(doneURLs, URLs)
}

// intercept ctrl + c
func init() { // signal handler
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		stop.Store(true)
		// Restore default signal handling so a second Ctrl+C actually
		// kills the process if it's stuck in a long network call.
		signal.Stop(c)
	}()
}

func Scrape(URL string) (string, []string, bool) {
	if !strings.HasPrefix(URL, "http://") && !strings.HasPrefix(URL, "https://") {
		print("Error: this URL doesnt start with http(s): " + URL)
		return "", []string{}, true
	}

	base, err := url.Parse(URL)
	if err != nil {
		print("url.Parse: " + err.Error())
		return "", []string{}, true
	}

	res, err := httpClient.Get(URL)
	if err != nil {
		print(err.Error())
		return "", []string{}, true
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		print("wierd statuscode: " + strconv.Itoa(res.StatusCode) + " " + URL)
		return "", []string{}, true
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		print(err.Error())
		return "", []string{}, true
	}

	title := doc.Find("title").First().Text()

	seen := map[string]bool{}
	var foundURLs []string

	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists {
			return
		}

		ref, err := url.Parse(href)
		if err != nil {
			return
		}

		resolved := base.ResolveReference(ref)
		if resolved.Scheme != "http" && resolved.Scheme != "https" {
			return
		}

		resolved.Fragment = ""
		link, ok := normalizeURL(resolved.String())
		if !ok {
			return
		}

		if !seen[link] {
			seen[link] = true
			foundURLs = append(foundURLs, link)
		}
	})

	return title, foundURLs, false
}
