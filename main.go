package main

// --------------------------------------------------
// Imports and the like
// --------------------------------------------------

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// --------------------------------------------------
//  some simple helper utils
// --------------------------------------------------

func runCmd(name string, arg ...string) {
	cmd := exec.Command(name, arg...)
	cmd.Stdout = os.Stdout
	cmd.Run()
}

func clear() {
	runCmd("clear")
}

func print(msg string) {
	fmt.Println(msg)
}

// print as log
func printl(msg string) {
	log.Print(msg)
}

func sleep(milliseconds int) {
	time.Sleep(time.Duration(milliseconds) * time.Millisecond)
}

func printHeader() {
	clear()
	print("====================")
	print("|      ARANEA      |")
	print("====================")
	print("pre-alpha v0")
	print("")
}

func printStats() {
	print("ELAPSED: " + time.Since(start).Round(10*time.Millisecond).String())
	print("DONE:    " + strconv.Itoa(len(doneURLs)))
	print("QUEUED:  " + strconv.Itoa(len(URLs)))
}

func toJSON(data any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // Disables escaping of &, <, and >

	if err := enc.Encode(data); err != nil {
		panic(err)
	}

	return buf.Bytes()
}

// normalizeURL canonicalizes a URL so equivalent forms compare equal.
// Returns ok=false if the URL is not http(s) or can't be parsed.
func normalizeURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	if u.Host == "" {
		return "", false
	}

	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)

	// Drop default ports.
	if (u.Scheme == "http" && strings.HasSuffix(u.Host, ":80")) ||
		(u.Scheme == "https" && strings.HasSuffix(u.Host, ":443")) {
		u.Host = u.Host[:strings.LastIndex(u.Host, ":")]
	}

	// Collapse trailing slash: "/" and "" are the same; "/foo/" and "/foo" are the same.
	if u.Path == "/" {
		u.Path = ""
	} else {
		u.Path = strings.TrimSuffix(u.Path, "/")
	}

	return u.String(), true
}

// atomicWriteJSON serialises v and swaps it into path via a temp file,
// so a crash mid-write can't leave path truncated.
func atomicWriteJSON(path string, v any) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, toJSON(v), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

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

var (
	stop atomic.Bool

	// URLs is the crawl queue. Restored from db/queue.json on startup,
	// seeded with `seed` on a truly fresh run.
	URLs = []string{}
	seed = "https://awesome-poetry.top/huajianji/"

	// for this PoC we skip uninteresting sources
	blacklisted = []string{
		"github.com/login",
		"github.com/mcp",
		"github.com/features",
		"github.com/signup",
		"github.com/contact",
		"github.com/resources",
		".github.com",
		"github.com/solutions",
		"/blob/",
		"/tree/",
		"githubusercontent.com",
		"google.com",
		"github.com/security",
		"github.com/why-github",
		"github.com/blog",
		"github.com/marketplace",
		"github.com/enterprise",
		"github.com/team",
		"github.com/customer-stories",
		"github.com/orgs",
		"github.com/trust-center",
		"github.com/partners",
		"github.com/open-source",
		"github.com/topics",
		"github.com/collections",
		"github.com/pricing",
		// "github.com", // we need to be able to still access repos ofc
		"github.blog",

		"goodreads.com", // temp until we fix 202 http handling

		"githubstatus.com",
	}

	doneURLs = []string{}
	start    = time.Now()
	dbPath   = "db/"
	urlsPath = dbPath + "urls.jsonl"
)

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

// saveUrlItem appends one record to urls.jsonl.
// Opening with O_APPEND means we never rewrite the file, so a crash
// mid-write loses at most the partial last line — every earlier line
// is still valid JSON.
func saveUrlItem(u URLItem) error {
	f, err := os.OpenFile(urlsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false) // match the old toJSON behaviour
	return enc.Encode(u)     // Encode appends a trailing "\n"
}

func isBlacklisted(link string) bool {
	for _, b := range blacklisted {
		if strings.Contains(link, b) {
			return true
		}
	}
	return false
}

// saveProgress persists crawl state to disk. Done list and queue go to
// separate files so neither clobbers the other, and neither touches
// urls.jsonl (that's the crawl output, owned by saveUrlItem).
func saveProgress(done []string, queue []string) {

	print("saving progress...")

	if err := atomicWriteJSON(dbPath+"done.json", done); err != nil {
		printl("failed to save done.json: " + err.Error())
	}
	if err := atomicWriteJSON(dbPath+"queue.json", queue); err != nil {
		printl("failed to save queue.json: " + err.Error())
	}

	print("progress saved.")
}

// loadProgress restores crawl state. Missing files = fresh crawl.
// A corrupt file is reported and treated as empty so we don't silently
// crawl with half-loaded state.
func loadProgress() {
	if dat, err := os.ReadFile(dbPath + "done.json"); err == nil {
		if err := json.Unmarshal(dat, &doneURLs); err != nil {
			printl("corrupt done.json, starting fresh: " + err.Error())
			doneURLs = nil
		}
	}
	if dat, err := os.ReadFile(dbPath + "queue.json"); err == nil {
		if err := json.Unmarshal(dat, &URLs); err != nil {
			printl("corrupt queue.json, starting fresh: " + err.Error())
			URLs = nil
		}
	}
}

func initDB() {
	// 1. Ensure the db/ directory exists. MkdirAll is a no-op when the
	//    directory is already there, so it's safe to call on every run.
	//    urls.jsonl itself is created lazily by saveUrlItem via O_CREATE.
	if err := os.MkdirAll(dbPath, 0755); err != nil {
		log.Fatal("could not create db directory: ", err)
	}

	// 2. Sweep away stale .tmp files from a previous run that was killed
	//    before it could rename them into place. urls.jsonl is append-only
	//    so it has no tmp file — only done.json/queue.json do.
	stale := []string{
		dbPath + "done.json.tmp",
		dbPath + "queue.json.tmp",
	}
	for _, f := range stale {
		if _, err := os.Stat(f); err == nil {
			_ = os.Remove(f)
		}
	}
}
