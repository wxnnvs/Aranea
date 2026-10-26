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
