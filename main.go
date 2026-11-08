package main

// --------------------------------------------------
// Imports and the like
// --------------------------------------------------

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"
)

// --------------------------------------------------
// JSON shi
// --------------------------------------------------

type URLItem struct {
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Links     []string `json:"links"` // ALL valid normalized outbound links on the page
	ScrapedAt int64    `json:"scrapedAt"`
}

// --------------------------------------------------
// The actual main code stuff
// --------------------------------------------------

var httpClient = &http.Client{
	Timeout: 15 * time.Second,
}

// backlinks[target] = list of source URLs that link to it.
// Rebuilt on startup from urls.jsonl; kept in memory while crawling.
var backlinks = map[string][]string{}

// If these filenames already exist as constants elsewhere in the project,
// delete these two and use yours instead.
const (
	urlsFile      = "urls.jsonl"
	backlinksFile = "backlinks.json"
)

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// loadBacklinks rebuilds the reverse index from every URLItem already on disk.
// Safe to call on a missing file (first run).
func loadBacklinks() {
	f, err := os.Open(urlsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		printl("failed to open urls for backlink rebuild: " + err.Error())
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024) // long lines
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item URLItem
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			// Skip bad line, keep going — one corrupt append shouldn't
			// take down the whole rebuild.
			continue
		}
		for _, target := range item.Links {
			backlinks[target] = appendUnique(backlinks[target], item.URL)
		}
	}
	if err := scanner.Err(); err != nil {
		printl("error while rebuilding backlinks: " + err.Error())
	}
}

// saveBacklinks writes a full snapshot atomically (temp file + rename),
// so a crash mid-write never leaves a half-written index behind.
func saveBacklinks(bl map[string][]string) {
	data, err := json.MarshalIndent(bl, "", "  ")
	if err != nil {
		printl("failed to marshal backlinks: " + err.Error())
		return
	}
	tmp := backlinksFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		printl("failed to write backlinks temp: " + err.Error())
		return
	}
	if err := os.Rename(tmp, backlinksFile); err != nil {
		printl("failed to rename backlinks file: " + err.Error())
	}
}

func main() {
	printHeader()

	initDB()
	loadProgress()
	loadBacklinks()

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

		// 1. Normalize once, keep ALL valid outbound links (dedup per page).
		allLinks := make([]string, 0, len(foundURLs))
		seen := make(map[string]struct{}, len(foundURLs))
		for _, raw := range foundURLs {
			link, ok := normalizeURL(raw)
			if !ok {
				continue
			}
			if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
				continue
			}
			if _, dup := seen[link]; dup {
				continue
			}
			seen[link] = struct{}{}
			allLinks = append(allLinks, link)
		}

		// 2. Record backlinks (this page -> every target it linked to).
		//    Note: this includes links we won't actually crawl, which is
		//    what you want — "all pages it links to" is a graph fact,
		//    not a crawl-budget decision.
		for _, link := range allLinks {
			backlinks[link] = appendUnique(backlinks[link], URL)
		}

		// 3. Queue only the *new* links. Blacklist/self-link filters
		//    apply to the crawl, not to the persisted link graph.
		for _, link := range allLinks {
			if slices.Contains(doneURLs, link) || slices.Contains(URLs, link) {
				continue
			}
			if isBlacklisted(link) || strings.Contains(link, URL) {
				continue
			}
			URLs = append(URLs, link)
		}

		doneURLs = append(doneURLs, URL)

		if err := saveUrlItem(URLItem{
			Title:     title,
			URL:       URL,
			Links:     allLinks, // ALL outbound links now, not just the queued subset
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
			saveBacklinks(backlinks)
			lastSave = time.Now()
		}

		sleep(200)
	}

	clear()
	printStats()
	saveProgress(doneURLs, URLs)
	saveBacklinks(backlinks)
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
