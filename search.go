package main

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"unicode"
)

const (
	// depthDecay is the per-level multiplier applied to a result's TF-IDF
	// score based on how deep its URL sits. A page at / gets the full
	// score, /about gets 85% of it, /a/b/c gets 0.85³ ≈ 61%. Rationale:
	// sites usually put their most important pages near the root; deep
	// paths are archives, pagination, or tag spam. 0.85 matches the
	// PageRank damping factor, so there's one magic number to remember.
	depthDecay = 0.85

	// minSubstringLen is the minimum query-token length at which substring
	// matching is used. Tokens of this length or longer match any entry
	// token that *contains* them, so "barbara" finds "barbaracollege".
	// Shorter tokens must match whole tokens exactly — that keeps "art"
	// from matching "start" and "the" from matching "there".
	minSubstringLen = 4

	// allQueryTitleBoost is the multiplicative boost applied when *every*
	// query token appears in an entry's title. This is the strongest
	// available signal for "the page is *about* this query", as opposed
	// to "the page mentions all these words somewhere in the body". That
	// distinction is what makes "Sint-Barbaracollege - YouTube" beat a
	// "Sint Barbara" blog that merely links to YouTube in its footer:
	// both technically contain all three tokens, but only the channel has
	// all three in its title. Tune higher for more aggressive emphasis on
	// aboutness, lower (or 1.0) to disable.
	allQueryTitleBoost = 10.0
)

// Entry mirrors one record in urls.jsonl.
type Entry struct {
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Body      string   `json:"body"`
	Links     []string `json:"links"`
	ScrapedAt int64    `json:"scrapedAt"`

	// Index-time fields, populated once at load; json.Unmarshal skips
	// them because they're unexported.
	//
	// termCounts:  how many times each lowercase word appears in Title+Body.
	// titleTokens: same, but Title only — cached separately so the "all
	//              query tokens in title" boost can check it in O(1)
	//              instead of re-tokenizing the title per search.
	// docLen:      total word count, used to normalize term frequency so a
	//              10-word page mentioning "trie" once scores higher than
	//              a 1000-word page mentioning it once.
	// depth:       URL path depth, used to demote deep archive/pagination
	//              URLs. See urlDepth.
	termCounts  map[string]int
	titleTokens map[string]int
	docLen      int
	depth       int
}

// SearchResult is the trimmed-down shape returned to clients — we don't
// want to ship the giant `body` field back over the wire.
type SearchResult struct {
	Title string  `json:"title"`
	URL   string  `json:"url"`
	Rank  float64 `json:"rank"`  // PageRank score; 0 if not yet computed
	Score float64 `json:"score"` // Final relevance: TF-IDF × depth decay × title boost
}

var (
	entries []Entry

	// docFreq[t] = number of documents containing term t. Needed for
	// IDF: terms present in every doc contribute nothing, rare terms
	// contribute a lot. Only counts *exact* token occurrences; substring
	// matches (added for long query tokens) are counted on the fly per
	// query so IDF stays correct for them.
	docFreq map[string]int

	// entries and docFreq are written exactly once inside sync.Once and
	// immutable afterwards, so reads need no lock.
	entriesOnce sync.Once
	entriesErr  error
)

// ensureEntriesLoaded loads and indexes the corpus on first search.
func ensureEntriesLoaded() {
	entriesOnce.Do(func() {
		entries, docFreq, entriesErr = loadEntries(urlsPath)
		if entriesErr != nil {
			printl(entriesErr)
		}
	})
}

// loadEntries reads a stream of concatenated JSON objects (one per record),
// then builds per-doc term counts, per-doc title counts, per-doc URL depth,
// and the global document-frequency table in a single pass. A single
// json.Unmarshal would choke on the second object, hence the Decoder loop.
func loadEntries(path string) ([]Entry, map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	var loaded []Entry
	dec := json.NewDecoder(f)
	for {
		var e Entry
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, nil, err
		}
		loaded = append(loaded, e)
	}

	docFreq := make(map[string]int)
	for i := range loaded {
		loaded[i].termCounts = make(map[string]int)
		loaded[i].docLen = tokenize(loaded[i].Title+" "+loaded[i].Body, loaded[i].termCounts)
		loaded[i].titleTokens = make(map[string]int)
		tokenize(loaded[i].Title, loaded[i].titleTokens)
		loaded[i].depth = urlDepth(loaded[i].URL)
		for term := range loaded[i].termCounts {
			docFreq[term]++
		}
	}

	return loaded, docFreq, nil
}

// tokenize splits s into lowercase alphanumeric words, increments dst for
// each occurrence, and returns the total word count. Anything non-letter
// and non-digit acts as a separator, so "art" no longer matches "start",
// and HTML tag names like "div" become their own tokens.
func tokenize(s string, dst map[string]int) int {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		dst[w]++
	}
	return len(words)
}

// urlDepth counts non-empty path segments. "/" → 0, "/about" → 1,
// "/blog/2024/go" → 3. Query strings and fragments are ignored because
// they don't indicate structural depth. Malformed URLs get depth 0: we
// can't tell how deep they are, so don't penalize them.
func urlDepth(raw string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return 0
	}
	return len(strings.Split(path, "/"))
}

// matchCount returns how many times qToken appears in the given token
// counts, counting exact occurrences plus — for query tokens at least
// minSubstringLen long — any entry token that contains qToken as a
// substring. The counts of all matching entry tokens are summed.
//
// Examples (minSubstringLen = 4):
//
//	q="barbara", tokens={"barbara": 2, "barbaracollege": 1} → 3
//	q="art",     tokens={"start": 5}                        → 0 (too short for substring)
//	q="barb",    tokens={"barbaracollege": 1}               → 0 (too short for substring)
//
// The exact-match fast path returns immediately on exact hit, so entries
// that literally contain the token don't pay the substring scan.
func matchCount(tokens map[string]int, qToken string) int {
	total := tokens[qToken] // exact match contributes directly; 0 if absent
	if len(qToken) < minSubstringLen {
		return total
	}
	for et, c := range tokens {
		if et != qToken && strings.Contains(et, qToken) {
			total += c
		}
	}
	return total
}

// Search ranks every entry that contains ALL query tokens (exact or, for
// longer tokens, substring), combining three signals:
//
//	tfidf(d, q)  = Σ_{t ∈ q} ( count(t,d) / words(d) ) * log( N / df(t) )
//	score(d, q)  = tfidf(d, q) × depthDecay^depth(d) × titleBoost(d, q)
//
// where count(t,d) is the total occurrence count of t in d (including
// substring matches for tokens ≥ minSubstringLen), df(t) is the number
// of documents that match t, and titleBoost is a flat multiplier —
// allQueryTitleBoost if every query token appears in d's *title*, 1.0
// otherwise. Title presence is the strongest signal that a page is
// *about* a query rather than merely mentioning it somewhere in the body,
// which is what separates "Sint-Barbaracollege - YouTube" from a
// "Sint Barbara" blog post that happens to link to YouTube in a footer.
//
// Results are sorted by Score descending, then PageRank, then title (for
// deterministic output across runs). Empty or all-punctuation queries
// return nothing; load errors propagate so the HTTP handler can surface
// them to the client.
func Search(query string) ([]SearchResult, error) {
	results := []SearchResult{}

	q := strings.TrimSpace(query)
	if q == "" {
		return results, nil
	}

	ensureEntriesLoaded()
	if entriesErr != nil {
		return nil, entriesErr
	}

	queryCounts := make(map[string]int)
	tokenize(q, queryCounts)
	if len(queryCounts) == 0 {
		return results, nil
	}

	N := float64(len(entries))
	if N == 0 {
		return results, nil
	}

	// Long query tokens may match via substring, so docFreq (which only
	// tracks exact occurrences) underestimates their document frequency.
	// Precompute, per long token, which entries match and how many times —
	// that gives us both the correct df for IDF below and the per-doc
	// counts for scoring, without rescanning entry tokens twice.
	//
	// Cost: O(entries × tokens-per-entry) per long query token. Fine for
	// personal-crawl corpora; if this ever gets slow, swap in a trigram
	// index. Short tokens use docFreq directly and pay nothing extra.
	longMatches := make(map[string]map[int]int, len(queryCounts))
	for t := range queryCounts {
		if len(t) < minSubstringLen {
			continue
		}
		em := make(map[int]int)
		for i, e := range entries {
			if c := matchCount(e.termCounts, t); c > 0 {
				em[i] = c
			}
		}
		longMatches[t] = em
	}

	// IDF per query token. df = 0 means nothing in the corpus matches
	// this token at all, so the whole search will be empty; leaving idf
	// at 0 makes those entries contribute nothing and the allPresent
	// check below will reject every entry anyway.
	idfMap := make(map[string]float64, len(queryCounts))
	for t := range queryCounts {
		var df int
		if len(t) < minSubstringLen {
			df = docFreq[t]
		} else {
			df = len(longMatches[t])
		}
		if df > 0 {
			idfMap[t] = math.Log(N / float64(df))
		}
	}

	ranks := LoadPageRanks() // nil-safe: map lookups yield 0.0

	for i, e := range entries {
		var tfidf float64
		allPresent := true
		for t := range queryCounts {
			// Per-entry occurrence count for this query token: exact
			// counts for short tokens come from termCounts (O(1)); long
			// tokens were precomputed into longMatches.
			var count int
			if len(t) < minSubstringLen {
				count = e.termCounts[t]
			} else {
				count = longMatches[t][i]
			}
			if count == 0 {
				allPresent = false
				break
			}
			tf := float64(count) / float64(e.docLen)
			tfidf += tf * idfMap[t]
		}
		if !allPresent {
			continue
		}

		// Title boost: does *every* query token appear in the title?
		// Same match rules as body scoring — exact tokens for short
		// tokens, substring allowed for long ones — so "sint-barbara"
		// finds a page titled "Sint-Barbaracollege" via substring, which
		// is exactly the case this feature exists to handle.
		titleHits := 0
		for t := range queryCounts {
			if matchCount(e.titleTokens, t) > 0 {
				titleHits++
			}
		}
		boost := 1.0
		if titleHits == len(queryCounts) {
			boost = allQueryTitleBoost
		}

		results = append(results, SearchResult{
			Title: e.Title,
			URL:   e.URL,
			Rank:  ranks[e.URL],
			Score: tfidf * math.Pow(depthDecay, float64(e.depth)) * boost,
		})
	}

	// Relevance first, then global importance, then stable title order.
	slices.SortFunc(results, func(a, b SearchResult) int {
		if c := cmpFloatDesc(a.Score, b.Score); c != 0 {
			return c
		}
		if c := cmpFloatDesc(a.Rank, b.Rank); c != 0 {
			return c
		}
		return strings.Compare(a.Title, b.Title)
	})

	return results, nil
}

// cmpFloatDesc returns -1 if a > b, 1 if a < b, else 0 — i.e. "greater
// floats sort first" as slices.SortFunc expects.
func cmpFloatDesc(a, b float64) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	default:
		return 0
	}
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")

	results, err := Search(q)

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"query": q,
			"error": err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"query":   q,
		"results": results,
	})
}

func StartWebServer() {
	go func() {
		fileserver := http.FileServer(http.Dir("./static"))
		http.Handle("/", fileserver)
		http.HandleFunc("/api/search", searchHandler)
		printl("Starting server at port 8090\n")
		if err := http.ListenAndServe(":8090", nil); err != nil {
			printl(err)
		}
	}()
}
