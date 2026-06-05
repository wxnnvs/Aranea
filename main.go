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

// --------------------------------------------------
// The actual main code stuff
// --------------------------------------------------

var stop = false

func main() {

	for !stop {
		printHeader()
		goofyprint("input url: ")
		var i string
		fmt.Scan(&i)
		print("Title: " + Scrape(i))
		fmt.Scanln(&i)
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
		print("shutting down...")
		os.Exit(0)
	}()
}

func Scrape(url string) string {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		log.Fatal("Error: this url doesnt start with http(s)")
	}

	res, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		log.Fatalf("status code error: %d %s", res.StatusCode, res.Status)
	}

	// Load the HTML document
	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Fatal(err)
	}

	// Find the title
	title := doc.Find("title").First().Text()

	return title

}
