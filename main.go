package main

// --------------------------------------------------
// Imports and the like
// --------------------------------------------------

import (
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
	case "windows":
		runCmd("cmd", "/c", "cls")
	default:
		runCmd("clear")
	}
}

func print(msg string) {
	fmt.Println(msg)
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
func removeFromSlice(a []string, i int) []string {
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

// --------------------------------------------------
// The actual main code stuff
// --------------------------------------------------

var stop = false

// it first does all newfound links instead of doing github, google, youtube, reddit, new links, ...
var URLs = []string{
	"https://github.com",
	"https://google.com",
	"https://youtube.com",
	"https://reddit.com",
}

var doneURLs = []string{}

var start = time.Now()

func main() {

	// if we dont scrape 1 url first, it gives an index out of range later for some reason
	_, URLs, err := Scrape(URLs[0])
	URLs = removeFromSlice(URLs, 0)
	doneURLs = append(doneURLs, URLs[0])

	if err {
		log.Fatal("error scraping seed")
	}

	printHeader()

	for !stop {
		// go through all queued URLs

		for i := range URLs {
			URL := URLs[i]

			// if the URL is absolute and has not been processed before, continue
			if (strings.HasPrefix(URL, "http://") || strings.HasPrefix(URL, "https://")) && !slices.Contains(doneURLs, URL) {

				// scrape it
				go func() {
					_, newURLs, err := Scrape(URL)

					if !err {
						// go through all newly found URLs
						for j := range newURLs {

							// if is not already been processed or queued and it is absolute, add it to queue
							if (strings.HasPrefix(URL, "http://") || strings.HasPrefix(URL, "https://")) && !slices.Contains(doneURLs, newURLs[j]) && !slices.Contains(URLs, newURLs[j]) {
								URLs = append(URLs, newURLs[j])
							}
						}
					}

				}()

				// remove processed URL from queue and add to doneURLs
				URLs = removeFromSlice(URLs, i)
				doneURLs = append(doneURLs, URL)
				clear()
				print("ELAPSED: " + time.Since(start).Round(10*time.Millisecond).String())
				print("DONE:    " + strconv.Itoa(len(doneURLs)))
				print("QUEUED:  " + strconv.Itoa(len(URLs)))
				sleep(200)
			}
		}
	}

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
		os.Exit(0)
	}()
}

func Scrape(URL string) (string, []string, bool) {
	if !strings.HasPrefix(URL, "http://") && !strings.HasPrefix(URL, "https://") {
		print("Error: this URL doesnt start with http(s): " + URL)
		return "", []string{}, true
	}

	u, err := url.Parse(URL)
	domain := u.Hostname()

	res, err := http.Get(URL)
	if err != nil {
		print(err.Error())
		return "", []string{}, true
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		print("wierd statuscode: " + strconv.Itoa(res.StatusCode) + " " + URL)
		return "", []string{}, true
	}

	// Load the HTML document
	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		print(err.Error())
		return "", []string{}, false
	}

	// Find the title
	title := doc.Find("title").First().Text()

	// Find URLs
	var foundURLs []string
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		URL, exists := s.Attr("href")
		if exists {
			if !strings.HasPrefix(URL, "http://") && !strings.HasPrefix(URL, "https://") {
				URL = "https://" + domain + URL
			}
			foundURLs = append(foundURLs, URL)
		}
	})

	return title, foundURLs, false

}
