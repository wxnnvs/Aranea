package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// --------------------------------------------------
//  general functions
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
//  more specific functions
// --------------------------------------------------

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

func isScrapable(link string) bool {
	link, ok := normalizeURL(link)
	if !ok {
		return false
	}
	if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
		return false
	}
	if slices.Contains(doneURLs, link) || slices.Contains(URLs, link) {
		return false
	}
	// if isBlacklisted(link) || strings.Contains(link, URL) {
	if isBlacklisted(link) {
		return false
	}
	return true
}
