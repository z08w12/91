import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const navigationCss = readFileSync(
  new URL("../src/styles/navigation.css", import.meta.url),
  "utf8"
);

const topBarSource = readFileSync(
  new URL("../src/components/TopBar.tsx", import.meta.url),
  "utf8"
);

function ruleBody(css: string, selector: string): string {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = css.match(new RegExp(`${escapedSelector}\\s*\\{([^}]*)\\}`));
  assert.ok(match, `Expected CSS rule for ${selector}`);
  return match[1];
}

test("mobile menu links fill the full expanded menu row", () => {
  // 默认 .main-nav__link 用 inline-flex（mobile 段不重写 display，所以仍是 flex 容器）。
  const baseBody = ruleBody(navigationCss, ".main-nav__link");
  assert.match(baseBody, /display\s*:\s*(?:inline-)?flex\b/);
  // mobile 展开态把链接铺满整行。
  const openBody = ruleBody(navigationCss, ".main-nav.is-open .main-nav__link");
  assert.match(openBody, /width\s*:\s*100%/);
});

test("main nav keeps tap targets below the iOS PWA status area", () => {
  const navBody = ruleBody(navigationCss, ".main-nav");
  assert.match(navBody, /padding-top\s*:\s*env\(safe-area-inset-top,\s*0px\)/);

  const openListBody = ruleBody(navigationCss, ".main-nav.is-open .main-nav__list");
  assert.match(
    openListBody,
    /top\s*:\s*calc\(64px\s*\+\s*env\(safe-area-inset-top,\s*0px\)\)/
  );
});

test("top bar does not render inactive public auth links", () => {
  assert.doesNotMatch(topBarSource, /href="#(?:register|login)"/);
  assert.doesNotMatch(topBarSource, />\s*(?:注册|登录)\s*</);
});
