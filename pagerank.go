package main

// --------------------------------------------------
// PageRank implementation.
//
// The graph model: each URL is a node, each hyperlink is a directed edge.
// A "random surfer" moves through the graph by picking a random outlink,
// except (a) with probability 1-d they jump to a uniformly random page,
// and (b) if they land on a page with no outlinks, they always jump.
// A page's rank is the long-run fraction of time the surfer spends on it.
// --------------------------------------------------

import (
	"encoding/json"
	"math"
	"os"
	"strings"
)

// ComputePageRank iterates the PageRank fixed point and returns one rank
// per entry in `pages`, in the same order.
//
// Inputs:
//   - pages:         every URL in the graph (both link sources and targets).
//   - backlinks:     backlinks[target] = list of URLs that link *to* target.
//   - outdegree:     outdegree[source] = number of distinct pages it links to.
//   - epsilon:       stop when the L1 change between iterations drops below this.
//   - maxIterations: hard cap on iterations (safety net for convergence stalls).
//
// The damping factor d = 0.85 is hard-coded, matching the PageRank paper.
func ComputePageRank(pages []string, backlinks map[string][]string, outdegree map[string]int, epsilon float64, maxIterations int) []float64 {
	const d = 0.85
	N := float64(len(pages))

	// Empty graph: nothing to compute, and the 1/N below would be +Inf.
	if N == 0 {
		return nil
	}

	// Name -> position lookup. Turns "which slot in PR[] holds page X?"
	// from an O(N) scan into an O(1) map lookup. Critical for speed when
	// the inner loop runs once per edge per iteration.
	index := make(map[string]int, len(pages))
	for i, p := range pages {
		index[p] = i
	}

	// Step 1: initialize every page with equal probability.
	PR := make([]float64, len(pages))
	for i := range PR {
		PR[i] = 1.0 / N
	}

	// Pre-collect positions of dangling pages (outdegree 0). They contribute
	// no outbound flow, so their rank each iteration must be redistributed
	// uniformly, see `danglingShare` below. Precomputing this avoids a
	// per-iteration scan over all pages looking for the same subset.
	dangling := make([]int, 0)
	for i, p := range pages {
		if outdegree[p] == 0 {
			dangling = append(dangling, i)
		}
	}

	// The "teleport" portion is the same for every page, every iteration.
	teleportShare := (1 - d) / N

	delta := math.Inf(1) // forces at least one iteration
	iterations := 0

	for delta > epsilon && iterations < maxIterations {
		// Total rank currently held by dangling pages. That fraction of
		// the graph has no outlinks to travel along, so it must be re-routed
		// uniformly, each of the N pages gets an equal slice of it.
		var danglingRank float64
		for _, di := range dangling {
			danglingRank += PR[di]
		}
		danglingShare := d * danglingRank / N

		// Step 2: each page's new rank is
		//   teleportShare                                (jump here randomly)
		// + d * Σ_{u links to p} PR[u] / outdegree[u]    (flow from in-neighbors)
		// + danglingShare                                (slice of orphaned mass)
		// Note: u divides its rank *equally* among its outlinks.
		newPR := make([]float64, len(pages))
		for i, p := range pages {
			var linkContrib float64
			for _, src := range backlinks[p] {
				linkContrib += PR[index[src]] / float64(outdegree[src])
			}
			newPR[i] = teleportShare + d*linkContrib + danglingShare
		}

		// L1 convergence check: sum of absolute changes across all pages.
		delta = 0
		for i := range PR {
			delta += math.Abs(newPR[i] - PR[i])
		}
		PR = newPR
		iterations++
	}
	return PR
}

// NormalizeRanks divides every entry by the total so the result sums to 1.0.
// PageRank already produces a probability distribution when run on a closed
// graph, but this is a safety net after any post-processing (filtering,
// floating-point drift) that might have broken that invariant.
func NormalizeRanks(ranks []float64) []float64 {
	var total float64
	for _, r := range ranks {
		total += r
	}
	if total == 0 {
		return ranks
	}
	for i, r := range ranks {
		ranks[i] = r / total
	}
	return ranks
}

// ReadBacklinksData loads the backlinks JSON from disk and derives the
// two extra structures PageRank needs.
//
// The on-disk format is `{"target_url": ["source_url", "source_url", ...]}`
// — i.e. each key is a page, and its value lists every page that links to it.
//
// From that single map we compute:
//   - pages:      the union of all keys (targets) and all values (sources),
//     because a URL can appear on either side of an edge.
//   - outdegree:  for each source, the number of distinct targets it appears
//     under. A source that shows up in K different targets'
//     backlink lists is emitting K outbound links.
//
// On any error, returns nil for all three so callers can detect failure
// with len(pages) == 0 instead of crashing on a partial map.
func ReadBacklinksData() ([]string, map[string][]string, map[string]int) {
	data, err := os.ReadFile(backlinksPath)
	if err != nil {
		printl("err: " + err.Error())
		return nil, nil, nil
	}

	var backlinks map[string][]string
	if err := json.Unmarshal(data, &backlinks); err != nil {
		printl("err: " + err.Error())
		return nil, nil, nil
	}

	// Single pass to collect every URL mentioned and count outlinks.
	pageSet := make(map[string]struct{})
	outdegree := make(map[string]int)
	for target, sources := range backlinks {
		pageSet[target] = struct{}{}
		for _, src := range sources {
			pageSet[src] = struct{}{}
			outdegree[src]++
		}
	}

	// Flatten the URL set into a slice, PageRank wants stable indices.
	pages := make([]string, 0, len(pageSet))
	for p := range pageSet {
		pages = append(pages, p)
	}
	return pages, backlinks, outdegree
}

// PageRanks computes PageRank over the pages we actually crawled, and
// returns a labeled map (URL -> rank) so callers can sort and print.
//
// Why the post-filter: ReadBacklinksData includes every URL that appears
// anywhere in the graph, including thousands of *external* targets we
// crawled-to but never crawled-from (Wikipedia, GitHub, etc.). Those have
// outdegree 0, so they all converge to the same minimum value and add
// nothing to the output besides noise. Keeping only pages with
// outdegree > 0 gives us exactly the pages the crawler saw emit links —
// i.e. the pages we actually have content for. Ranks are then
// re-normalized over that subset so they still sum to 1.
func PageRanks() map[string]float64 {
	pages, backlinks, outdegree := ReadBacklinksData()
	if len(pages) == 0 {
		return nil
	}

	ranks := ComputePageRank(pages, backlinks, outdegree, 0.0001, 70)

	// Keep only pages that emit at least one link (i.e. crawled pages
	// with real content). Everything else is a phantom target node.
	//
	// Also ignore ooh.directory pages as these are just the seed, not
	// the actual content.
	names := make([]string, 0, len(pages))
	values := make([]float64, 0, len(pages))
	for i, p := range pages {
		if outdegree[p] > 0 && !strings.Contains(p, "ooh.directory") {
			names = append(names, p)
			values = append(values, ranks[i])
		}
	}
	NormalizeRanks(values)

	// Pair URL -> rank so the caller can print with labels.
	out := make(map[string]float64, len(names))
	for i, n := range names {
		out[n] = values[i]
	}
	return out
}
