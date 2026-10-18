# TODO

- [ ] Replace O(n) `slices.Contains` checks in the main loop with map sets for O(1) membership tests.
- [ ] Parallelize scraping with a small worker pool (4-8 workers) instead of one page at a time.
- [ ] Respect `robots.txt` before crawling a host.
- [ ] Add a per-host rate limit so we don't get banned.