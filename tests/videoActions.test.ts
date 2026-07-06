import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const actionsSource = readFileSync(
  new URL("../src/components/VideoActions.tsx", import.meta.url),
  "utf8"
);
const detailCss = readFileSync(
  new URL("../src/styles/video-detail.css", import.meta.url),
  "utf8"
);
const detailPageSource = readFileSync(
  new URL("../src/pages/VideoDetailPage.tsx", import.meta.url),
  "utf8"
);
const infoPanelSource = readFileSync(
  new URL("../src/components/VideoInfoPanel.tsx", import.meta.url),
  "utf8"
);

test("detail dislike does not locally decrement persisted likes", () => {
  const match = /function handleDislike\(\) \{([\s\S]*?)\n  return \(/.exec(
    actionsSource
  );
  assert.ok(match, "handleDislike block should be present");
  assert.match(match[1], /setDisliked\(true\)/);
  assert.doesNotMatch(match[1], /setLikes/);
});

test("detail like and dislike buttons are visually separated", () => {
  assert.doesNotMatch(actionsSource, /vd-actions__divider/);
  assert.match(
    detailCss,
    /\.vd-actions__group\s*\{[^}]*gap:\s*var\(--space-2\)/s
  );
  assert.match(
    detailCss,
    /\.vd-actions__pill\s*\{[^}]*border:\s*1px solid var\(--border-subtle\)[^}]*border-radius:\s*var\(--radius-sm\)/s
  );
});

test("detail playback actions only expose delete as the management action", () => {
  assert.doesNotMatch(actionsSource, /不再显示/);
  assert.doesNotMatch(actionsSource, /EyeOff/);
  assert.doesNotMatch(actionsSource, /onHideVideo/);
  assert.doesNotMatch(actionsSource, /hideSaving/);
  assert.doesNotMatch(actionsSource, /vd-actions__hide/);
  assert.match(actionsSource, /aria-label="删除这个视频"/);
  assert.doesNotMatch(detailPageSource, /hideVideo/);
  assert.doesNotMatch(detailPageSource, /handleHideVideo/);
  assert.doesNotMatch(detailPageSource, /onHideVideo/);
});

test("detail delete dialog stays centered on mobile", () => {
  assert.match(
    detailCss,
    /@media \(max-width:\s*480px\)\s*\{[\s\S]*\.vd-delete-modal\s*\{[^}]*place-items:\s*center/s
  );
  assert.doesNotMatch(
    detailCss,
    /@media \(max-width:\s*480px\)\s*\{[\s\S]*\.vd-delete-modal\s*\{[^}]*align-items:\s*end/s
  );
});

test("detail delete source option stays visually flat", () => {
  assert.match(
    detailCss,
    /\.vd-delete-option\s*\{[^}]*padding:\s*0[^}]*border:\s*0[^}]*background:\s*transparent/s
  );
});

test("detail tag editor opens as a page modal", () => {
  assert.match(infoPanelSource, /createPortal\(/);
  assert.match(infoPanelSource, /className="vd-tag-editor-modal"/);
  assert.match(infoPanelSource, /aria-modal="true"/);
  assert.match(
    detailCss,
    /\.vd-tag-editor-modal\s*\{[^}]*position:\s*fixed[^}]*place-items:\s*center/s
  );
  assert.doesNotMatch(detailCss, /\.vd-tag-editor\s*\{[^}]*margin:/s);
});

test("detail tag editor chips hide counts and avoid divider lines", () => {
  assert.doesNotMatch(infoPanelSource, /typeof tag\.count/);
  assert.doesNotMatch(infoPanelSource, /<em>\{tag\.count\}<\/em>/);
  assert.doesNotMatch(detailCss, /vd-tag-editor__chip em/);
  assert.match(detailCss, /\.vd-tag-editor\s*\{[^}]*border:\s*0/s);
  assert.match(detailCss, /\.vd-tag-editor__head\s*\{[^}]*border-bottom:\s*0/s);
  assert.match(detailCss, /\.vd-tag-editor__chip\s*\{[^}]*border:\s*0/s);
  assert.match(detailCss, /\.vd-tag-editor__actions\s*\{[^}]*border-top:\s*0/s);
});
