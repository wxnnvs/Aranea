package main

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

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
