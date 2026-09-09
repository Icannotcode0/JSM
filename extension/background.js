// Background service worker — the only piece allowed to make network calls
// (see DESIGN_GUIDE.md Part 6 / Milestone 13). Receives messages from
// content.js rather than content.js fetching directly.

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  // TODO: POST message.job to the local JSM API once the extension-import
  // endpoint exists (Milestone 14).
  console.log("[JSM] background received:", message);
});
