"""Offline synthetic DNS + HTTP/TLS peer; never contacts external services."""
import http.server
import os
import socketserver
import ssl
import struct
import threading

IP = os.environ['LAB_IP']


def answer(query):
    # This lab only needs a single uncompressed DNS question and A/AAAA records.
    end = 12
    while query[end]:
        end += query[end] + 1
    end += 1
    kind = struct.unpack('!H', query[end:end + 2])[0]
    question = query[12:end + 4]
    record = b''
    if kind == 1:
        record = b'\xc0\x0c' + struct.pack('!HHIH', 1, 1, 60, 4) + bytes(map(int, IP.split('.')))
    return query[:2] + struct.pack('!HHHHH', 0x8180, 1, bool(record), 0, 0) + question + record


class UDP(socketserver.BaseRequestHandler):
    def handle(self):
        query, sock = self.request
        sock.sendto(answer(query), self.client_address)


class TCP(socketserver.StreamRequestHandler):
    def handle(self):
        length = struct.unpack('!H', self.rfile.read(2))[0]
        response = answer(self.rfile.read(length))
        self.wfile.write(struct.pack('!H', len(response)) + response)


class HTTP(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        payload = b'hopdns-offline-origin\n' * 4096
        self.send_response(200)
        self.send_header('Content-Length', str(len(payload)))
        self.send_header('Content-Type', 'text/plain')
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *args):
        pass


servers = [socketserver.ThreadingUDPServer(('0.0.0.0', 53), UDP),
           socketserver.ThreadingTCPServer(('0.0.0.0', 53), TCP),
           http.server.ThreadingHTTPServer(('0.0.0.0', 80), HTTP),
           http.server.ThreadingHTTPServer(('0.0.0.0', 443), HTTP)]
context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
context.load_cert_chain('/lab/cert.pem', '/lab/key.pem')
servers[-1].socket = context.wrap_socket(servers[-1].socket, server_side=True)
for server in servers[:-1]:
    threading.Thread(target=server.serve_forever, daemon=True).start()
servers[-1].serve_forever()
