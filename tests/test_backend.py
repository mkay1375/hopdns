import copy
import http.client
import json
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch

from app.server import (Controller, DEFAULT_CONFIG, Server, active_connections,
                        atomic_write, domain_csv, engine_config, network_bytes,
                        parse_metrics, validate_config)


def config():
    result = copy.deepcopy(DEFAULT_CONFIG)
    result['public_ipv4'] = '192.0.2.10'
    return result


class ConfigurationTests(unittest.TestCase):
    def test_domain_normalization_and_boundaries(self):
        value = config()
        value['domains'] = ['EXAMPLE.COM.', 'example.com']
        self.assertEqual(validate_config(value)['domains'], ['example.com'])
        self.assertEqual(domain_csv(['example.com']), 'example.com.,fqdn\n.example.com.,suffix\n')

    def test_rejects_invalid_input(self):
        mutations = [('public_ipv4', '0.0.0.0'), ('public_ipv4', '1.2.3.999'),
                     ('public_ipv4', ''), ('mode', 'vpn'), ('domains', ['*.example.com']),
                     ('domains', ['example.com\nreject']), ('domains', ['1.2.3.4']),
                     ('domains', ['-bad.example']), ('domains', ['localhost']),
                     ('upstream_dns', 'udp://dns.example:53'),
                     ('upstream_dns', 'udp://192.0.2.10:53'),
                     ('upstream_dns', 'udp://user@1.1.1.1:53'),
                     ('upstream_dns', 'udp://1.1.1.1:53/path'),
                     ('upstream_dns', 'udp://1.1.1.1:99999'),
                     ('upstream_dns', 'tcp://1.1.1.1'), ('domains', 'example.com')]
        for key, value in mutations:
            with self.subTest(key=key, value=value):
                candidate = config()
                candidate[key] = value
                with self.assertRaises(ValueError):
                    validate_config(candidate)
        with self.assertRaises(ValueError):
            validate_config(dict(config(), unknown=True))

    def test_engine_config_has_private_metrics_and_safety(self):
        result = engine_config(config(), '/run/hopdns')
        self.assertEqual(result['general']['prometheus'], '127.0.0.1:9090')
        self.assertEqual(result['general']['bind_dns_over_tcp'], '0.0.0.0:53')
        self.assertFalse(result['general']['allow_conn_to_local'])
        self.assertFalse(result['acl']['override']['enabled'])
        self.assertTrue(result['acl']['domain']['enabled'])
        self.assertFalse(engine_config(dict(config(), mode='all'), '/run/hopdns')['acl']['domain']['enabled'])

    def test_atomic_file_and_saved_settings(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'config.json'
            atomic_write(path, json.dumps(config()))
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertEqual(Controller(data=directory).get_config(), config())
            self.assertFalse(path.with_name('config.json.tmp').exists())

    def test_invalid_settings_never_stop_existing_process(self):
        with tempfile.TemporaryDirectory() as directory:
            controller = Controller(data=directory, runtime=directory)
            with patch.object(controller, '_stop_engine') as stop:
                with self.assertRaises(ValueError):
                    controller.apply(dict(config(), domains=['bad,url']))
                stop.assert_not_called()
            self.assertFalse((Path(directory) / 'config.json').exists())

    def test_restart_persists_and_missing_binary_reports_error(self):
        with tempfile.TemporaryDirectory() as directory:
            controller = Controller(data=directory, runtime=directory, binary='/nonexistent/hopdns-engine')
            self.assertTrue(controller.apply(config()))
            self.assertEqual(controller.status()['state'], 'error')
            self.assertEqual(Controller(data=directory).get_config(), config())
            self.assertFalse(controller.apply(config()))


class MetricsTests(unittest.TestCase):
    def test_parse_counters_and_reject_nonfinite(self):
        result = parse_metrics('''# HELP ignored
sniproxy_192_0_2_1_dns_requests_received 25
sniproxy_192_0_2_1_dns_requests_proxied_count 7
sniproxy_192_0_2_1_http_requests_proxied 3
sniproxy_192_0_2_1_https_requests_proxied 4
sniproxy_192_0_2_1_https_requests_received NaN
''')
        self.assertEqual(result, {'dns_requests': 25, 'dns_proxied': 7, 'proxied_connections': 7})

    def test_proc_only_counts_incoming_established_proxy_connections(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'tcp'
            path.write_text('header\n0: 00000000:0050 00000000:C000 01\n1: 00000000:01BB 00000000:C001 01\n2: 00000000:1F90 00000000:C002 01\n3: 00000000:0050 00000000:C003 0A\n')
            self.assertEqual(active_connections([path]), 2)
            self.assertIsNone(active_connections([path.with_name('missing')]))
            path.write_text('eth0: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0\nlo: 900 0 0 0 0 0 0 0 900 0 0 0 0 0 0 0\n')
            self.assertEqual(network_bytes(path), (100, 200))

    def test_history_bounded_and_bandwidth_reset(self):
        with tempfile.TemporaryDirectory() as directory:
            controller = Controller(data=directory)
            with patch('app.server.OPENER.open', side_effect=OSError), patch('app.server.network_bytes', return_value=(10, 20)), patch('app.server.active_connections', return_value=0):
                controller.last_network = (0, 100, 200)
                for _ in range(125):
                    controller.sample_once()
            status = controller.status()
            self.assertEqual(len(status['history']), 120)
            self.assertEqual(status['rx_bytes_per_second'], 0)
            self.assertIsNone(status['dns_requests'])
            self.assertEqual(status['state'], 'setup_required')


class HTTPTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.controller = Controller(data=self.directory.name, runtime=self.directory.name, binary='/nonexistent/hopdns')
        self.server = Server(('127.0.0.1', 0), self.controller)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.host = '127.0.0.1:' + str(self.server.server_port)

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        self.directory.cleanup()

    def request(self, method, path, body=None, headers=None):
        conn = http.client.HTTPConnection('127.0.0.1', self.server.server_port)
        conn.request(method, path, body, headers or {})
        response = conn.getresponse()
        payload = response.read()
        status = response.status
        conn.close()
        return status, json.loads(payload)

    def test_status_and_setup_health(self):
        status, result = self.request('GET', '/api/status')
        self.assertEqual(status, 200)
        self.assertFalse(result['engine_running'])
        self.assertIsNone(result['active_connections'])
        self.assertEqual(self.request('GET', '/healthz')[0], 200)

    def test_host_and_origin_defense(self):
        for headers in ({'Host': 'evil.example'}, {'Origin': 'http://evil.example'}, {'Sec-Fetch-Site': 'cross-site'}, {'Host': 'localhost@evil.example'}):
            with self.subTest(headers=headers):
                self.assertEqual(self.request('GET', '/api/config', headers=headers)[0], 403)
        self.assertEqual(self.request('PUT', '/api/config', json.dumps(config()), {'Content-Type': 'application/json'})[0], 403)

    def test_save_and_body_limits(self):
        headers = {'X-HopDNS-Request': '1', 'Content-Type': 'application/json', 'Origin': 'http://' + self.host}
        status, result = self.request('PUT', '/api/config', json.dumps(config()), headers)
        self.assertEqual(status, 200)
        self.assertTrue(result['restarted'])
        self.assertEqual(self.request('GET', '/api/config')[1], config())
        self.assertEqual(self.request('PUT', '/api/config', 'x' * 65537, headers)[0], 413)
        self.assertEqual(self.request('PUT', '/api/config', '{}', dict(headers, **{'Content-Type': 'text/plain'}))[0], 415)
        self.assertEqual(self.request('PUT', '/api/config', 'not json', headers)[0], 400)
        self.assertEqual(self.request('GET', '/../../app/server.py')[0], 404)


if __name__ == '__main__':
    unittest.main()
