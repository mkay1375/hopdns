"""Run a single HopDNS executable against local DNS, HTTP and TLS fixtures.

Python is a test dependency only. No Docker or public service is used. The
fixture-only engine configuration permits private origins; the normal parent
configuration must retain private-origin refusal.
"""
import argparse
import concurrent.futures
import contextlib
import http.client
import http.server
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import socketserver
import ssl
import struct
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
PAYLOAD = b'hopdns-native-offline-origin\n' * 4096
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def local_ip():
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        # UDP connect chooses a source address without transmitting a packet.
        sock.connect(('8.8.8.8', 53))
        address = sock.getsockname()[0]
    assert not address.startswith('127.'), 'A non-loopback interface is required for the DNS fixture'
    return address


def available_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def receive(sock, length):
    data = b''
    while len(data) < length:
        chunk = sock.recv(length - len(data))
        if not chunk:
            raise AssertionError('Unexpected TCP EOF')
        data += chunk
    return data


def dns_question(name, kind=1):
    return struct.pack('!HHHHHH', 1234, 256, 1, 0, 0, 0) + b''.join(bytes([len(x)]) + x.encode() for x in name.split('.')) + b'\0' + struct.pack('!HH', kind, 1)


def dns_query(port, name, tcp=False, kind=1):
    query = dns_question(name, kind)
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM if tcp else socket.SOCK_DGRAM) as sock:
        sock.settimeout(4)
        sock.connect(('127.0.0.1', port))
        if tcp:
            sock.sendall(struct.pack('!H', len(query)) + query)
            response = receive(sock, struct.unpack('!H', receive(sock, 2))[0])
        else:
            sock.send(query)
            response = sock.recv(4096)
    count = struct.unpack('!H', response[6:8])[0]
    if kind == 28:
        assert count == 0, response.hex()
        return None
    assert count == 1, response.hex()
    return socket.inet_ntoa(response[-4:])


class DNSUDP(socketserver.BaseRequestHandler):
    def handle(self):
        query, sock = self.request
        sock.sendto(dns_answer(query, self.server.origin_ip), self.client_address)


class DNSTCP(socketserver.StreamRequestHandler):
    def handle(self):
        while prefix := self.rfile.read(2):
            if len(prefix) != 2:
                return
            length = struct.unpack('!H', prefix)[0]
            response = dns_answer(self.rfile.read(length), self.server.origin_ip)
            self.wfile.write(struct.pack('!H', len(response)) + response)


def dns_answer(query, origin_ip):
    end = 12
    while query[end]:
        end += query[end] + 1
    end += 1
    kind = struct.unpack('!H', query[end:end + 2])[0]
    record = b''
    if kind == 1:
        record = b'\xc0\x0c' + struct.pack('!HHIH', 1, 1, 1, 4) + socket.inet_aton(origin_ip)
    return query[:2] + struct.pack('!HHHHH', 0x8180, 1, bool(record), 0, 0) + query[12:end + 4] + record


class Origin(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-Length', str(len(PAYLOAD)))
        self.end_headers()
        self.wfile.write(PAYLOAD)

    def log_message(self, *args):
        pass


@contextlib.contextmanager
def serving(server):
    server.daemon_threads = True
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=3)


def stop_process(process):
    if process.poll() is not None:
        return
    try:
        process.send_signal(signal.CTRL_BREAK_EVENT if os.name == 'nt' else signal.SIGTERM)
        process.wait(timeout=12)
    except (OSError, subprocess.TimeoutExpired):
        # Clean up this test's owned process tree, but keep the graceful-stop
        # failure visible instead of turning forceful cleanup into a passing test.
        if os.name == 'nt':
            subprocess.run(['taskkill', '/PID', str(process.pid), '/T', '/F'],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        else:
            process.kill()
        process.wait(timeout=5)
        raise


class Application:
    def __init__(self, binary, directory, ports):
        self.binary, self.directory, self.ports = binary, directory, ports
        self.log = directory / 'terminal.log'
        self.process = None
        self.url = None

    def start(self):
        self.url = None
        self.stream = self.log.open('wb', buffering=0)
        self.process = subprocess.Popen([str(self.binary), '--panel', 'localhost:0', '--no-browser', '--data-dir', str(self.directory / 'settings'), '--dns-port', str(self.ports[0]), '--http-port', str(self.ports[1]), '--https-port', str(self.ports[2])], cwd=self.directory, stdout=self.stream, stderr=self.stream, creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if os.name == 'nt' else 0)
        try:
            wait_until(lambda: self.discover_url(), 'terminal panel URL', 20)
            wait_until(lambda: self.api('/healthz'), 'panel readiness', 20)
        except Exception:
            print(self.log.read_text(errors='replace'), flush=True)
            raise

    def discover_url(self):
        text = self.log.read_text(errors='replace')
        matches = re.findall(r'Panel: (http://127\.0\.0\.1:\d+)', text)
        if matches:
            self.url = matches[-1]
        return self.url

    def api(self, path='/api/status', value=None):
        request = urllib.request.Request(self.url + path)
        if value is not None:
            request = urllib.request.Request(self.url + path, data=json.dumps(value).encode(), method='PUT', headers={'Content-Type': 'application/json', 'X-HopDNS-Request': '1', 'Origin': self.url})
        return json.load(OPENER.open(request, timeout=10))

    def stop(self):
        if self.process and self.process.poll() is None:
            stop_process(self.process)
        if getattr(self, 'stream', None):
            self.stream.close()
        self.process = None


def wait_until(predicate, description, timeout=25):
    deadline = time.monotonic() + timeout
    error = None
    while time.monotonic() < deadline:
        try:
            value = predicate()
            if value:
                return value
        except (OSError, ValueError, urllib.error.HTTPError) as exc:
            error = exc
        time.sleep(.15)
    raise AssertionError(f'Timed out waiting for {description}: {error}')


def http_request(port, host):
    conn = http.client.HTTPConnection('127.0.0.1', port, timeout=5)
    try:
        conn.request('GET', '/', headers={'Host': host})
        response = conn.getresponse()
        return response.status, response.read()
    finally:
        conn.close()


def tls_request(port, cert):
    context = ssl.create_default_context(cafile=str(cert))
    with socket.create_connection(('127.0.0.1', port), timeout=6) as raw:
        with context.wrap_socket(raw, server_hostname='selected.hopdns.test') as stream:
            stream.sendall(b'GET / HTTP/1.1\r\nHost: selected.hopdns.test\r\nConnection: close\r\n\r\n')
            response = bytearray()
            while chunk := stream.recv(65536):
                response.extend(chunk)
    header, _, body = bytes(response).partition(b'\r\n\r\n')
    assert b'200 OK' in header and body == PAYLOAD


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, type=Path)
    args = parser.parse_args()
    original = args.binary.resolve()
    ip = local_ip()
    with tempfile.TemporaryDirectory(prefix='hopdns-native-smoke-') as temp, contextlib.ExitStack() as stack:
        directory = Path(temp)
        binary = directory / ('hopdns.exe' if os.name == 'nt' else 'hopdns')
        shutil.copy2(original, binary)
        binary.chmod(0o755)
        result = subprocess.run([str(binary), '--version'], cwd=directory, check=True, capture_output=True, text=True)
        assert 'HopDNS' in result.stdout
        dns_server = socketserver.ThreadingTCPServer(('0.0.0.0', 0), DNSTCP)
        dns_port = dns_server.server_address[1]
        udp_server = socketserver.ThreadingUDPServer(('0.0.0.0', dns_port), DNSUDP)
        for server in (dns_server, udp_server):
            server.origin_ip = ip
            stack.enter_context(serving(server))
        ports = tuple(available_port() for _ in range(3))
        assert len(set(ports)) == 3
        app = Application(binary, directory, ports)
        stack.callback(app.stop)
        app.start()
        print('Parent panel started', flush=True)
        assert app.api()['state'] == 'setup_required'
        for path in ('/', '/app.js', '/theme.js', '/style.css'):
            with OPENER.open(app.url + path, timeout=3) as response:
                assert response.status == 200 and len(response.read()) > 100
        config = {'public_ipv4': '127.0.0.1', 'upstream_dns': f'tcp://{ip}:{dns_port}', 'mode': 'selected', 'domains': ['selected.hopdns.test']}
        assert app.api('/api/config', config)['restarted']
        wait_until(lambda: app.api()['state'] == 'running', 'parent engine readiness')
        assert dns_query(ports[0], 'selected.hopdns.test') == '127.0.0.1'
        assert dns_query(ports[0], 'selected.hopdns.test', tcp=True) == '127.0.0.1'
        assert dns_query(ports[0], 'selected.hopdns.test', kind=28) is None
        assert dns_query(ports[0], 'cdn.selected.hopdns.test') == '127.0.0.1'
        assert dns_query(ports[0], 'evil-selected.hopdns.test') == ip
        assert dns_query(ports[0], 'other.hopdns.test') == ip
        for headers in ({'Host': 'evil.test'}, {'Origin': 'https://evil.test'}):
            request = urllib.request.Request(app.url + '/api/config', headers=headers)
            try:
                OPENER.open(request, timeout=3)
                raise AssertionError('Untrusted panel request accepted')
            except urllib.error.HTTPError as error:
                assert error.code == 403
        try:
            OPENER.open(urllib.request.Request(app.url + '/api/config', data=b'{}', method='PUT', headers={'Content-Type': 'application/json'}), timeout=3)
            raise AssertionError('Missing mutation header accepted')
        except urllib.error.HTTPError as error:
            assert error.code == 403
        print('Selected DNS and API boundaries passed', flush=True)
        config['mode'] = 'all'
        assert app.api('/api/config', config)['restarted']
        wait_until(lambda: app.api()['state'] == 'running', 'all-domain readiness')
        assert dns_query(ports[0], 'other.hopdns.test') == '127.0.0.1'
        status, _ = http_request(ports[1], '127.0.0.1:' + str(ports[1]))
        assert status == 404, 'Production parent must refuse private destinations'
        # Ordinary sampling must not manufacture new HTTPS attempts.
        time.sleep(6)
        attempts = app.api()['https_connections']
        time.sleep(11)
        assert app.api()['https_connections'] == attempts
        app.stop()
        # Parent shutdown must release both child forwarding ports and panel.
        for port in ports:
            with socket.socket() as sock:
                sock.settimeout(1)
                assert sock.connect_ex(('127.0.0.1', port)) != 0, 'Engine child survived parent shutdown'
        app.start()
        wait_until(lambda: app.api()['state'] == 'running', 'saved configuration restart')
        assert app.api('/api/config') == config
        print('Parent lifecycle and monitoring passed', flush=True)

        # Exercise the identical embedded engine against private test origins.
        # This opt-in exists only in the generated fixture JSON, never panel API.
        tls_port, http_port, engine_dns = (available_port() for _ in range(3))
        origin_http = stack.enter_context(serving(http.server.ThreadingHTTPServer((ip, 0), Origin)))
        origin_tls = http.server.ThreadingHTTPServer((ip, tls_port), Origin)
        fixtures = ROOT / 'tests/fixtures/native'
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(fixtures / 'cert.pem', fixtures / 'key.pem')
        origin_tls.socket = context.wrap_socket(origin_tls.socket, server_side=True)
        stack.enter_context(serving(origin_tls))
        engine_config = {'general': {'public_ipv4': '127.0.0.1', 'preferred_version': 'ipv4only', 'upstream_dns': f'tcp://{ip}:{dns_port}', 'bind_dns_over_udp': f'127.0.0.1:{engine_dns}', 'bind_dns_over_tcp': f'127.0.0.1:{engine_dns}', 'bind_http': f'127.0.0.1:{http_port}', 'bind_https': f'127.0.0.1:{tls_port}', 'allow_conn_to_local': True, 'log_level': 'warn'}, 'acl': {'domain': {'enabled': False}, 'override': {'enabled': False}, 'geoip': {'enabled': False}, 'cidr': {'enabled': False}}}
        engine_path = directory / 'fixture-engine.json'
        engine_path.write_text(json.dumps(engine_config))
        with (directory / 'engine.log').open('wb') as log:
            engine = subprocess.Popen([str(binary), 'engine', '--config', str(engine_path)], cwd=directory, stdout=log, stderr=log, creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if os.name == 'nt' else 0)
            try:
                wait_until(lambda: dns_query(engine_dns, 'selected.hopdns.test') == '127.0.0.1', 'fixture engine DNS')
                target = f'{ip}:{origin_http.server_port}'
                assert http_request(http_port, target) == (200, PAYLOAD)
                tls_request(tls_port, fixtures / 'cert.pem')
                with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
                    work = [pool.submit(tls_request, tls_port, fixtures / 'cert.pem') for _ in range(6)]
                    work += [pool.submit(http_request, http_port, target) for _ in range(6)]
                    work += [pool.submit(lambda: [app.api() for _ in range(20)]) for _ in range(4)]
                    for task in work:
                        task.result()
            except Exception:
                log.flush()
                print((directory / 'engine.log').read_text(errors='replace'), flush=True)
                raise
            finally:
                stop_process(engine)
        print('PASS: standalone embedded panel, API security, selected/all UDP/TCP DNS, AAAA, suffix boundaries, private-origin refusal, stable counters, shutdown/restart, HTTP and certificate-verified TLS with EOF')


if __name__ == '__main__':
    main()
