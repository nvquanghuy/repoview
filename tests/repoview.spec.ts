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

  test("clicking chevron collapses an expanded folder", async ({ page }) => {
    await page.goto("/");
    const sidebar = page.locator("#tree-container");
    await expect(sidebar.locator(".tree-item")).not.toHaveCount(0);

    // Click the subdir label to expand it
    await sidebar.locator(".tree-item .label", { hasText: "subdir" }).click();
    const subdirRow = sidebar.locator(".tree-item", { hasText: "subdir" }).first();
    await expect(subdirRow.locator(".caret")).toHaveClass(/open/);
    // nested.md should be visible inside expanded subdir
    await expect(sidebar.locator(".tree-item .label", { hasText: "nested.md" })).toBeVisible();

    // Click the chevron to collapse
    await subdirRow.locator(".caret").click();
    await expect(subdirRow.locator(".caret")).not.toHaveClass(/open/);
    // nested.md should no longer be visible
    await expect(sidebar.locator(".tree-item .label", { hasText: "nested.md" })).not.toBeVisible();
  });

  test("clicking folder icon toggles expand/collapse", async ({ page }) => {
    await page.goto("/");
    const sidebar = page.locator("#tree-container");
    const subdirRow = sidebar.locator(".tree-item", { hasText: "subdir" }).first();

    // Initially collapsed
    await expect(subdirRow.locator(".caret")).not.toHaveClass(/open/);

    // Click the folder icon to expand
    await subdirRow.locator(".icon").click();
    await expect(subdirRow.locator(".caret")).toHaveClass(/open/);
    await expect(sidebar.locator(".tree-item .label", { hasText: "nested.md" })).toBeVisible();

    // Click the folder icon again to collapse
    await subdirRow.locator(".icon").click();
    await expect(subdirRow.locator(".caret")).not.toHaveClass(/open/);
    await expect(sidebar.locator(".tree-item .label", { hasText: "nested.md" })).not.toBeVisible();
  });

  test("clicking selected folder toggles expand/collapse instead of navigating", async ({ page }) => {
    await page.goto("/");
    const sidebar = page.locator("#tree-container");
    const subdirRow = sidebar.locator(".tree-item", { hasText: "subdir" }).first();

    // Click folder label to navigate (folder becomes selected and expanded)
    await subdirRow.locator(".label").click();
    await expect(subdirRow).toHaveClass(/active/);
    await expect(subdirRow.locator(".caret")).toHaveClass(/open/);
    await expect(page.locator("#content-area .dir-listing")).toBeVisible();

    // Click folder label again - should collapse (since already selected)
    await subdirRow.locator(".label").click();
    await expect(subdirRow.locator(".caret")).not.toHaveClass(/open/);

    // Click folder label again - should expand
    await subdirRow.locator(".label").click();
    await expect(subdirRow.locator(".caret")).toHaveClass(/open/);
  });

  test("active folder is highlighted in sidebar", async ({ page }) => {
    await page.goto("/");
    const sidebar = page.locator("#tree-container");
    const subdirRow = sidebar.locator(".tree-item", { hasText: "subdir" }).first();

    // Initially no folder is active
    await expect(subdirRow).not.toHaveClass(/active/);

    // Navigate to folder
    await subdirRow.locator(".label").click();
    await expect(subdirRow).toHaveClass(/active/);

    // Navigate to a file - folder should no longer be active
    await sidebar.locator(".tree-item .label", { hasText: "nested.md" }).click();
    await expect(subdirRow).not.toHaveClass(/active/);
    await expect(sidebar.locator(".tree-item", { hasText: "nested.md" })).toHaveClass(/active/);
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

  test("CSV — toggle between Table and Records view", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "data.csv" }).click();
    const content = page.locator("#content-area");

    // Toggle should be visible with Table and Records buttons
    await expect(page.locator(".view-toggle")).toBeVisible();
    await expect(page.locator(".view-toggle button", { hasText: "Table" })).toBeVisible();
    await expect(page.locator(".view-toggle button", { hasText: "Records" })).toBeVisible();

    // Table view is active by default
    await expect(page.locator(".view-toggle button", { hasText: "Table" })).toHaveClass(/active/);
    await expect(content.locator(".csv-table")).toBeVisible();

    // Click Records to switch to records view
    await page.locator(".view-toggle button", { hasText: "Records" }).click();
    await expect(content.locator(".csv-records")).toBeVisible();
    await expect(content.locator(".csv-record-card")).toHaveCount(3); // Alice, Bob, Charlie
    await expect(content.locator(".csv-record-card .record-header", { hasText: "Record 1" })).toBeVisible();
    // Each record card has field labels and values - check at least one exists
    await expect(content.locator(".csv-record-card .field-label", { hasText: "name" }).first()).toBeVisible();
    await expect(content.locator(".csv-record-card .field-value", { hasText: "Alice" })).toBeVisible();

    // URL should have ?view=records
    await expect(page).toHaveURL(/\?view=records/);

    // Click Table to switch back
    await page.locator(".view-toggle button", { hasText: "Table" }).click();
    await expect(content.locator(".csv-table")).toBeVisible();
    await expect(content.locator(".csv-records")).not.toBeVisible();
  });

  test("CSV — direct navigation with ?view=records loads records view", async ({ page }) => {
    await page.goto("/data.csv?view=records");
    const content = page.locator("#content-area");
    await expect(content.locator(".csv-records")).toBeVisible();
    await expect(page.locator(".view-toggle button", { hasText: "Records" })).toHaveClass(/active/);
  });

  test("CSV — clicking row link switches to Records view and scrolls to that record", async ({ page }) => {
    await page.goto("/data.csv");
    const content = page.locator("#content-area");

    // Table view should be visible with row link buttons
    await expect(content.locator(".csv-table")).toBeVisible();
    await expect(content.locator(".csv-record-link-btn").first()).toBeVisible();

    // Click the second row's link button (data-row="2")
    await content.locator('.csv-record-link-btn[data-row="2"]').click();

    // Should switch to Records view
    await expect(content.locator(".csv-records")).toBeVisible();
    await expect(page.locator(".view-toggle button", { hasText: "Records" })).toHaveClass(/active/);

    // URL should have ?view=records&r=2
    await expect(page).toHaveURL(/\?view=records&r=2/);

    // Record 2 should be visible
    await expect(content.locator(".csv-record-card#record-2")).toBeVisible();
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

test.describe("Pretty-printed files", () => {
  test("JSON — pretty-printed and syntax-highlighted", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "data.json" }).click();
    const content = page.locator("#content-area");
    await expect(content.locator(".source-code")).toBeVisible();
    // Pretty-printed JSON should show "name" key
    await expect(content.locator(".source-code", { hasText: "name" })).toBeVisible();
    await expect(content.locator(".source-code", { hasText: "Alice" })).toBeVisible();
  });

  test("YAML — pretty-printed and syntax-highlighted", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "config.yaml" }).click();
    const content = page.locator("#content-area");
    await expect(content.locator(".source-code")).toBeVisible();
    await expect(content.locator(".source-code", { hasText: "server" })).toBeVisible();
    await expect(content.locator(".source-code", { hasText: "database" })).toBeVisible();
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

  test("spaces in query act as separate terms", async ({ page }) => {
    await page.goto("/");
    await expect(page.locator("#tree-container .tree-item")).not.toHaveCount(0);
    await page.keyboard.press("t");
    await expect(page.locator("#search-overlay")).toHaveClass(/open/);
    // "sub nes" should match "subdir/nested.md" — each term matched independently
    await page.locator("#search-input").fill("sub nes");
    await expect(page.locator(".search-result").first()).toBeVisible();
    await expect(page.locator(".search-result .sr-path", { hasText: "nested" })).toBeVisible();
  });
});

test.describe("Full-text search", () => {
  test("press '/', search contents, open result and see the hit highlighted", async ({ page }) => {
    await page.goto("/");
    await expect(page.locator("#tree-container .tree-item")).not.toHaveCount(0);
    await page.keyboard.press("/");
    await expect(page.locator("#search-overlay")).toHaveClass(/open/);
    await expect(page.locator('#search-modes button[data-mode="content"]')).toHaveClass(/active/);
    await expect(page.locator("#search-input")).toHaveAttribute("placeholder", /contents/);

    // Two words that only co-occur in hello.md's content (not its name)
    await page.locator("#search-input").fill("alice bob");
    const result = page.locator(".content-result", { has: page.locator(".sr-path", { hasText: "hello.md" }) });
    await expect(result).toBeVisible();
    await expect(result.locator(".cr-snippet mark").first()).toHaveText(/alice|bob/i);
    await expect(result.locator(".cr-count")).toHaveText("2 matches");
    // nested.md doesn't contain these words
    await expect(page.locator(".content-result .sr-path", { hasText: "nested.md" })).toHaveCount(0);

    // data.csv also contains both names; it ranks first (same count, path order)
    await expect(page.locator(".content-result").first()).toHaveAttribute("data-path", "data.csv");
    await result.click();
    await expect(page.locator("#search-overlay")).not.toHaveClass(/open/);
    await expect(page).toHaveURL(/\/hello\.md$/);
    await expect(page.locator("#content-area mark.search-hit")).toHaveText(/alice|bob/i);
  });

  test("quoted phrase matches exact phrase only", async ({ page }) => {
    await page.goto("/");
    await page.keyboard.press("/");
    await page.locator("#search-input").fill('"inside a subdirectory"');
    await expect(page.locator(".content-result .sr-path", { hasText: "nested.md" })).toBeVisible();
    await page.locator("#search-input").fill('"subdirectory inside"');
    await expect(page.locator("#search-empty")).toHaveText("No notes match");
  });

  test("Tab switches between Files and Contents modes", async ({ page }) => {
    await page.goto("/");
    await expect(page.locator("#tree-container .tree-item")).not.toHaveCount(0);
    await page.keyboard.press("/");
    await page.locator("#search-input").fill("nested");
    await expect(page.locator(".content-result").first()).toBeVisible();

    await page.locator("#search-input").press("Tab");
    await expect(page.locator('#search-modes button[data-mode="files"]')).toHaveClass(/active/);
    await expect(page.locator("#search-input")).toHaveAttribute("placeholder", "Go to file…");
    // Same query is re-run as a file-name search
    await expect(page.locator(".search-result:not(.content-result) .sr-path", { hasText: "subdir/nested.md" })).toBeVisible();
    await expect(page.locator(".content-result")).toHaveCount(0);

    await page.locator("#search-input").press("Tab");
    await expect(page.locator(".content-result").first()).toBeVisible();
  });

  test("short query shows a hint instead of searching", async ({ page }) => {
    await page.goto("/");
    await page.keyboard.press("/");
    await page.locator("#search-input").fill("a");
    await expect(page.locator("#search-empty")).toHaveText("Keep typing…");
  });

  test("search button in the top bar opens content search on mobile", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/");
    await expect(page.locator("#search-btn")).toBeVisible();
    await page.locator("#search-btn").click();
    await expect(page.locator("#search-overlay")).toHaveClass(/open/);
    await expect(page.locator('#search-modes button[data-mode="content"]')).toHaveClass(/active/);
    await page.locator("#search-input").fill("subdirectory");
    await page.locator(".content-result").first().click();
    await expect(page).toHaveURL(/\/subdir\/nested\.md$/);
    await expect(page.locator("#content-area mark.search-hit")).toBeInViewport();
  });

  test("'t' still opens file search in Files mode", async ({ page }) => {
    await page.goto("/");
    await page.keyboard.press("/");
    await page.keyboard.press("Escape");
    await page.keyboard.press("t");
    await expect(page.locator('#search-modes button[data-mode="files"]')).toHaveClass(/active/);
    await expect(page.locator(".search-result").first()).toBeVisible();
  });
});

test.describe("Backlinks", () => {
  test("note shows notes that link to it, and clicking reveals the link", async ({ page }) => {
    await page.goto("/hello.md");
    const section = page.locator("#content-area .backlinks");
    await expect(section).toBeVisible();
    await expect(section.locator(".backlinks-count")).toHaveText("1");
    const item = section.locator(".backlink-item");
    await expect(item).toHaveCount(1);
    await expect(item).toHaveAttribute("data-path", "wiki-test.md");
    await expect(item.locator(".bl-name")).toHaveText("wiki-test");
    await expect(item.locator(".bl-snippet").first()).toContainText("Simple link: [[hello]]");
    // Section sits below the note body
    await expect(page.locator("#content-area .markdown-body + .backlinks")).toHaveCount(1);

    await item.click();
    await expect(page).toHaveURL(/\/wiki-test\.md$/);
    const hit = page.locator("#content-area a.backlink-hit");
    await expect(hit).toHaveCount(1);
    await expect(hit).toHaveAttribute("href", "/hello.md");
    await expect(hit).toBeInViewport();
  });

  test("works for notes in subfolders", async ({ page }) => {
    await page.goto("/subdir/nested.md");
    const item = page.locator("#content-area .backlinks .backlink-item");
    await expect(item).toHaveAttribute("data-path", "wiki-test.md");
    await expect(item.locator(".bl-snippet")).toContainText("[[subdir/nested]]");
  });

  test("no section when nothing links to the note", async ({ page }) => {
    await page.goto("/frontmatter.md");
    await expect(page.locator("#content-area .markdown-body")).toBeVisible();
    // Give the backlinks request time to land before asserting absence
    await page.waitForLoadState("networkidle");
    await expect(page.locator("#content-area .backlinks")).toHaveCount(0);
  });

  test("not shown in Code view, reappears in Preview", async ({ page }) => {
    await page.goto("/hello.md");
    await expect(page.locator("#content-area .backlinks")).toBeVisible();
    await page.locator(".view-toggle button", { hasText: "Code" }).click();
    await expect(page.locator("#content-area .source-code")).toBeVisible();
    await expect(page.locator("#content-area .backlinks")).toHaveCount(0);
    await page.locator(".view-toggle button", { hasText: "Preview" }).click();
    await expect(page.locator("#content-area .backlinks")).toBeVisible();
  });

  test("backlinks from a previous note don't leak into the next one", async ({ page }) => {
    await page.goto("/hello.md");
    await expect(page.locator("#content-area .backlinks")).toBeVisible();
    await page.locator(".tree-item .label", { hasText: "frontmatter.md" }).click();
    await expect(page).toHaveURL(/\/frontmatter\.md$/);
    await page.waitForLoadState("networkidle");
    await expect(page.locator("#content-area .backlinks")).toHaveCount(0);
  });
});

test.describe("Anchor links", () => {
  test("headings have id attributes", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "hello.md" }).click();
    const content = page.locator("#content-area .markdown-body");
    // Wait for hello.md's specific h1 content, not just any h1
    await expect(content.locator("h1", { hasText: "Hello World" })).toBeVisible();
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
    await expect(page).toHaveTitle("testdata - RepoView");
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
    await expect(page).toHaveTitle("testdata - RepoView");
  });
});

test.describe("Markdown raw/preview toggle", () => {
  test("toggle appears for markdown files, not for non-markdown", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "hello.md" }).click();
    await expect(page.locator(".view-toggle")).toBeVisible();
    await expect(page.locator(".view-toggle button", { hasText: "Preview" })).toBeVisible();
    await expect(page.locator(".view-toggle button", { hasText: "Code" })).toBeVisible();

    // Navigate to a non-markdown file — toggle should disappear
    await page.locator(".tree-item .label", { hasText: "sample.go" }).click();
    await expect(page.locator(".source-code")).toBeVisible();
    await expect(page.locator(".view-toggle")).not.toBeVisible();
  });

  test("clicking Code shows source, clicking Preview restores rendered markdown", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "hello.md" }).click();
    await expect(page.locator("#content-area .markdown-body")).toBeVisible();

    // Switch to code view
    await page.locator(".view-toggle button", { hasText: "Code" }).click();
    await expect(page.locator("#content-area .source-code")).toBeVisible();
    await expect(page.locator("#content-area .markdown-body")).not.toBeVisible();

    // Switch back to preview
    await page.locator(".view-toggle button", { hasText: "Preview" }).click();
    await expect(page.locator("#content-area .markdown-body")).toBeVisible();
    await expect(page.locator("#content-area .source-code")).not.toBeVisible();
  });

  test("toggle disappears when navigating to non-toggle file", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "hello.md" }).click();
    await expect(page.locator(".view-toggle")).toBeVisible();

    // Navigate to a plain text file (no toggle) - CSV files now have Table/Records toggle
    await page.locator(".tree-item .label", { hasText: "example.txt" }).click();
    await expect(page.locator(".source-code")).toBeVisible();
    await expect(page.locator(".view-toggle")).not.toBeVisible();
  });

  test("URL updates with ?view=code when switching to code view", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "hello.md" }).click();
    await expect(page.locator(".view-toggle")).toBeVisible();

    await page.locator(".view-toggle button", { hasText: "Code" }).click();
    expect(page.url()).toContain("?view=code");

    // Switch back — param should be gone
    await page.locator(".view-toggle button", { hasText: "Preview" }).click();
    expect(page.url()).not.toContain("view=code");
  });

  test("direct navigation with ?view=code loads code view", async ({ page }) => {
    await page.goto("/hello.md?view=code");
    await expect(page.locator("#content-area .source-code")).toBeVisible();
    await expect(page.locator("#content-area .markdown-body")).not.toBeVisible();
    // Toggle should show Code as active
    await expect(page.locator(".view-toggle button.active", { hasText: "Code" })).toBeVisible();
  });
});

test.describe("Binary file handling", () => {
  test("binary image shows inline img with correct src", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "pixel.png" }).click();
    const content = page.locator("#content-area");
    await expect(content.locator(".binary-preview img")).toBeVisible();
    const src = await content.locator(".binary-preview img").getAttribute("src");
    expect(src).toContain("/api/raw?path=pixel.png");
    await expect(content.locator(".binary-download")).toBeVisible();
  });

  test("SVG shows inline preview with toggle to code view", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "icon.svg" }).click();
    const content = page.locator("#content-area");
    // Preview mode: shows inline SVG element
    await expect(content.locator(".svg-preview svg")).toBeVisible();
    // Toggle should be visible
    await expect(page.locator(".view-toggle")).toBeVisible();
    await expect(page.locator(".view-toggle button", { hasText: "Preview" })).toBeVisible();
    await expect(page.locator(".view-toggle button", { hasText: "Code" })).toBeVisible();

    // Switch to code view
    await page.locator(".view-toggle button", { hasText: "Code" }).click();
    await expect(content.locator(".source-code")).toBeVisible();
    await expect(content.locator(".svg-preview")).not.toBeVisible();

    // Switch back to preview
    await page.locator(".view-toggle button", { hasText: "Preview" }).click();
    await expect(content.locator(".svg-preview svg")).toBeVisible();
    await expect(content.locator(".source-code")).not.toBeVisible();
  });

  test("HTML shows rendered preview iframe with toggle to code view", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "sample.html" }).click();
    const content = page.locator("#content-area");
    // Preview mode: rendered page inside a sandboxed iframe
    await expect(content.locator("iframe.html-preview")).toBeVisible();
    const frame = page.frameLocator("iframe.html-preview");
    await expect(frame.locator("h1")).toHaveText("Hello from HTML");

    // Switch to code view
    await page.locator(".view-toggle button", { hasText: "Code" }).click();
    await expect(content.locator(".source-code")).toBeVisible();
    await expect(content.locator(".source-code")).toContainText("<h1>Hello from HTML</h1>");
    await expect(content.locator("iframe.html-preview")).toHaveCount(0);

    // Switch back to preview
    await page.locator(".view-toggle button", { hasText: "Preview" }).click();
    await expect(page.frameLocator("iframe.html-preview").locator("h1")).toHaveText("Hello from HTML");
  });

  test("non-image binary shows 'Binary file not displayed' with download link", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "tiny.bin" }).click();
    const content = page.locator("#content-area");
    await expect(content.locator(".binary-info")).toBeVisible();
    await expect(content.locator("text=Binary file not displayed")).toBeVisible();
    await expect(content.locator(".binary-download")).toBeVisible();
    const href = await content.locator(".binary-download").getAttribute("href");
    expect(href).toContain("/api/raw?path=tiny.bin");
  });
});

test.describe("README below directory listing", () => {
  test("renders README.md below the file listing in a directory", async ({ page }) => {
    await page.goto("/");
    await page.locator(".tree-item .label", { hasText: "subdir" }).click();
    const content = page.locator("#content-area");
    await expect(content.locator(".dir-listing")).toBeVisible();
    await expect(content.locator(".readme-container")).toBeVisible();
    await expect(content.locator(".readme-header")).toHaveText("README.md");
    await expect(content.locator(".readme-body h1", { hasText: "Subdir README" })).toBeVisible();
  });

  test("renders README.md on initial page load at root", async ({ page }) => {
    await page.goto("/");
    const content = page.locator("#content-area");
    await expect(content.locator(".dir-listing")).toBeVisible();
    await expect(content.locator(".readme-container")).toBeVisible();
    await expect(content.locator(".readme-header")).toHaveText("README.md");
    await expect(content.locator(".readme-body h1", { hasText: "Root README" })).toBeVisible();
  });

  test("renders README.md after browser back navigation", async ({ page }) => {
    // Navigate directly to subdir via URL
    await page.goto("/subdir");
    await expect(page.locator("#content-area .readme-body h1", { hasText: "Subdir README" })).toBeVisible();
    // Navigate to a file within the directory listing
    await page.locator(".dir-listing-row .name", { hasText: "nested.md" }).click();
    await expect(page.locator("#content-area .markdown-body h1", { hasText: "Nested" })).toBeVisible();
    // Go back to subdir using browser back
    await page.goBack();
    const content = page.locator("#content-area");
    await expect(content.locator(".dir-listing")).toBeVisible();
    await expect(content.locator(".readme-container")).toBeVisible();
    await expect(content.locator(".readme-body h1", { hasText: "Subdir README" })).toBeVisible();
  });

  test("renders README.md on direct URL navigation to directory", async ({ page }) => {
    await page.goto("/subdir");
    const content = page.locator("#content-area");
    await expect(content.locator(".dir-listing")).toBeVisible();
    await expect(content.locator(".readme-container")).toBeVisible();
    await expect(content.locator(".readme-header")).toHaveText("README.md");
    await expect(content.locator(".readme-body h1", { hasText: "Subdir README" })).toBeVisible();
  });
});

test.describe("Markdown image rewriting", () => {
  test("local image src is rewritten to /api/raw", async ({ page }) => {
    await page.goto("/images.md");
    const content = page.locator("#content-area .markdown-body");
    await expect(content).toBeVisible();

    // Relative image (pixel.png) should be rewritten
    const relImg = content.locator('img[alt="pixel"]');
    await expect(relImg).toBeVisible();
    const relSrc = await relImg.getAttribute("src");
    expect(relSrc).toContain("/api/raw?path=pixel.png");

    // Dot-relative image (./pixel.png) should also be rewritten
    const dotImg = content.locator('img[alt="pixel dot"]');
    await expect(dotImg).toBeVisible();
    const dotSrc = await dotImg.getAttribute("src");
    expect(dotSrc).toContain("/api/raw?path=pixel.png");

    // External image should NOT be rewritten
    const extImg = content.locator('img[alt="external"]');
    await expect(extImg).toBeVisible();
    const extSrc = await extImg.getAttribute("src");
    expect(extSrc).toBe("https://example.com/logo.png");
  });

  test("rewritten image actually loads via /api/raw", async ({ page }) => {
    await page.goto("/images.md");
    const content = page.locator("#content-area .markdown-body");
    await expect(content).toBeVisible();

    const img = content.locator('img[alt="pixel"]');
    await expect(img).toBeVisible();
    // Verify the image loaded successfully (naturalWidth > 0)
    const loaded = await img.evaluate(
      (el: HTMLImageElement) => el.complete && el.naturalWidth > 0
    );
    expect(loaded).toBe(true);
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

test.describe("Connection error handling", () => {
  test("connection banner is hidden by default", async ({ page }) => {
    await page.goto("/");
    await expect(page.locator("#connection-banner")).not.toHaveClass(/show/);
  });

  test("connection banner exists with retry button", async ({ page }) => {
    await page.goto("/");
    // Banner element exists but is hidden
    await expect(page.locator("#connection-banner")).toBeAttached();
    await expect(page.locator("#connection-banner .retry-btn")).toBeAttached();
    await expect(page.locator("#connection-banner .banner-message")).toBeAttached();
  });

  test("failed file fetch shows connection error banner", async ({ page }) => {
    await page.goto("/");
    // Wait for initial load
    await expect(page.locator("#tree-container .tree-item")).not.toHaveCount(0);

    // Mock fetch to fail
    await page.evaluate(() => {
      (window as any).originalFetch = window.fetch;
      window.fetch = () => Promise.reject(new Error("Network error"));
    });

    // Try to load a file - should show error
    await page.locator(".tree-item .label", { hasText: "hello.md" }).click();
    await expect(page.locator("#connection-banner")).toHaveClass(/show/);
    await expect(page.locator("#content-area")).toContainText("Unable to load file");

    // Restore fetch
    await page.evaluate(() => {
      window.fetch = (window as any).originalFetch;
    });
  });

  test("failed tree fetch shows connection error banner", async ({ page }) => {
    await page.goto("/");
    // Wait for initial load
    await expect(page.locator("#tree-container .tree-item")).not.toHaveCount(0);

    // Mock fetch to fail
    await page.evaluate(() => {
      (window as any).originalFetch = window.fetch;
      window.fetch = () => Promise.reject(new Error("Network error"));
    });

    // Click a folder to trigger tree fetch - should show error
    await page.locator(".tree-item .label", { hasText: "subdir" }).click();
    await expect(page.locator("#connection-banner")).toHaveClass(/show/);

    // Restore fetch
    await page.evaluate(() => {
      window.fetch = (window as any).originalFetch;
    });
  });
});

test.describe("Wiki links", () => {
  test("wiki link with mixed-case heading navigates and scrolls to target", async ({ page }) => {
    await page.goto("/wiki-test.md");
    const content = page.locator("#content-area .markdown-body");
    await expect(content.locator("h1", { hasText: "Wiki Link Test" })).toBeVisible();

    // Find the wiki link with mixed case heading
    const wikiLink = content.locator('a.wiki-link', { hasText: "Jump to Code Block" });
    await expect(wikiLink).toBeVisible();

    // Verify the href is correctly slugified (lowercase, hyphenated)
    await expect(wikiLink).toHaveAttribute("href", "/hello.md#code-block");

    // Click the link
    await wikiLink.click();

    // Should navigate to hello.md and the Code Block heading should be visible
    await expect(page.locator("#content-area .markdown-body h2#code-block")).toBeVisible();

    // URL should include the hash
    expect(page.url()).toContain("/hello.md#code-block");
  });

  test("wiki link href is properly slugified", async ({ page }) => {
    await page.goto("/wiki-test.md");
    const content = page.locator("#content-area .markdown-body");
    await expect(content.locator("h1", { hasText: "Wiki Link Test" })).toBeVisible();

    // Check that heading anchors in hrefs are slugified (wait for resolution)
    const linkWithHeading = content.locator('a.wiki-link[data-wiki-heading="Code Block"]');
    // Wait for the link to be resolved (href changes from "#" to actual path)
    await expect(linkWithHeading).toHaveAttribute("href", /^\/hello\.md#/);

    const href = await linkWithHeading.getAttribute("href");
    // href should have lowercase, hyphenated anchor
    expect(href).toBe("/hello.md#code-block");
  });
});

test.describe("PWA (Add to Home Screen)", () => {
  test("manifest is linked and lists icons that load", async ({ page, request }) => {
    await page.goto("/");
    const href = await page.locator('link[rel="manifest"]').getAttribute("href");
    expect(href).toBe("/api/manifest.webmanifest");

    const res = await request.get(href!);
    expect(res.ok()).toBeTruthy();
    const manifest = await res.json();
    expect(manifest.display).toBe("standalone");
    expect(manifest.short_name).toBe("testdata");
    for (const icon of manifest.icons) {
      const iconRes = await request.get(icon.src);
      expect(iconRes.ok()).toBeTruthy();
      expect(iconRes.headers()["content-type"]).toBe("image/png");
    }

    const touchIcon = await page.locator('link[rel="apple-touch-icon"]').getAttribute("href");
    expect((await request.get(touchIcon!)).ok()).toBeTruthy();
  });

  test("theme-color meta follows the light/dark toggle", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "light" });
    await page.goto("/");
    await page.evaluate(() => localStorage.removeItem("rv-theme"));
    await page.reload();
    const meta = page.locator('meta[name="theme-color"]');
    await expect(meta).toHaveAttribute("content", "#ffffff");

    await page.locator("#theme-toggle").click();
    await expect(meta).toHaveAttribute("content", "#1e1e2e");

    // Persisted dark theme is applied on load, before any toggle.
    await page.reload();
    await expect(meta).toHaveAttribute("content", "#1e1e2e");

    await page.locator("#theme-toggle").click();
    await expect(meta).toHaveAttribute("content", "#ffffff");
  });

  test("an unrelated user file is still routed to the SPA, not shadowed", async ({ page }) => {
    await page.goto("/hello.md");
    await expect(page.locator("#content-area")).toContainText("Hello");
  });
});
