// Content script — extraction only, never touches the network directly
// (see DESIGN_GUIDE.md Part 6 / Milestone 13). Runs in an isolated world on
// whatever job-board pages get added to manifest.json's content_scripts
// "matches".

// TODO: look for a <script type="application/ld+json"> schema.org/JobPosting
// block (JSON.parse only, never eval), fall back to OG tags. Inject a
// "Save to JSM" button using textContent/DOM APIs — never innerHTML on
// scraped content.
