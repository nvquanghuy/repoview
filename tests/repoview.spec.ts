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
