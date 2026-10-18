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
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	switch runtime.GOOS {
	case "darwin":
		runCmd("clear")
	case "linux":
		runCmd("clear")
	default:
		runCmd("clear")
	}
}

func print(msg string) {
	fmt.Println(msg)
}

// print as log
func printl(msg string) {
	log.Print(msg)
}

func goofyprint(msg string) { // print without newline
	fmt.Print(msg)
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

// the queued stat is not shown right for some reason
// it does work when we print len(URLs) inside main manually
func printStats() {
	print("ELAPSED: " + time.Since(start).Round(10*time.Millisecond).String())
	print("DONE:    " + strconv.Itoa(len(doneURLs)))
	print("QUEUED:  " + strconv.Itoa(len(URLs)))
}

// go has no built-in functionality for removing an element from a slice
func removeFromSliceByIndex(a []string, i int) []string {
	a = append(a[:i], a[i+1:]...)
	return a
}

// doestn seem to work...
func timer(name string) func() {
	start := time.Now()
	return func() {
		fmt.Printf("[%s]: %s\n", name, time.Since(start))
	}
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

// --------------------------------------------------
// JSON shi
// --------------------------------------------------

type AllURLItems struct {
	AllURLItems []URLItem `json:"urls"`
}

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
	stop = false
	// URLs = []string{"http://localhost:8000"}
	// URLs = []string{"https://github.com/hackerkid/Mind-Expanding-Books"}
	URLs = []string{"https://awesome-poetry.top/huajianji/"}

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

	doneURLs   = []string{}
	start      = time.Now()
	allItems   AllURLItems
	allItemsMu sync.Mutex
	jsonPath   = "web/test.json"
)

var httpClient = &http.Client{
	Timeout: 15 * time.Second,
}

func main() {
	loadURLItems()

	if len(URLs) == 0 {
		log.Fatal("no seed URL")
	}

	seed := URLs[0]

	_, firstURLs, err := Scrape(seed)
	if err {
		log.Fatal("error scraping seed")
	}

	// Remove seed from queue and mark it done.
	URLs = URLs[1:]
	doneURLs = append(doneURLs, seed)

	// Queue only the links we've actually not seen before.
	// We build a separate slice so we don't mutate firstURLs while iterating it.
	seedNew := []string{}
	for _, link := range firstURLs {
		link, ok := normalizeURL(link)
		if !ok {
			continue
		}
		if link == seed {
			continue
		}
		if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
			continue
		}
		if slices.Contains(URLs, link) || slices.Contains(doneURLs, link) {
			continue
		}
		if isBlacklisted(link) {
			continue
		}
		URLs = append(URLs, link)
		seedNew = append(seedNew, link)
	}

	saveUrlItem(URLItem{
		URL:       seed,
		Links:     seedNew,
		ScrapedAt: time.Now().Unix(),
	})

	printHeader()

	for !stop && len(URLs) > 0 {
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

		saveUrlItem(URLItem{
			Title:     title,
			URL:       URL,
			Links:     newLinks,
			ScrapedAt: time.Now().Unix(),
		})

		clear()
		printStats()
		sleep(200)
	}

	clear()
	printStats()
}

// intercept ctrl + c
func init() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		stop = true
		clear()
		printStats()
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

func loadURLItems() {
	dat, err := os.ReadFile(jsonPath)
	if err != nil {
		if !os.IsNotExist(err) {
			printl("failed to read file: " + err.Error())
		}
		allItems = AllURLItems{}
		return
	}

	if err := json.Unmarshal(dat, &allItems); err != nil {
		printl("error unmarshalling: " + err.Error())
		allItems = AllURLItems{}
	}
}

func saveUrlItem(u URLItem) {
	allItemsMu.Lock()
	defer allItemsMu.Unlock()

	// Append to the in-memory copy we already loaded instead of
	// re-reading the file every time.
	allItems.AllURLItems = append(allItems.AllURLItems, u)

	b := toJSON(allItems)

	// Write to a temp file, then rename so a Ctrl+C mid-write
	// doesn't leave test.json truncated/corrupted.
	tmp := jsonPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		log.Fatal("failed to write temp file: ", err)
	}
	if err := os.Rename(tmp, jsonPath); err != nil {
		log.Fatal("failed to rename temp file: ", err)
	}
}

func isBlacklisted(link string) bool {
	for _, b := range blacklisted {
		if strings.Contains(link, b) {
			return true
		}
	}
	return false
}
