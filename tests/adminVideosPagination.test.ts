import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const videosPageSource = readFileSync(new URL("../src/admin/VideosPage.tsx", import.meta.url), "utf8");

test("admin videos page uses responsive page size", () => {
  assert.match(videosPageSource, /const DESKTOP_VIDEOS_PAGE_SIZE = 50;/);
  assert.match(videosPageSource, /const MOBILE_VIDEOS_PAGE_SIZE = 20;/);
  assert.match(videosPageSource, /const VIDEOS_MOBILE_QUERY = "\(max-width: 640px\)";/);
  assert.match(videosPageSource, /window\.matchMedia\(VIDEOS_MOBILE_QUERY\)/);
  assert.match(videosPageSource, /api\.listVideos\(\{ driveId, page, size: pageSize, keyword: searchKeyword \}\)/);
});
