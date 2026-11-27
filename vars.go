package main

import (
	"sync/atomic"
	"time"
)

var (
	stop atomic.Bool

	// URLs is the crawl queue. Restored from db/queue.json on startup,
	// seeded with `seed` on a truly fresh run.
	URLs = []string{}
	
	// seed = "https://ooh.directory"
	seed = "https://sint-barbara.be"

	// for this PoC we skip uninteresting sources
	blacklisted = []string{
		"githubusercontent.com",
		"google.com",
		
		"github.com", // the bloat here is not worth the value
		"github.blog",
		"githubstatus.com",

		"goodreads.com", // temp until we fix 202 http handling

		"instapods.com",

		"googleusercontent.com",
		"google.com",
		"buymeacoffee.com",

	//	"notiz.blog", // for now, todo: strip query parameters in url
	}

	doneURLs      = []string{}
	start         = time.Now()
	dbPath        = "db/"
	urlsPath      = dbPath + "urls.jsonl"
	backlinksPath = dbPath + "backlinks.json"
	pageRankPath  = dbPath + "pagerank.json"

	userAgent = "aranea/0.0 (+https://blog.wannesg.be)"
)
