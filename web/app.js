(() => {
  'use strict';
  const $ = id => document.getElementById(id);
  const number = value => Number.isFinite(value) ? new Intl.NumberFormat().format(value) : '—';
  const bytes = (value, rate = false) => {
    if (!Number.isFinite(value)) return '—';
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++; }
    return `${value.toLocaleString(undefined, { maximumFractionDigits: unit ? 1 : 0 })} ${units[unit]}${rate ? '/s' : ''}`;
  };
  const duration = value => {
    if (!Number.isFinite(value)) return '—';
    const days = Math.floor(value / 86400), hours = Math.floor(value / 3600) % 24, mins = Math.floor(value / 60) % 60;
    return days ? `${days}d ${hours}h` : hours ? `${hours}h ${mins}m` : `${mins}m ${Math.floor(value) % 60}s`;
  };
  let busy = false, saving = false, initialized = false, timer, lastStatus = null;
  const form = $('routing-form');
  const configInputs = form.querySelectorAll('input, textarea');
  async function request(path, options = {}) {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 10000);
    try {
      const response = await fetch(path, { ...options, signal: controller.signal, cache: 'no-store' });
      let data;
      try { data = await response.json(); } catch (_) { throw new Error('The panel returned an unreadable response.'); }
      if (!response.ok) throw new Error(data.error || `Request failed (${response.status}).`);
      return data;
    } finally { clearTimeout(timeout); }
  }
  function updateMode() {
    const all = form.elements.mode.value === 'all';
    $('domains').disabled = all;
    $('domains-help').textContent = all ? 'Saved rules are kept for when you switch back to selected domains.' : 'One domain per line, without wildcards. Each rule includes the domain and all its subdomains.';
    const count = $('domains').value.split(/\r?\n/).map(s => s.trim()).filter(Boolean).length;
    $('rule-count').textContent = `${count} ${count === 1 ? 'rule' : 'rules'}`;
  }
  function applyConfig(config) {
    $('public-ip').value = config.public_ipv4 || '';
    $('upstream-dns').value = config.upstream_dns || '';
    form.elements.mode.value = config.mode === 'all' ? 'all' : 'selected';
    $('domains').value = (config.domains || []).join('\n');
    $('route-address').textContent = config.public_ipv4 || 'Configure an address';
    $('device-address').textContent = config.public_ipv4 || 'the reachable IPv4 address above';
    $('dns-upstream').textContent = config.upstream_dns || 'Not configured';
    updateMode();
  }
  function renderChart(history) {
    const now = Date.now();
    const valid = history.filter(point => Number.isFinite(Date.parse(point.at)) && now - Date.parse(point.at) <= 300000);
    $('chart-empty').hidden = valid.filter(point => Number.isFinite(point.rx_bytes_per_second) || Number.isFinite(point.tx_bytes_per_second)).length > 1;
    const maximum = Math.max(1, ...valid.flatMap(p => [p.rx_bytes_per_second || 0, p.tx_bytes_per_second || 0]));
    $('chart-scale').textContent = bytes(maximum, true);
    const x = p => Math.min(800, Math.max(0, (Date.parse(p.at) - now + 300000) / 300000 * 800));
    const y = v => 140 - Math.max(0, v) / maximum * 115;
    function line(key) {
      let d = '', connected = false;
      for (const point of valid) {
        if (!Number.isFinite(point[key])) { connected = false; continue; }
        d += `${connected ? 'L' : 'M'}${x(point).toFixed(1)},${y(point[key]).toFixed(1)} `;
        connected = true;
      }
      return d;
    }
    const received = line('rx_bytes_per_second');
    $('rx-line').setAttribute('d', received);
    $('tx-line').setAttribute('d', line('tx_bytes_per_second'));
    // Only fill uninterrupted samples; gaps in monitoring must remain visible.
    $('rx-area').setAttribute('d', valid.length > 1 && valid.every(p => Number.isFinite(p.rx_bytes_per_second)) ? `${received}L${x(valid[valid.length - 1]).toFixed(1)},150 L${x(valid[0]).toFixed(1)},150 Z` : '');
  }
  function renderStatus(status) {
    lastStatus = status;
    const states = { setup_required: 'Setup required', starting: 'Relay starting', running: 'Relay running', error: 'Relay unavailable', stopped: 'Relay stopped' };
    const badge = $('engine-badge');
    badge.className = `status-badge ${status.state === 'running' ? 'running' : status.state === 'error' ? 'error' : ''}`;
    badge.querySelector('span').textContent = states[status.state] || 'Status unavailable';
    const notice = $('engine-notice');
    notice.hidden = status.state === 'running';
    notice.classList.toggle('error', status.state === 'error');
    notice.replaceChildren();
    if (status.state === 'setup_required') {
      notice.append('Set the reachable IPv4 address and save your routing settings to start the relay. ');
      const link = document.createElement('a'); link.href = '#routing'; link.textContent = 'Configure routing'; notice.append(link);
    } else if (status.state !== 'running') notice.textContent = status.error || (status.state === 'starting' ? 'The relay is starting. Connection statistics will appear shortly.' : 'The relay is stopped. Check the configuration and container logs.');
    $('active-connections').textContent = number(status.active_connections);
    $('proxied-connections').textContent = number(status.proxied_connections);
    $('domain-count').textContent = status.mode === 'all' ? 'All' : number(status.domain_count);
    $('domain-caption').textContent = status.mode === 'all' ? `${number(status.domain_count)} saved rules` : 'Configured routing rules';
    $('network-transfer').textContent = Number.isFinite(status.rx_bytes) && Number.isFinite(status.tx_bytes) ? bytes(status.rx_bytes + status.tx_bytes) : '—';
    $('rx-rate').textContent = bytes(status.rx_bytes_per_second, true);
    $('tx-rate').textContent = bytes(status.tx_bytes_per_second, true);
    $('uptime').textContent = duration(status.uptime_seconds);
    $('dns-requests').textContent = number(status.dns_requests);
    $('dns-proxied').textContent = number(status.dns_proxied);
    $('http-requests').textContent = number(status.http_proxied);
    $('https-connections').textContent = number(status.https_proxied);
    $('route-mode').textContent = status.mode === 'all' ? 'All domains' : 'Selected domains';
    $('bandwidth-scope').textContent = `${status.bandwidth_scope || 'HTTP bodies and HTTPS tunnel bytes forwarded by HopDNS'}. Sampled every ${status.sample_interval_seconds || 5} seconds.`;
    renderChart(status.history || []);
    renderSampleAge();
  }
  function renderSampleAge() {
    const time = Date.parse(lastStatus?.sampled_at);
    if (!Number.isFinite(time)) { $('sample-time').textContent = 'Waiting for first sample'; return; }
    const age = Math.max(0, Math.floor((Date.now() - time) / 1000));
    $('sample-time').textContent = age > 15 ? `Sample is ${age}s old` : `Updated ${age}s ago`;
  }
  function schedule() {
    clearTimeout(timer);
    if (!document.hidden) timer = setTimeout(poll, 5000);
  }
  async function poll() {
    if (busy || saving || document.hidden) return;
    busy = true;
    $('refresh').disabled = true;
    try {
      if (!initialized) {
        const config = await request('/api/config');
        applyConfig(config.config || config);
        initialized = true;
        $('save').disabled = false;
      }
      renderStatus(await request('/api/status'));
      $('connection-error').hidden = true;
    } catch (error) {
      const notice = $('connection-error');
      notice.textContent = `Cannot refresh the panel. ${error.name === 'AbortError' ? 'The request timed out.' : error.message} Displayed values may be stale.`;
      notice.hidden = false;
      $('engine-badge').className = 'status-badge error';
      $('engine-badge').querySelector('span').textContent = 'Panel disconnected';
      renderSampleAge();
    } finally {
      busy = false;
      $('refresh').disabled = false;
      schedule();
    }
  }
  form.addEventListener('input', () => {
    updateMode();
    $('save-result').textContent = '';
  });
  form.addEventListener('submit', async event => {
    event.preventDefault();
    if (!initialized || saving) return;
    saving = true;
    clearTimeout(timer);
    $('save').disabled = true;
    $('save').textContent = 'Saving…';
    configInputs.forEach(input => { input.disabled = true; });
    const result = $('save-result');
    result.classList.remove('error');
    result.textContent = '';
    try {
      const response = await request('/api/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json', 'X-HopDNS-Request': '1' },
        body: JSON.stringify({ public_ipv4: $('public-ip').value.trim(), upstream_dns: $('upstream-dns').value.trim(), mode: form.elements.mode.value, domains: $('domains').value.split(/\r?\n/).map(value => value.trim()).filter(Boolean) })
      });
      if (response.config) applyConfig(response.config);
      result.textContent = response.restarted ? 'Routing saved. Relay restarting.' : 'Routing is already up to date.';
    } catch (error) {
      result.classList.add('error');
      result.textContent = error.name === 'AbortError' ? 'Saving timed out. Refresh to check whether settings were applied.' : error.message;
    } finally {
      configInputs.forEach(input => { input.disabled = false; });
      updateMode();
      $('save').disabled = false;
      $('save').textContent = 'Save routing';
      saving = false;
      poll();
    }
  });
  $('refresh').addEventListener('click', () => { clearTimeout(timer); poll(); });
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) clearTimeout(timer); else poll();
  });
  poll();
})();
