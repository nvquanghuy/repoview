import { test, expect } from "@playwright/test";

test.describe("Sidebar tree", () => {
  test("root loads and contains known files", async ({ page }) => {
    await page.goto("/");
    const sidebar = page.locator("#tree-container");
    await expect(sidebar.locator(".tree-item")).not.toHaveCount(0);
    await expect(sidebar.locator(".tree-item .label", { hasText: "hello.md" })).toBeVisible();
    await expect(sidebar.locator(".tree-item .label", { hasText: "data.csv" })).toBeVisible();
    await expect(sidebar.locator(".tree-item .label", { hasText: "subdir" })).toBeVisible();
  });
});

test.describe("File viewing", () => {
  test("markdown — rendered HTML with headings, table, heading IDs", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "hello.md" }).click();
    const content = page.locator("#content-area");
    await expect(content.locator(".markdown-body")).toBeVisible();
    await expect(content.locator("h1")).toHaveText("Hello World");
    await expect(content.locator("h1")).toHaveAttribute("id", /./);
    await expect(content.locator("table")).toBeVisible();
    await expect(content.locator("td", { hasText: "Alice" })).toBeVisible();
  });

  test("CSV — rendered HTML table with data", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "data.csv" }).click();
    const content = page.locator("#content-area");
    await expect(content.locator(".csv-table")).toBeVisible();
    await expect(content.locator("th", { hasText: "name" })).toBeVisible();
    await expect(content.locator("td", { hasText: "Alice" })).toBeVisible();
    await expect(content.locator("td", { hasText: "Tokyo" })).toBeVisible();
  });

  test("code — syntax-highlighted source", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "sample.go" }).click();
    const content = page.locator("#content-area");
    await expect(content.locator(".source-code")).toBeVisible();
    // Chroma wraps tokens in <span> elements for syntax highlighting
    await expect(content.locator(".source-code span").first()).toBeVisible();
  });
});

test.describe("Frontmatter", () => {
  test("renders frontmatter table and body", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "frontmatter.md" }).click();
    const content = page.locator("#content-area .markdown-body");
    await expect(content).toBeVisible();
    // Frontmatter is rendered as a table with Key/Value headers
    await expect(content.locator("th", { hasText: "Key" })).toBeVisible();
    await expect(content.locator("td", { hasText: "title" })).toBeVisible();
    await expect(content.locator("td", { hasText: "My Document" })).toBeVisible();
    // Body content
    await expect(content.locator("h1", { hasText: "Welcome" })).toBeVisible();
  });
});

test.describe("Directory listing", () => {
  test("clicking a folder shows directory listing with '..' entry", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "subdir" }).click();
    const content = page.locator("#content-area");
    await expect(content.locator(".dir-listing")).toBeVisible();
    await expect(content.locator(".dir-listing-row .name", { hasText: ".." })).toBeVisible();
    await expect(content.locator(".dir-listing-row .name", { hasText: "nested.md" })).toBeVisible();
  });

  test("parent navigation — clicking '..' returns to root", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "subdir" }).click();
    await expect(page.locator("#content-area .dir-listing-row .name", { hasText: ".." })).toBeVisible();
    await page.locator(".dir-listing-row .name", { hasText: ".." }).click();
    // After clicking "..", we should see root-level files again
    const content = page.locator("#content-area");
    await expect(content.locator(".dir-listing-row .name", { hasText: "hello.md" })).toBeVisible();
    await expect(content.locator(".dir-listing-row .name", { hasText: "subdir" })).toBeVisible();
  });
});

test.describe("Fuzzy search", () => {
  test("press 't', type query, see results, select one", async ({ page }) => {
    await page.goto("/");
    // Wait for tree to load first
    await expect(page.locator("#tree-container .tree-item")).not.toHaveCount(0);
    await page.keyboard.press("t");
    await expect(page.locator("#search-overlay")).toHaveClass(/open/);
    await page.locator("#search-input").fill("hello");
    await expect(page.locator(".search-result").first()).toBeVisible();
    await expect(page.locator(".search-result .sr-path", { hasText: "hello" })).toBeVisible();
    // Click the matching result directly
    await page.locator(".search-result .sr-path", { hasText: "hello" }).first().click();
    await expect(page.locator("#search-overlay")).not.toHaveClass(/open/);
    await expect(page.locator("#content-area .markdown-body")).toBeVisible();
  });
});

test.describe("Anchor links", () => {
  test("headings have id attributes", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "hello.md" }).click();
    const content = page.locator("#content-area .markdown-body");
    await expect(content.locator("h1")).toBeVisible();
    const h2s = content.locator("h2");
    const count = await h2s.count();
    expect(count).toBeGreaterThan(0);
    for (let i = 0; i < count; i++) {
      await expect(h2s.nth(i)).toHaveAttribute("id", /./);
    }
  });
});

test.describe("Page title", () => {
  test("title updates to show filename when viewing a file", async ({ page }) => {
    await page.goto("/");
    await expect(page).toHaveTitle("RepoView");
    await page.locator(".tree-item .label", { hasText: "hello.md" }).click();
    await expect(page).toHaveTitle("hello.md - RepoView");
  });

  test("title updates to show dirname when navigating to a subdirectory", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "subdir" }).click();
    await expect(page).toHaveTitle("subdir - RepoView");
  });

  test("title resets to RepoView when navigating back to root", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "hello.md" }).click();
    await expect(page).toHaveTitle("hello.md - RepoView");
    await page.locator(".crumb.root").click();
    await expect(page).toHaveTitle("RepoView");
  });
});

test.describe("URL routing", () => {
  test("direct navigation to a file URL loads the file", async ({ page }) => {
    await page.goto("/hello.md");
    const content = page.locator("#content-area");
    await expect(content.locator(".markdown-body")).toBeVisible();
    await expect(content.locator("h1", { hasText: "Hello World" })).toBeVisible();
  });
});
