const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { chromium } = require('playwright');

const root = path.resolve(__dirname, '..');
const routes = {
  '/': 'home.html',
  '/aws-config': 'aws-config.html',
  '/tunnels': 'tunnels.html',
  '/shell-launch': 'shell-launch.html',
  '/remote-shell-launch': 'remote-shell-launch.html',
  '/aliases': 'aliases.html',
  '/shell': 'shell.html',
  '/remote-shell': 'remote-shell.html',
};
const actions = ['/aws-config', '/tunnels', '/shell-launch', '/remote-shell-launch', '/aliases'];

test('menu navigation is accessible on desktop and mobile', async (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'chekov-menu-'));
  let server;
  let browser;
  try {
    execFileSync('go', ['test', './cmd', '-run', '^TestExportMenuBrowserFixtures$', '-count=1'], {
      cwd: root,
      env: { ...process.env, CHEKOV_MENU_FIXTURE_DIR: dir },
      stdio: 'pipe',
    });

    server = http.createServer((req, res) => {
      const pathname = new URL(req.url, 'http://localhost').pathname;
      if (routes[pathname]) {
        res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
        res.end(fs.readFileSync(path.join(dir, routes[pathname])));
      } else if (pathname === '/static/styles.css' || pathname === '/static/app.js') {
        res.writeHead(200, { 'Content-Type': pathname.endsWith('.css') ? 'text/css' : 'text/javascript' });
        res.end(fs.readFileSync(path.join(root, pathname)));
      } else if (pathname === '/homepage/version') {
        res.writeHead(200, { 'Content-Type': 'text/plain' });
        res.end('1');
      } else {
        res.writeHead(404);
        res.end();
      }
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const base = `http://127.0.0.1:${server.address().port}`;
    browser = await chromium.launch({
      headless: true,
      ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}),
    });

    await t.test('links navigate and identify the current page', async () => {
      const page = await browser.newPage();
      await page.goto(base);
      const menu = page.getByRole('navigation', { name: 'Main actions' });
      assert.equal(await menu.getByRole('link').count(), 5);
      assert.equal(await menu.locator('[aria-current]').count(), 0);

      for (const action of actions) {
        await menu.locator(`a[href="${action}"]`).click();
        assert.equal(new URL(page.url()).pathname, action);
        assert.equal(await menu.locator('[aria-current="page"]').count(), 1);
        assert.equal(await menu.locator('[aria-current="page"]').getAttribute('href'), action);
      }
      await page.close();
    });

    await t.test('keyboard focus is visible and Enter navigates', async () => {
      const page = await browser.newPage();
      await page.goto(base);
      const firstLink = page.locator('.main-action').first();
      for (let i = 0; i < 10 && !(await firstLink.evaluate((el) => el === document.activeElement)); i++) {
        await page.keyboard.press('Tab');
      }
      assert.equal(await firstLink.evaluate((el) => el === document.activeElement), true);
      assert.equal(await firstLink.evaluate((el) => el.matches(':focus-visible')), true);
      const outline = await firstLink.evaluate((el) => getComputedStyle(el).outlineWidth);
      assert.ok(parseFloat(outline) >= 3, `focus outline was ${outline}`);
      await page.keyboard.press('Enter');
      assert.equal(new URL(page.url()).pathname, actions[0]);
      await page.close();
    });

    await t.test('mobile menu fits in two rows with visible text', async () => {
      const page = await browser.newPage({ viewport: { width: 320, height: 800 }, isMobile: true });
      await page.goto(base);
      assert.equal(await page.evaluate(() => window.innerWidth), 320);
      const menu = page.locator('.main-action-menu');
      const geometry = await menu.evaluate((el) => ({
        width: el.getBoundingClientRect().width,
        height: el.getBoundingClientRect().height,
        scrollWidth: el.scrollWidth,
        clientWidth: el.clientWidth,
        rows: new Set([...el.querySelectorAll('a')].map((link) => Math.round(link.getBoundingClientRect().top))).size,
      }));
      assert.equal(geometry.rows, 2);
      assert.ok(geometry.height < 270, `menu height was ${geometry.height}px`);
      assert.ok(geometry.scrollWidth <= geometry.clientWidth, 'menu overflows horizontally');
      for (const action of actions) {
        assert.equal(await menu.locator(`a[href="${action}"] .main-action-title`).isVisible(), true);
        assert.equal(await menu.locator(`a[href="${action}"] .main-action-description`).isVisible(), true);
      }
      await page.close();
    });

    await t.test('terminal pages have no menu', async () => {
      const page = await browser.newPage();
      for (const route of ['/shell', '/remote-shell']) {
        await page.goto(base + route, { waitUntil: 'domcontentloaded' });
        assert.equal(await page.locator('.main-action-menu').count(), 0);
        assert.equal(await page.locator('#terminal').count(), 1);
      }
      await page.close();
    });
  } finally {
    if (browser) await browser.close();
    if (server) await new Promise((resolve) => server.close(resolve));
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
