package main

// --------------------------------------------------
// Imports and the like
// --------------------------------------------------

import (
	"fmt"
	"log"
	"net/http"
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

func goofyprint(msg string) {
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

const seed = "https://vimexx.be"

var urls = []string{}

var doneUrls = []string{seed}

var start = time.Now()

func main() {

	_, urls = Scrape(seed)

	for !stop {
		// go through all queued urls
		for i := range urls {
			url := urls[i]

			if url == "/help/niet-tevreden-geld-terug" {
				print("whatthekanker")
			}

			// if the url is absolute and has not been processed before, continue
			if (strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")) && !slices.Contains(doneUrls, url) {

				// scrape it
				go func() {
					_, newUrls := Scrape(url)

					// go through all newly found urls
					for j := range newUrls {

						// if its already been processed or queued or it isnt absolute, skip it
						if (strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")) && !slices.Contains(doneUrls, newUrls[j]) && !slices.Contains(urls, newUrls[j]) {
							urls = append(urls, newUrls[j])
						}
					}
				}()

				// remove processed url from queue and add to doneurls
				urls = removeFromSlice(urls, i)
				doneUrls = append(doneUrls, url)
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
		print("ELAPSED: " + time.Since(start).Round(10*time.Millisecond).String())
		print("DONE:    " + strconv.Itoa(len(doneUrls)))
		print("QUEUED:  " + strconv.Itoa(len(urls)))
		os.Exit(0)
	}()
}

func Scrape(url string) (string, []string) {
	if url == "/help/niet-tevreden-geld-terug" {
		print("whatthekanker")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		log.Fatal("Error: this url doesnt start with http(s): " + url)
	}

	res, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		print("wierd statuscode")
		return "", []string{}
	}

	// Load the HTML document
	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		print(err.Error())
		return "", []string{}
	}

	// Find the title
	title := doc.Find("title").First().Text()

	// Find urls
	var foundurls []string
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		url, exists := s.Attr("href")
		if exists {
			foundurls = append(foundurls, url)
		}
	})

	return title, foundurls

}
