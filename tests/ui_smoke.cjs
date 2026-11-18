/* UI fixtures only: these values are synthetic, not proxy performance evidence. */
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || '/app/node_modules/playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const base = process.env.PANEL_URL || 'http://127.0.0.1:8787';
(async () => {
  const browser = await chromium.launch({ channel: 'chromium', headless: true, args: ['--no-sandbox'] });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1250 } });
    let config = { public_ipv4: '192.0.2.40', upstream_dns: 'udp://1.1.1.1:53', mode: 'selected', domains: ['example.com', 'playstation.net'] };
    let failStatus = false, submitted;
    await page.route('**/api/config', async route => {
      if (route.request().method() === 'PUT') {
        assert.equal(route.request().headers()['x-hopdns-request'], '1');
        submitted = route.request().postDataJSON();
        config = submitted;
        return route.fulfill({ json: { config, restarted: true } });
      }
      await route.fulfill({ json: config });
    });
    await page.route('**/api/status', route => {
      if (failStatus) return route.fulfill({ status: 503, json: { error: 'Fixture unavailable' } });
      return route.fulfill({ json: {
        state: 'running', engine_running: true, error: null, uptime_seconds: 5020,
        active_connections: 7, proxied_connections: 213, domain_count: config.domains.length,
        dns_requests: 621, dns_proxied: 420, http_requests: 12, https_connections: 207,
        http_proxied: 9, https_proxied: 204,
        rx_bytes: 501234567, tx_bytes: 12543210, rx_bytes_per_second: 252400, tx_bytes_per_second: 34000,
        mode: config.mode, sampled_at: new Date().toISOString(), sample_interval_seconds: 5,
        bandwidth_scope: 'HTTP bodies and HTTPS tunnel bytes forwarded by HopDNS',
        history: Array.from({length: 60}, (_, i) => ({at: new Date(Date.now() - (59-i)*5000).toISOString(),rx_bytes_per_second: 80000+Math.sin(i/3)*40000+i*1200,tx_bytes_per_second: 16000+Math.cos(i/4)*7000}))
      }});
    });
    await page.goto(base);
    await page.locator('#engine-badge').getByText('Relay running').waitFor();
    assert.equal(await page.locator('#active-connections').textContent(), '7');
    assert.equal(await page.locator('#domain-count').textContent(), '2');
    assert.equal(await page.locator('#http-requests').textContent(), '9');
    assert.equal(await page.locator('#https-connections').textContent(), '204');
    assert.equal(await page.locator('#network-transfer').textContent(), '490 MiB');
    assert.equal(await page.locator('#domains').inputValue(), 'example.com\nplaystation.net');
    await page.locator('#theme').selectOption('dark');
    assert.equal(await page.locator('html').getAttribute('data-theme'), 'dark');
    await page.reload();
    await page.locator('#engine-badge').getByText('Relay running').waitFor();
    assert.equal(await page.locator('#theme').inputValue(), 'dark');
    await page.locator('#theme').selectOption('system');
    await page.emulateMedia({colorScheme:'light'});
    await page.waitForFunction(() => document.documentElement.dataset.theme === 'light');
    await page.emulateMedia({colorScheme:'dark'});
    await page.waitForFunction(() => document.documentElement.dataset.theme === 'dark');
    await page.locator('input[name=mode][value=all]').check();
    assert.equal(await page.locator('#domains').isDisabled(), true);
    await page.locator('#save').click();
    await page.getByText('Routing saved. Relay restarting.').waitFor();
    assert.equal(submitted.mode, 'all');
    assert.deepEqual(submitted.domains, ['example.com', 'playstation.net']);
    await page.locator('#domain-count').getByText('All', {exact: true}).waitFor();
    await page.locator('input[name=mode][value=selected]').check();
    assert.equal(await page.locator('#domains').isDisabled(), false);
    await page.locator('#domains').fill('example.com\nplaystation.net');
    if (process.env.ARTIFACTS) {
      fs.mkdirSync(process.env.ARTIFACTS, { recursive: true });
      await page.screenshot({path: `${process.env.ARTIFACTS}/fixture-desktop-dark.png`, fullPage: true});
      await page.locator('#theme').selectOption('light');
      await page.screenshot({path: `${process.env.ARTIFACTS}/fixture-desktop-light.png`, fullPage: true});
    }
    await page.setViewportSize({width:390,height:844});
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'Mobile layout overflows');
    if (process.env.ARTIFACTS) await page.screenshot({path: `${process.env.ARTIFACTS}/fixture-mobile.png`, fullPage: true});
    failStatus = true;
    await page.locator('#refresh').click();
    await page.locator('#connection-error').getByText(/Fixture unavailable/).waitFor();
    assert.equal(await page.locator('#active-connections').textContent(), '7', 'Last sample should remain visible');
    await page.locator('#engine-badge').getByText('Panel disconnected').waitFor();
    console.log('UI smoke passed: themes, polling, routing form, stale state, responsive layout.');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exit(1); });
