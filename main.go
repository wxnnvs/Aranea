package main

// --------------------------------------------------
// Imports and the like
// --------------------------------------------------

import (
	"bufio"
	"encoding/json"
	"fmt"
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

// URLItem is one line in urls.jsonl. Each line captures a single crawled
// page: its title, its URL, every normalized outbound link on that page,
// and when it was scraped. Links is authoritative, it records the whole
// outbound graph of the page, not just the subset the crawler chose to
// enqueue.
type URLItem struct {
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Links     []string `json:"links"` // ALL valid normalized outbound links on the page
	ScrapedAt int64    `json:"scrapedAt"`
}

// --------------------------------------------------
// The actual main code stuff
// --------------------------------------------------

// httpClient is the shared HTTP client for every scrape request. A global
// timeout prevents a single slow server from stalling the crawl forever.
var httpClient = &http.Client{
	Timeout: 15 * time.Second,
}

// backlinks[target] = list of source URLs that link to it.
// Rebuilt on startup from urls.jsonl; kept in memory while crawling.
// This is the reverse index of the link graph, the same structure that
// pagerank.go consumes via ReadBacklinksData.
var backlinks = map[string][]string{}

// appendUnique returns s with v appended, but only if v wasn't already
// present. Used to build backlink lists without duplicates (a page
// linking twice to the same target only counts once).
func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// loadBacklinks rebuilds the reverse index from every URLItem already on
// disk. Called once at startup so a resumed crawl has the full backlink
// graph in memory without re-fetching anything.
//
// Safe to call on a missing file (first run): returns silently.
// Corrupt lines are skipped, one bad append shouldn't bring down the
// whole rebuild.
func loadBacklinks() {
	f, err := os.Open(urlsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		printl("failed to open urls for backlink rebuild: " + err.Error())
		return
	}
	defer f.Close()

	// urls.jsonl can have very long lines (pages with hundreds of links).
	// Bump the scanner's buffer ceiling to 16 MB so those lines aren't
	// silently truncated.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item URLItem
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			// Skip the bad line; keep going.
			continue
		}
		// Invert item.Links: for each target the page linked to, add
		// this page as one of the target's backlink sources.
		for _, target := range item.Links {
			backlinks[target] = appendUnique(backlinks[target], item.URL)
		}
	}
	if err := scanner.Err(); err != nil {
		printl("error while rebuilding backlinks: " + err.Error())
	}
}

// saveBacklinks writes a full snapshot of the in-memory backlink map to
// disk atomically: write to a temp file, then rename over the real file.
// A crash mid-write never leaves a half-written index behind, the old
// file stays valid until the new one is complete.
func saveBacklinks(bl map[string][]string) {
	data, err := json.MarshalIndent(bl, "", "  ")
	if err != nil {
		printl("failed to marshal backlinks: " + err.Error())
		return
	}
	tmp := backlinksPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		printl("failed to write backlinks temp: " + err.Error())
		return
	}
	if err := os.Rename(tmp, backlinksPath); err != nil {
		printl("failed to rename backlinks file: " + err.Error())
	}
}

// main drives the crawl. High-level flow:
//
//  1. Startup: load the on-disk state (DB, progress, backlink index).
//  2. Seed: if both the queue and done-list are empty, treat this as a
//     fresh run and seed the queue with the initial URL.
//  3. Loop: pop a URL, scrape it, record its outbound links as backlinks,
//     enqueue any new URLs, persist the page, sleep, repeat.
//  4. Shutdown: save final state and print PageRank results.
//
// The loop exits when stop is set (Ctrl+C) or the queue empties.
func main() {
	printHeader()

	// Restore crawl state from disk. initDB opens the persistence layer,
	// loadProgress reads doneURLs and URLs, loadBacklinks rebuilds the
	// reverse index from urls.jsonl so we don't lose it on restart.
	initDB()
	loadProgress()
	loadBacklinks()

	// Only seed on a truly fresh run. On resume, the queue comes from disk;
	// on a completed crawl both are empty and there's nothing to do.
	if len(URLs) == 0 && len(doneURLs) == 0 {
		URLs = []string{seed}
	}

	// Throttle periodic persistence so slow disk I/O doesn't dominate the
	// crawl. urls.jsonl itself is appended line-by-line and survives crashes
	// on its own; this timer only controls when the queue and backlink
	// snapshot are flushed.
	lastSave := time.Now()

	for !stop.Load() && len(URLs) > 0 {
		// Pop the next URL from the front of the queue.
		URL := URLs[0]
		URLs = URLs[1:]

		// Re-normalize on dequeue, URLs can enter the queue from many
		// sources (seed, scraped links, resumes) and we want a single
		// canonical form before deduplication.
		normalized, ok := normalizeURL(URL)
		if !ok {
			continue
		}
		URL = normalized

		// Skip anything that isn't HTTP(S). normalizeURL may pass odd
		// schemes through, so re-check here.
		if !strings.HasPrefix(URL, "http://") && !strings.HasPrefix(URL, "https://") {
			continue
		}
		// Skip pages we've already processed.
		if slices.Contains(doneURLs, URL) {
			continue
		}

		// Fetch and parse the page. On failure, mark it done so we don't
		// retry a broken URL forever, and move on.
		title, foundURLs, err := Scrape(URL)
		if err {
			print("error scraping: " + URL)
			doneURLs = append(doneURLs, URL)
			continue
		}

		// 1. Normalize every outbound link and deduplicate per page.
		//    The resulting allLinks slice is the page's *complete* outbound
		//    edge set, not the subset we choose to crawl.
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

		// 2. Record backlinks. This is a graph fact, every page this
		//    page linked to gets us added as a source, whether or not
		//    that target will ever be crawled. PageRank depends on this
		//    being complete.
		for _, link := range allLinks {
			backlinks[link] = appendUnique(backlinks[link], URL)
		}

		// 3. Queue only the *new* links. Blacklist and self-link filters
		//    apply to the crawl, not to the persisted link graph: we want
		//    to record every edge but not follow every edge.
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

		// Persist this page's data. Each line is appended immediately so
		// a crash loses at most the page currently being processed.
		if err := saveUrlItem(URLItem{
			Title:     title,
			URL:       URL,
			Links:     allLinks, // full outbound graph, not just queued URLs
			ScrapedAt: time.Now().Unix(),
		}); err != nil {
			// One bad append isn't fatal, log it and keep crawling.
			printl("failed to save URLItem: " + err.Error())
		}

		clear()
		printStats()

		// Persist crawl state periodically. Without this, a SIGKILL loses
		// the queue and backlink snapshot (urls.jsonl survives line-by-line
		// via the append above). 30 seconds is a balance between state
		// freshness and disk-write overhead.
		if time.Since(lastSave) > 30*time.Second {
			saveProgress(doneURLs, URLs)
			saveBacklinks(backlinks)
			lastSave = time.Now()
		}

		// Be a good citizen, don't hammer remote servers.
		sleep(200)
	}

	// Crawl finished (queue empty or user interrupted). Flush state and
	// produce the final PageRank output.
	clear()
	printStats()
	saveProgress(doneURLs, URLs)
	saveBacklinks(backlinks)

	// Compute PageRank over the crawled portion of the graph and print
	// the top 20 pages by rank. PageRanks filters out phantom target
	// nodes (URLs we linked to but never crawled from) so the result
	// describes real, content-bearing pages.
	ranks := PageRanks()

	// Flatten the map into a slice so we can sort it. Maps in Go have
	// randomized iteration order; without this the output would shuffle
	// between runs.
	type pageRank struct {
		Page string
		Rank float64
	}
	ranked := make([]pageRank, 0, len(ranks))
	for p, r := range ranks {
		ranked = append(ranked, pageRank{p, r})
	}

	// Sort descending by rank so ranked[0] is the most important page.
	slices.SortFunc(ranked, func(a, b pageRank) int {
		if a.Rank > b.Rank {
			return -1
		}
		if a.Rank < b.Rank {
			return 1
		}
		return 0
	})

	// Print the top K, reading more than a couple dozen ranks off the
	// terminal is rarely useful, and the bottom of the list is all
	// near-identical minimum values anyway.
	const topK = 20
	limit := min(len(ranked), topK)

	printl(fmt.Sprintf("PageRank top %d of %d pages:", limit, len(ranked)))
	for i := range limit {
		printl(fmt.Sprintf("  %2d. %.6f  %s", i+1, ranked[i].Rank, ranked[i].Page))
	}
}

// init registers the signal handler. When the user hits Ctrl+C or the
// process receives SIGTERM, we set `stop` to true so the main loop exits
// cleanly (and saves progress) instead of dying mid-write. A *second*
// Ctrl+C restores the default handler so a stuck network call can still
// be force-killed.
func init() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		stop.Store(true)
		signal.Stop(c)
	}()
}
