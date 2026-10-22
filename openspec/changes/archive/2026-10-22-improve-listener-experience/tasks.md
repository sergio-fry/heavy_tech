# Tasks

## 1. Configuration and content files

- [x] 1.1 Extend `Channel` with `tagline`, `links` (`label` + absolute http(s) URL) and `start_here`, validate them in `LoadChannel`; verify `go test -run TestLoadChannel` covers valid values, bad URL, empty label and non-positive number.
- [x] 1.2 Load `about.md` at startup and reload it together with `podcast.yaml` after a successful rescan under a mutex; verify tests for about-overrides-description, rescan picking up an edit, missing-file fallback and raw-HTML sanitization.
- [x] 1.3 Document the new `podcast.yaml` fields and `about.md` in README; verify every documented example parses through `LoadChannel` in a test and the README describes the same field names.

## 2. YouTube chapters and views

- [x] 2.1 Add migration `0005_youtube_meta.sql` and store methods to upsert, read and cascade-delete YouTube metadata; verify store tests for upsert, `ON DELETE CASCADE` after episode removal, and refetch after a fresh database.
- [x] 2.2 Implement video-id extraction for `youtu.be`, `watch?v=`, `shorts`, `embed` and `live`, including extra query params; verify table-driven tests for every form and for an unparsable link.
- [x] 2.3 Implement the chapter parser with the ≥3-lines, first-at-0:00 rule and separator handling; verify tests for a valid list, two lines, first not at zero, different separators and empty titles.
- [x] 2.4 Implement the YouTube client and 6-hour refresh loop with batches of 50, 10-second timeout and error isolation; verify tests against an `httptest` fake API for success, API error preserving previous views, refresh-window gating and batching.
- [x] 2.5 Wire the syncer into startup, the periodic ticker and post-rescan refresh; verify the service starts and serves pages without a key, logs the disabled state once, and runs an immediate cycle when a key is present.
- [x] 2.6 Extend `EpisodeView` and `ListEpisodesWithCounts` with `YouTubeViews` and `Chapters` via `LEFT JOIN` and `COALESCE`; verify store tests cover an episode with no YouTube row and one with chapters.
- [x] 2.7 Document `YOUTUBE_API_KEY`, the refresh cadence and degradation behavior in README; verify the documented command starts the service without the key and shows the disabled message.

## 3. Page foundation and SEO

- [x] 3.1 Split `web/index.html` into `layout.html`, `home.html`, `episodes.html`, `episode.html` plus `style.css` and `app.js`, embed and serve `/static/*`; verify all template sets parse and `GET /static/app.js` returns 200 with a JS content type.
- [x] 3.2 Add page-level SEO fields (`PageKind`, `Canonical`, `MetaDescription`, `OGType`, `JSONLD`) and builders for markdown stripping, channel description and JSON-LD `PodcastSeries`/`PodcastEpisode`; verify handler tests assert canonical, meta description and decodable JSON-LD on each page type.
- [x] 3.3 Implement `GET /sitemap.xml` and `GET /robots.txt`; verify tests for all public URLs, absolute `Sitemap` line, and disappearance of a removed episode after rescan.
- [x] 3.4 Implement the landing handler: `about.md` text, latest three episodes, `start_here` order, subscribe actions, latest-play action and empty state; verify handler tests for each behavior and the fallback without `about.md`.
- [x] 3.5 Implement `GET /episodes` with compact rows and `data-year`, `data-guests`, `data-video`, `data-search` attributes; verify tests for full list, attributes present, links to episode pages and a no-JS-complete list.
- [x] 3.6 Implement `GET /episodes/{number}` with full notes, media actions, YouTube link, share action, prev/next neighbors and 404; verify tests for a full episode, unknown number, video-only episode and neighbor order.
- [x] 3.7 Implement the subscribe block from `links` with ordered external buttons, `rel="noopener noreferrer"` and always-available RSS copy; verify tests for ordered links, absent links and copy target.
- [x] 3.8 Document the new routes and the `/#ep-N` → `/episodes/N` migration in README; verify each documented route returns the documented status against a running local server.

## 4. Player and UX

- [x] 4.1 Replace per-card audio with a single `<audio>` and a shared player object that switches episodes and shows a fixed mini-panel when the inline player leaves the viewport; verify handler markup (one `audio`, data attributes) and a browser check that playing another episode stops the previous one.
- [x] 4.2 Add resume position and speed persistence with guarded `localStorage` access and a «начать сначала» action; verify browser checks for resume at 12:34, near-end restart, reset, and remembered 1.5x after reload.
- [x] 4.3 Make the progress bar a focusable `role="slider"`/range with accessible name and value, ±15-second keyboard seek and a 44 px touch target; verify markup tests for role/aria/label and a manual keyboard and phone check.
- [x] 4.4 Render chapters as a collapsed `<details>` block with clickable seek buttons and rail markers, keeping them out of show notes; verify handler tests for the block and absence without chapters, plus a browser click that seeks to the chapter start.
- [x] 4.5 Publish title, channel and cover through Media Session and handle system play/pause/±15 s; verify metadata in browser devtools and on a phone lock screen.
- [x] 4.6 Send `POST /track/play` on playback start from the shared player without duplicate sends on pause/resume; verify the network tab shows one beacon per start and existing play-handler tests still pass.

## 5. Counters and API

- [x] 5.1 Add `youtube_views` and `chapters` to `GET /api/episodes` while keeping existing fields; verify a JSON test checks values, empty chapter arrays and the old fields unchanged.
- [x] 5.2 Compute engagement as downloads + plays + YouTube views and hide it when zero on archive and episode pages; verify handler tests for zero hidden, non-zero shown and view contribution counted.

## 6. Integration verification

- [x] 6.1 Run `go test ./...`, `go vet ./...` and `go build -o heavytech .`; verify all pass and the binary starts against test fixtures.
- [x] 6.2 Run a local end-to-end pass with fixture episodes and a fake YouTube API: check `/`, `/episodes`, `/episodes/{n}`, `/sitemap.xml`, `/robots.txt`, `/api/episodes`, both feeds and `/audio|video/{n}`; verify feeds and media responses are byte-for-byte compatible with the previous build.
- [x] 6.3 Run the manual browser matrix — desktop and phone, JS disabled, private mode, dark mode, keyboard-only, screen reader labels; verify every spec scenario that cannot be covered by Go tests.
- [x] 6.4 Prepare the deploy checklist: migration `0005`, `YOUTUBE_API_KEY`, content files, `POST /admin/rescan`, rollback to the previous binary; verify each step is written in README with the expected observable result.

## Workflow follow-up

- Archive the change after review and a successful production rollout.
- Verify the archived specs match the shipped routes and behaviors.
