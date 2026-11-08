package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func ComputePageRank(pages []string, backlinks map[string][]string, outdegree map[string]int) map[string]float64 {

	const c = 0.85

	// set initial page rank to 1/N for all pages
	initial := make(map[string]float64, len(pages))
	for _, p := range pages {
		initial[p] = 1.0 / float64(len(pages))
	}

	// keep memory of previously calculated ranks
	type key struct {
		page  string
		depth int
	}
	memory := make(map[key]float64)

	var rank func(u string, depth int) float64
	rank = func(u string, depth int) float64 {

		if depth == 0 {
			return initial[u]
		}

		k := key{u, depth}          // define key for this u
		if v, ok := memory[k]; ok { // if it exists, return its value (aka we already calculated it)
			return v
		}

		total := 0.0

		for _, v := range backlinks[u] {
			if outdegree[v] > 0 {
				total += rank(v, depth-1) / float64(outdegree[v])
			}
		}

		value := c * total
		memory[k] = value
		return value

	}

	result := make(map[string]float64, len(pages))
	for _, u := range pages {
		result[u] = rank(u, 3)
	}
	return result

}

func NormalizeRanks(ranks map[string]float64) map[string]float64 {
	var total float64
	for _, r := range ranks {
		total += r
	}
	if total == 0 {
		return ranks
	}

	for u, r := range ranks {
		ranks[u] = r / total
	}

	return ranks
}

func TestPageRank() {
	pages := []string{"A", "B", "C", "D"}

	backlinks := map[string][]string{
		"A": {"C"},           // C → A
		"B": {"A"},           // A → B
		"C": {"A", "B", "D"}, // A, B, D all → C
		"D": {},              // nothing → D
	}

	outdegree := map[string]int{
		"A": 2, // A → B, C
		"B": 1, // B → C
		"C": 1, // C → A
		"D": 1, // D → C
	}

	ranks := NormalizeRanks(ComputePageRank(pages, backlinks, outdegree))

	for u, r := range ranks {
		print(u + " " + fmt.Sprintf("%f", r))
	}
}

func ReadBacklinksData() ([]string, map[string][]string, map[string]int) {
	data, err := os.ReadFile(backlinksPath)
	if err != nil {
		printl("err:" + err.Error())
	}

	var backlinks map[string][]string
	if err := json.Unmarshal(data, &backlinks); err != nil {
		printl("err:" + err.Error())
	}

	// Build the set of all pages and the outdegree map.
	pageSet := make(map[string]struct{})
	outdegree := make(map[string]int)

	for page, bls := range backlinks {
		pageSet[page] = struct{}{}
		for _, b := range bls {
			pageSet[b] = struct{}{}
			outdegree[b]++ // b links out to one more page
		}
	}

	pages := make([]string, 0, len(pageSet))
	for p := range pageSet {
		pages = append(pages, p)
	}

	return pages, backlinks, outdegree
}

func PageRanks() map[string]float64 {

	pages, backlinks, outdegree := ReadBacklinksData()

	ranks := ComputePageRank(pages, backlinks, outdegree)
	ranks = NormalizeRanks(ranks)

	return ranks
}
