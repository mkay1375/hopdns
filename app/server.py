"""Bounded, sampled monitoring and configuration for a separate sniproxy process."""
from __future__ import annotations

import copy
import ipaddress
import json
import math
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import threading
import tempfile
import time
from collections import deque
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import URLError
from urllib.parse import urlsplit
from urllib.request import ProxyHandler, build_opener

ROOT = Path(__file__).resolve().parent.parent
UPSTREAM_COMMIT = '1429d6dbe7bed4767597470223f93a407b0d5dab'
DEFAULT_CONFIG = {'public_ipv4': '', 'upstream_dns': 'udp://1.1.1.1:53',
                  'mode': 'selected', 'domains': ['xbox.com', 'xboxlive.com', 'playstation.net']}
MAX_BODY = 65536
INTERVAL = 5
OPENER = build_opener(ProxyHandler({}))


def timestamp():
    return datetime.now(timezone.utc).isoformat()


def validate_config(value, allow_empty_ip=False):
    if not isinstance(value, dict) or set(value) != set(DEFAULT_CONFIG):
        raise ValueError('Provide public_ipv4, upstream_dns, mode, and domains only.')
    result = copy.deepcopy(value)
    ip = result['public_ipv4']
    if not (allow_empty_ip and ip == ''):
        try:
            parsed = ipaddress.IPv4Address(ip)
            if parsed.is_unspecified or parsed.is_multicast or str(parsed) == '255.255.255.255':
                raise ValueError()
        except (ValueError, TypeError, ipaddress.AddressValueError):
            raise ValueError('Public IPv4 must be a reachable IPv4 address.') from None
    uri = result['upstream_dns']
    if not isinstance(uri, str) or len(uri) > 256 or any(ord(c) < 33 for c in uri):
        raise ValueError('Upstream DNS must be udp://IP:port or tcp://IP:port.')
    try:
        parts = urlsplit(uri)
        ipaddress.ip_address(parts.hostname or '')
        if parts.scheme not in ('udp', 'tcp') or not parts.port or parts.username or parts.password or parts.path or parts.query or parts.fragment:
            raise ValueError()
        if parts.hostname == ip:
            raise ValueError()
    except (ValueError, TypeError):
        raise ValueError('Use an upstream DNS IP with udp://IP:port or tcp://IP:port, different from the advertised address.') from None
    if result['mode'] not in ('selected', 'all'):
        raise ValueError('Mode must be selected or all.')
    domains = result['domains']
    if not isinstance(domains, list) or len(domains) > 1000:
        raise ValueError('Provide at most 1,000 domain rules.')
    normalized = []
    for domain in domains:
        if not isinstance(domain, str):
            raise ValueError('Each domain must be a hostname.')
        domain = domain.lower().rstrip('.')
        if len(domain) > 253 or '.' not in domain or not all(re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', label) for label in domain.split('.')):
            raise ValueError('Domains must be plain hostnames without wildcards, URLs, or ports.')
        try:
            ipaddress.ip_address(domain)
        except ValueError:
            pass
        else:
            raise ValueError('Use domain names, not IP addresses.')
        if domain not in normalized:
            normalized.append(domain)
    result['domains'] = normalized
    return result


def atomic_write(path, content):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    temporary = path.with_name(path.name + '.tmp')
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        with os.fdopen(fd, 'w') as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def engine_config(config, runtime):
    # JSON is a YAML subset and avoids interpolation/injection into upstream YAML.
    return {'general': {
        'public_ipv4': config['public_ipv4'], 'public_ipv6': '',
        'preferred_version': 'ipv4only', 'upstream_dns': config['upstream_dns'],
        'bind_dns_over_udp': '0.0.0.0:53', 'bind_dns_over_tcp': '0.0.0.0:53',
        'bind_http': '0.0.0.0:80', 'bind_https': '0.0.0.0:443',
        # Pinned upstream reads "prometheus", not the documented bind_prometheus.
        'prometheus': '127.0.0.1:9090', 'bind_prometheus': '127.0.0.1:9090',
        'bind_pprof': '', 'goroutine_leak_interval': '0s',
        'allow_conn_to_local': False, 'log_level': 'warn'},
        'acl': {'domain': {'enabled': config['mode'] == 'selected', 'priority': 20,
                           'path': str(Path(runtime) / 'domains.csv'), 'refresh_interval': '24h'},
                'cidr': {'enabled': False}, 'geoip': {'enabled': False},
                'override': {'enabled': False}}}


def domain_csv(domains):
    return ''.join(f'{domain}.,fqdn\n.{domain}.,suffix\n' for domain in domains)


def parse_metrics(body):
    result = {}
    names = {'dns_requests_received': 'dns_requests', 'dns_requests_proxied': 'dns_proxied',
             'http_requests_received': 'http_requests', 'https_requests_received': 'https_connections',
             'http_requests_proxied': 'http_proxied', 'https_requests_proxied': 'https_proxied'}
    for line in body.splitlines():
        if not line or line.startswith('#'):
            continue
        fields = line.split()
        if len(fields) != 2:
            continue
        key = fields[0].split('{', 1)[0]
        for suffix, target in names.items():
            if key.endswith('_' + suffix) or key.endswith('_' + suffix + '_count'):
                try:
                    number = float(fields[1])
                    if math.isfinite(number) and number >= 0:
                        result[target] = int(number)
                except ValueError:
                    pass
    if 'http_proxied' in result and 'https_proxied' in result:
        result['proxied_connections'] = result.pop('http_proxied') + result.pop('https_proxied')
    return result


def network_bytes(path='/proc/net/dev'):
    try:
        for line in Path(path).read_text().splitlines():
            if ':' not in line:
                continue
            name, data = line.split(':', 1)
            if name.strip() == 'eth0':
                values = data.split()
                return int(values[0]), int(values[8])
    except (OSError, ValueError, IndexError):
        pass
    return None, None


def active_connections(paths=('/proc/net/tcp', '/proc/net/tcp6')):
    count, available = 0, False
    for path in paths:
        try:
            with Path(path).open() as stream:
                next(stream, None)
                available = True
                for index, line in enumerate(stream):
                    # Stop rather than allocate or scan without a bound on busy hosts.
                    if index >= 100000:
                        return None
                    fields = line.split()
                    if fields[3] == '01' and int(fields[1].rsplit(':', 1)[1], 16) in (80, 443):
                        count += 1
        except (OSError, ValueError, IndexError):
            continue
    return count if available else None


class Controller:
    def __init__(self, data=None, runtime=None, binary=None):
        self.data = Path(data or os.environ.get('HOPDNS_DATA', ROOT / 'data'))
        self.runtime = Path(runtime or os.environ.get('HOPDNS_RUNTIME', '/tmp/hopdns'))
        self.binary = binary or os.environ.get('HOPDNS_ENGINE', '/usr/local/bin/sniproxy')
        self.lock = threading.RLock()
        self.stop_event = threading.Event()
        self.process = None
        self.engine_temp = None
        self.started_at = None
        self.last_start = 0
        self.generation = 0
        self.state = 'setup_required'
        self.error = None
        self.history = deque(maxlen=120)
        self.last_network = None
        self.sample = {}
        self.config = copy.deepcopy(DEFAULT_CONFIG)
        if (self.data / 'config.json').exists():
            self.config = validate_config(json.loads((self.data / 'config.json').read_text()), allow_empty_ip=True)
        elif os.environ.get('HOPDNS_PUBLIC_IPV4'):
            self.config['public_ipv4'] = os.environ['HOPDNS_PUBLIC_IPV4']
            self.config = validate_config(self.config)

    def _stop_engine(self):
        self.generation += 1
        process, self.process = self.process, None
        if process and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=2)
        if self.engine_temp:
            self.engine_temp.cleanup()
            self.engine_temp = None
        self.started_at = None
        self.sample = {}
        self.history.clear()
        self.last_network = None

    def _start_engine(self):
        self.last_start = time.monotonic()
        self.error = None
        if not self.config['public_ipv4']:
            self.state = 'setup_required'
            return
        try:
            atomic_write(self.runtime / 'domains.csv', domain_csv(self.config['domains']))
            atomic_write(self.runtime / 'engine.json', json.dumps(engine_config(self.config, self.runtime)))
            # Ignore unvalidated upstream overrides; all configuration comes from us.
            env = {k: v for k, v in os.environ.items() if not k.startswith('SNIPROXY_')}
            env['NO_COLOR'] = 'true'
            if self.engine_temp:
                self.engine_temp.cleanup()
            self.engine_temp = tempfile.TemporaryDirectory(prefix='engine-', dir=self.runtime)
            env['TMPDIR'] = self.engine_temp.name
            self.process = subprocess.Popen([self.binary, '--config', str(self.runtime / 'engine.json')],
                                            stdin=subprocess.DEVNULL, env=env)
            self.started_at = time.monotonic()
            self.state = 'starting'
        except OSError:
            if self.engine_temp:
                self.engine_temp.cleanup()
                self.engine_temp = None
            self.state = 'error'
            self.error = 'Proxy engine could not start. Check the installed binary, runtime permissions, and container logs.'

    def apply(self, value):
        config = validate_config(value)
        with self.lock:
            if config == self.config:
                return False
            atomic_write(self.data / 'config.json', json.dumps(config, indent=2) + '\n')
            self._stop_engine()
            self.config = config
            self._start_engine()
            return True

    def get_config(self):
        with self.lock:
            return copy.deepcopy(self.config)

    def status(self):
        with self.lock:
            result = {key: None for key in ('active_connections', 'dns_requests', 'dns_proxied',
                      'http_requests', 'https_connections', 'proxied_connections', 'rx_bytes',
                      'tx_bytes', 'rx_bytes_per_second', 'tx_bytes_per_second')}
            result.update(self.sample)
            result.update(state=self.state, engine_running=self.process is not None and self.process.poll() is None,
                          error=self.error, uptime_seconds=int(time.monotonic() - self.started_at) if self.started_at else 0,
                          domain_count=len(self.config['domains']), mode=self.config['mode'],
                          history=list(self.history), sample_interval_seconds=INTERVAL,
                          bandwidth_scope='Container network (includes DNS and panel traffic)',
                          upstream_commit=UPSTREAM_COMMIT)
            return result

    def sample_once(self):
        # Bounded scrape outside the controller lock, so HTTP API reads stay fast.
        with self.lock:
            generation = self.generation
        try:
            with OPENER.open('http://127.0.0.1:9090/metrics', timeout=1) as response:
                body = response.read(262145)
                if len(body) > 262144:
                    raise ValueError('Metrics response too large')
                metrics = parse_metrics(body.decode('utf-8'))
        except (OSError, URLError, ValueError):
            metrics = {}
        rx, tx = network_bytes()
        now = time.monotonic()
        sample = dict(metrics, sampled_at=timestamp(), active_connections=active_connections(),
                      rx_bytes=rx, tx_bytes=tx, rx_bytes_per_second=None, tx_bytes_per_second=None)
        with self.lock:
            if generation != self.generation:
                return
            if self.last_network and rx is not None and tx is not None:
                previous_time, previous_rx, previous_tx = self.last_network
                elapsed = now - previous_time
                if elapsed > 0:
                    sample['rx_bytes_per_second'] = max(0, rx - previous_rx) / elapsed
                    sample['tx_bytes_per_second'] = max(0, tx - previous_tx) / elapsed
            self.last_network = (now, rx, tx) if rx is not None and tx is not None else None
            if self.process and self.process.poll() is None:
                if metrics:
                    self.state = 'running'
                    self.error = None
                elif self.started_at and now - self.started_at > 15:
                    self.state = 'error'
                    self.error = 'Proxy metrics are unavailable. Check container logs and listener ports.'
            self.sample = sample
            self.history.append({key: sample[key] for key in ('sampled_at', 'rx_bytes_per_second', 'tx_bytes_per_second', 'active_connections')})
            self.history[-1]['at'] = self.history[-1].pop('sampled_at')

    def run(self):
        with self.lock:
            self._start_engine()
        while not self.stop_event.is_set():
            with self.lock:
                if self.process and self.process.poll() is not None:
                    self.process = None
                    self.started_at = None
                    self.state = 'error'
                    self.error = 'Proxy engine exited. Retrying in 15 seconds; check container logs.'
                if self.config['public_ipv4'] and self.process is None and time.monotonic() - self.last_start >= 15:
                    self._start_engine()
            self.sample_once()
            self.stop_event.wait(INTERVAL)
        with self.lock:
            self._stop_engine()
            self.state = 'stopped'

    def stop(self):
        self.stop_event.set()


class Server(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True

    def __init__(self, address, controller):
        super().__init__(address, Handler)
        self.controller = controller
        self.allowed_hosts = {'localhost', '127.0.0.1', '[::1]'}
        self.allowed_hosts.update(x.strip().lower() for x in os.environ.get('HOPDNS_ALLOWED_HOSTS', '').split(',') if x.strip())
        self.slots = threading.BoundedSemaphore(32)

    def process_request(self, request, client_address):
        if not self.slots.acquire(blocking=False):
            request.close()
            return
        try:
            super().process_request(request, client_address)
        except Exception:
            self.slots.release()
            raise

    def process_request_thread(self, request, client_address):
        try:
            super().process_request_thread(request, client_address)
        finally:
            self.slots.release()


class Handler(BaseHTTPRequestHandler):
    server_version = 'HopDNS'

    def setup(self):
        super().setup()
        self.connection.settimeout(5)

    def log_message(self, *args):
        pass

    def send(self, status, value, content_type='application/json'):
        payload = json.dumps(value).encode() if content_type == 'application/json' else value
        self.send_response(status)
        self.send_header('Content-Type', content_type)
        self.send_header('Content-Length', str(len(payload)))
        self.send_header('Cache-Control', 'no-store')
        self.send_header('X-Content-Type-Options', 'nosniff')
        self.send_header('X-Frame-Options', 'DENY')
        self.send_header('Referrer-Policy', 'no-referrer')
        self.send_header('Content-Security-Policy', "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
        self.end_headers()
        self.wfile.write(payload)

    def trusted(self, mutation=False):
        host = self.headers.get('Host', '')
        try:
            parsed = urlsplit('http://' + host)
            name = '[' + parsed.hostname + ']' if ':' in (parsed.hostname or '') else parsed.hostname
            valid = name in self.server.allowed_hosts and (parsed.port is not None or ':' not in host)
            if not valid or parsed.path or parsed.query or parsed.fragment or parsed.username or parsed.password:
                raise ValueError()
            origin = self.headers.get('Origin')
            if origin and origin != 'http://' + host and origin != 'https://' + host:
                raise ValueError()
            if self.headers.get('Sec-Fetch-Site') in ('cross-site',):
                raise ValueError()
            if mutation and self.headers.get('X-HopDNS-Request') != '1':
                raise ValueError()
        except (ValueError, TypeError):
            self.send(403, {'error': 'Open the panel through an allowed host and use same-origin requests.'})
            return False
        return True

    def do_GET(self):
        if not self.trusted():
            return
        if self.path == '/api/config':
            self.send(200, self.server.controller.get_config())
        elif self.path == '/api/status':
            self.send(200, self.server.controller.status())
        elif self.path == '/healthz':
            status = self.server.controller.status()
            self.send(200 if status['state'] in ('setup_required', 'running') else 503, {'state': status['state']})
        else:
            files = {'/': ('index.html', 'text/html; charset=utf-8'), '/index.html': ('index.html', 'text/html; charset=utf-8'),
                     '/app.js': ('app.js', 'text/javascript; charset=utf-8'), '/theme.js': ('theme.js', 'text/javascript; charset=utf-8'),
                     '/style.css': ('style.css', 'text/css; charset=utf-8'), '/favicon.svg': ('favicon.svg', 'image/svg+xml')}
            if self.path not in files:
                self.send(404, {'error': 'Not found.'})
                return
            filename, content_type = files[self.path]
            try:
                self.send(200, (ROOT / 'web' / filename).read_bytes(), content_type)
            except FileNotFoundError:
                self.send(404, {'error': 'Not found.'})

    def do_PUT(self):
        if not self.trusted(mutation=True):
            return
        if self.path != '/api/config':
            self.send(404, {'error': 'Not found.'})
            return
        try:
            length = int(self.headers.get('Content-Length', '-1'))
            if length < 0 or length > MAX_BODY or self.headers.get('Transfer-Encoding'):
                self.send(413, {'error': 'Request must have a Content-Length of at most 65,536 bytes.'})
                return
            if self.headers.get('Content-Type', '').split(';')[0] != 'application/json':
                self.send(415, {'error': 'Use application/json.'})
                return
            value = json.loads(self.rfile.read(length))
            restarted = self.server.controller.apply(value)
            self.send(200, {'config': self.server.controller.get_config(), 'restarted': restarted})
        except (ValueError, UnicodeError):
            self.send(400, {'error': 'Invalid settings. Check the IPv4 address, DNS URI, mode, and domain names.'})
        except OSError:
            self.send(500, {'error': 'Settings could not be saved. Check the data volume permissions.'})


def main():
    controller = Controller()
    server = Server((os.environ.get('HOPDNS_BIND', '0.0.0.0'), int(os.environ.get('HOPDNS_PORT', '8787'))), controller)
    worker = threading.Thread(target=controller.run, daemon=True)
    worker.start()
    def stop(signum, frame):
        controller.stop()
        threading.Thread(target=server.shutdown, daemon=True).start()
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    try:
        server.serve_forever(poll_interval=0.25)
    finally:
        controller.stop()
        worker.join(timeout=10)
        server.server_close()


if __name__ == '__main__':
    main()
