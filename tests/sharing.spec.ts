import { test, expect, Page, Browser } from "@playwright/test";

// Runs against the "auth" project: a server started with REPOVIEW_PASSWORD.
const OWNER_PASSWORD = "test-owner-pw";

async function submitPassword(page: Page, password: string) {
  await page.locator("input[name=password]").fill(password);
  await page
    .locator('button[type=submit], input[type=submit]')
    .first()
    .click();
}

async function login(page: Page, next = "/") {
  await page.goto(next);
  await expect(page).toHaveURL(/\/login/);
  await submitPassword(page, OWNER_PASSWORD);
  await expect(page.locator("#tree-container .tree-item").first()).toBeVisible();
}

// Opens the share dialog for whatever is on screen, creates a link and
// returns its absolute URL.
async function createShare(
  page: Page,
  opts: { password?: string; expiry?: string } = {},
): Promise<string> {
  await page.locator("#share-btn").click();
  const dialog = page.locator("#share-overlay");
  await expect(dialog).toBeVisible();
  // A random password is suggested.
  await expect(dialog.locator("#share-password")).toHaveValue(/^[A-Za-z0-9]{16}$/);
  await dialog.locator("#share-password").fill(opts.password ?? "");
  if (opts.expiry) await dialog.locator("#share-expiry").selectOption(opts.expiry);
  await dialog.locator("#share-create-btn").click();
  const urlInput = dialog.locator("#share-result-url");
  await expect(urlInput).toBeVisible();
  const url = await urlInput.inputValue();
  expect(url).toMatch(/^http:\/\/localhost:3124\/s\/[^/]+$/);
  return url;
}

async function freshPage(browser: Browser) {
  const ctx = await browser.newContext();
  return { ctx, page: await ctx.newPage() };
}

test.describe("Owner login", () => {
  test("unauthenticated / redirects to /login", async ({ page }) => {
    await page.goto("/");
    await expect(page).toHaveURL(/\/login/);
    await expect(page.locator("input[name=password]")).toBeVisible();
  });

  test("API returns 401 without a session", async ({ request }) => {
    const res = await request.get("/api/tree?path=.");
    expect(res.status()).toBe(401);
  });

  test("wrong password is rejected, right password logs in", async ({ page }) => {
    await page.goto("/hello.md");
    await expect(page).toHaveURL(/\/login/);
    await submitPassword(page, "not-the-password");
    await expect(page).toHaveURL(/\/login/);
    await expect(page.locator("input[name=password]")).toBeVisible();

    await submitPassword(page, OWNER_PASSWORD);
    // `next` brings us back to the page we asked for.
    await expect(page).toHaveURL(/\/hello\.md$/);
    await expect(page.locator(".markdown-body")).toBeVisible();
    await expect(page.locator("#share-btn")).toBeVisible();
    await expect(page.locator("#shares-btn")).toBeVisible();
    await expect(page.locator("#logout-form button")).toBeVisible();
  });

  test("logout ends the session", async ({ page }) => {
    await login(page);
    await page.locator("#logout-form button").click();
    await expect(page).toHaveURL(/\/login/);
    await page.goto("/");
    await expect(page).toHaveURL(/\/login/);
  });
});

test.describe("Sharing", () => {
  test("password-protected folder share only exposes that folder", async ({ page, browser }) => {
    await login(page, "/subdir/");
    await expect(page.locator(".dir-listing-row", { hasText: "nested.md" }).first()).toBeVisible();
    await page.locator("#share-btn").click();
    await expect(page.locator("#share-overlay .share-target-path").first()).toHaveText("subdir");
    await page.keyboard.press("Escape");
    await expect(page.locator("#share-overlay")).toBeHidden();

    const url = await createShare(page, { password: "sub-pw-123", expiry: "86400" });
    await expect(page.locator("#share-result-password")).toHaveValue("sub-pw-123");
    await expect(page.locator("#share-result-note")).toContainText("can't be shown again");

    const { ctx, page: guest } = await freshPage(browser);
    await guest.goto(url);
    await expect(guest.locator("input[name=password]")).toBeVisible();
    await submitPassword(guest, "wrong");
    await expect(guest.getByText("Incorrect password.")).toBeVisible();
    await expect(guest.locator("input[name=password]")).toBeVisible();
    await submitPassword(guest, "sub-pw-123");

    const tree = guest.locator("#tree-container .tree-item");
    await expect(tree.first()).toBeVisible();
    await expect(tree).toHaveCount(2);
    await expect(tree.filter({ hasText: "hello.md" })).toHaveCount(0);
    const nested = tree.filter({ hasText: "nested.md" });
    await expect(nested).toHaveCount(1);
    await expect(guest.locator("#breadcrumb-bar .crumb.root")).toHaveText("subdir");

    // Owner-only UI is gone.
    await expect(guest.locator("#share-btn")).toBeHidden();
    await expect(guest.locator("#shares-btn")).toBeHidden();
    await expect(guest.locator("#logout-form")).toBeHidden();

    // Opening a file keeps URLs under the share base.
    await nested.click();
    await expect(guest.locator(".markdown-body")).toBeVisible();
    const base = new URL(url).pathname;
    expect(new URL(guest.url()).pathname).toBe(base + "/nested.md");

    // Root-relative links resolve against the share root.
    const readmeLink = guest.locator(".markdown-body a", { hasText: "subdir readme" });
    await expect(readmeLink).toHaveAttribute("href", base + "/README.md");
    await readmeLink.click();
    await expect(guest.locator(".markdown-body h1")).toHaveText("Subdir README");
    await guest.goBack();

    // Reload lands on the same file.
    await guest.reload();
    await expect(guest.locator(".markdown-body")).toBeVisible();

    // Escaping the share is refused.
    const escape = await guest.request.get(url + "/api/raw?path=../hello.md");
    expect(escape.status()).toBeGreaterThanOrEqual(400);
    expect(escape.status()).toBeLessThan(500);
    const owner = await guest.request.get("/api/tree?path=.");
    expect(owner.status()).toBe(401);

    await ctx.close();
  });

  test("single-file share shows the file without a sidebar", async ({ page, browser }) => {
    await login(page, "/hello.md");
    await expect(page.locator(".markdown-body")).toBeVisible();
    await page.locator("#share-btn").click();
    await expect(page.locator("#share-overlay .share-target-path").first()).toHaveText("hello.md");
    await page.locator("#share-password").fill("");
    await expect(page.locator("#share-password-hint")).toContainText("anyone with the link");
    await page.keyboard.press("Escape");

    const url = await createShare(page, { password: "", expiry: "3600" });
    await expect(page.locator("#share-result-password-block")).toBeHidden();

    const { ctx, page: guest } = await freshPage(browser);
    await guest.goto(url);
    await expect(guest.locator(".markdown-body")).toBeVisible();
    await expect(guest.locator(".markdown-body h1").first()).toBeVisible();
    await expect(guest.locator("#sidebar")).toBeHidden();
    await expect(guest.locator("#menu-toggle")).toBeHidden();
    await expect(guest.locator("#share-btn")).toBeHidden();
    await ctx.close();
  });

  test("custom expiry and manage panel listing", async ({ page }) => {
    await login(page);
    await page.locator("#share-btn").click();
    await page.locator("#share-expiry").selectOption("custom");
    await expect(page.locator("#share-custom-amount")).toBeVisible();
    await page.locator("#share-custom-amount").fill("2");
    await page.locator("#share-custom-unit").selectOption("3600");
    await page.locator("#share-create-btn").click();
    await expect(page.locator("#share-result-url")).toHaveValue(/\/s\//);
    const url = await page.locator("#share-result-url").inputValue();
    await expect(page.locator("#share-result-note")).toContainText("in 2 hours");
    await page.locator("#share-result button", { hasText: "Done" }).click();

    await page.locator("#shares-btn").click();
    const id = url.slice(-22);
    const row = page.locator(`.share-row[data-id="${id}"]`);
    await expect(row).toBeVisible();
    await expect(row).toContainText("Password");
    await expect(row.locator(".share-row-expiry")).toContainText("in 2 hours");
  });

  test("one-click link opens a password share without typing", async ({ page, browser }) => {
    await login(page, "/subdir/");
    // Characters that need encoding survive the trip through the fragment.
    const pw = "p@ss #1 &k=é/?";
    const url = await createShare(page, { password: pw });
    const oneClick = page.locator("#share-result-oneclick");
    await expect(oneClick).toHaveValue(url + "#k=" + encodeURIComponent(pw));
    await expect(page.locator("#share-result-oneclick-hint")).toContainText("Anyone with this link");

    const { ctx, page: guest } = await freshPage(browser);
    await guest.goto(await oneClick.inputValue());
    await expect(guest.locator("#tree-container .tree-item").first()).toBeVisible();
    await expect(guest.locator("input[name=password]")).toHaveCount(0);
    // The key is gone from the address bar and from history.
    expect(guest.url()).not.toContain("#");
    expect(guest.url()).not.toContain("k=");
    await guest.goBack().catch(() => {});
    expect(guest.url()).not.toContain("k=");

    // Already unlocked: opening the one-click link again strips the key too.
    await guest.goto(await oneClick.inputValue());
    await expect(guest.locator("#tree-container .tree-item").first()).toBeVisible();
    expect(guest.url()).not.toContain("k=");
    await ctx.close();

    // The owner opening their own one-click link also gets a clean URL.
    await page.goto(await oneClick.inputValue());
    await expect(page.locator("#tree-container .tree-item").first()).toBeVisible();
    expect(page.url()).not.toContain("k=");
  });

  test("one-click link with a wrong key falls back to the prompt", async ({ page, browser }) => {
    await login(page, "/subdir/");
    const url = await createShare(page, { password: "right-key-1" });
    const { ctx, page: guest } = await freshPage(browser);
    await guest.goto(url + "#k=wrong-key");
    await expect(guest.getByText("Incorrect password.")).toBeVisible();
    await expect(guest.locator("input[name=password]")).toBeVisible();
    expect(guest.url()).not.toContain("wrong-key");
    // No resubmit loop: the prompt stays put.
    await guest.waitForTimeout(500);
    await expect(guest.getByText("Incorrect password.")).toBeVisible();
    await submitPassword(guest, "right-key-1");
    await expect(guest.locator("#tree-container .tree-item").first()).toBeVisible();
    await ctx.close();
  });

  test("no one-click link without a password", async ({ page }) => {
    await login(page, "/hello.md");
    await createShare(page, { password: "" });
    await expect(page.locator("#share-result-oneclick-block")).toBeHidden();
    await expect(page.locator("#share-result-invite-btn")).toBeHidden();
  });

  test("copy invite puts the link and password on the clipboard", async ({ page, context }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await login(page, "/subdir/");
    const url = await createShare(page, { password: "invite-pw-9" });
    await page.locator("#share-result-invite-btn").click();
    const text = await page.evaluate(() => navigator.clipboard.readText());
    expect(text).toContain(url);
    expect(text).toContain("invite-pw-9");
    expect(text).not.toContain("#k=");
    expect(text).toContain("subdir");
  });

  test("revoking a share makes its link stop working", async ({ page, browser }) => {
    await login(page, "/data.csv");
    const url = await createShare(page, { password: "" });
    await page.keyboard.press("Escape");

    const { ctx, page: guest } = await freshPage(browser);
    const before = await guest.goto(url);
    expect(before!.status()).toBeLessThan(400);

    await page.locator("#shares-btn").click();
    const id = url.slice(-22);
    const row = page.locator(`.share-row[data-id="${id}"]`);
    await expect(row).toBeVisible();
    page.once("dialog", (d) => d.accept());
    await row.locator(".share-revoke-btn").click();
    await expect(row).toHaveCount(0);

    const after = await guest.goto(url);
    expect(after!.status()).toBeGreaterThanOrEqual(400);
    const api = await guest.request.get(new URL(url).pathname + "/api/tree?path=.");
    expect(api.status()).toBeGreaterThanOrEqual(400);
    await ctx.close();
  });
});

// Sandboxed HTML previews can't send cookies; their relative assets must
// still load for the owner and in password-protected shares.
test.describe("HTML previews with relative assets", () => {
  async function previewImageWidth(page: Page) {
    const img = page.frameLocator("iframe.html-preview").locator("#rel-img");
    await expect(img).toBeAttached();
    await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(0);
  }

  test("'Open original' runs the page full-tab for the owner", async ({ page }) => {
    await login(page, "/site/index.html");
    await page.goto("/site/index.html");
    const link = page.locator("#file-header-bar a.open-original-btn");
    await expect(link).toHaveAttribute("href", /^\/_preview\/[^/]+\/site\/index\.html$/);
    const [tab] = await Promise.all([page.context().waitForEvent("page"), link.click()]);
    await expect(tab.locator("h1")).toHaveAttribute("data-js", "ran");
    await expect.poll(() => tab.locator("#rel-img").evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(0);
    await tab.close();
  });

  test("load for the owner", async ({ page }) => {
    await login(page, "/site/index.html");
    await page.goto("/site/index.html");
    await previewImageWidth(page);
  });

  test("load in a password-protected folder share", async ({ page, browser }) => {
    await login(page, "/site/");
    const url = await createShare(page, { password: "preview-pw" });
    const { ctx, page: visitor } = await freshPage(browser);
    await visitor.goto(url + "/");
    await submitPassword(visitor, "preview-pw");
    await visitor.goto(url + "/index.html");
    await previewImageWidth(visitor);
    await ctx.close();
  });
});
