import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const pages = {
  home: readFileSync(new URL("../src/pages/HomePage.tsx", import.meta.url), "utf8"),
  listing: readFileSync(new URL("../src/pages/ListingPage.tsx", import.meta.url), "utf8"),
  detail: readFileSync(new URL("../src/pages/VideoDetailPage.tsx", import.meta.url), "utf8"),
  upload: readFileSync(new URL("../src/pages/UploadPage.tsx", import.meta.url), "utf8"),
  shorts: readFileSync(new URL("../src/pages/ShortsPage.tsx", import.meta.url), "utf8"),
};

test("public page document titles omit the site suffix", () => {
  for (const [name, source] of Object.entries(pages)) {
    assert.doesNotMatch(source, /document\.title\s*=[^;]*·\s*91/, `${name} still appends the site suffix`);
  }

  assert.match(pages.home, /document\.title = activeSearchQuery[\s\S]*\? `搜索 "\$\{activeSearchQuery\}"`[\s\S]*: activeTag[\s\S]*\? `标签 \$\{activeTag\}`[\s\S]*: "首页"/);
  assert.match(pages.listing, /\? `搜索 "\$\{keyword\}"`/);
  assert.match(pages.listing, /\? `标签 \$\{tag\}`/);
  assert.match(pages.detail, /document\.title = stableDetail \? stableDetail\.title : "视频不存在"/);
  assert.match(pages.upload, /document\.title = "上传视频"/);
  assert.match(pages.shorts, /document\.title = "短视频"/);
});
