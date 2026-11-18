"""Exercise the real image against an offline DNS/HTTP/TLS peer in Docker.

Requires Docker and an openssl executable. No public DNS or game service is used.
"""
import argparse
import contextlib
import json
import random
import subprocess
import tempfile
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def docker(*args, **kwargs):
    return subprocess.run(['docker', *args], check=True, text=True,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE, **kwargs).stdout.strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', default='hopdns:local')
    parser.add_argument('--keep', action='store_true', help='Keep the isolated lab for manual review; prints cleanup commands')
    args = parser.parse_args()
    suffix = str(random.randrange(100000, 999999))
    network, app, origin = (f'hopdns-{part}-{suffix}' for part in ('lab', 'app', 'origin'))
    subnet = f'198.18.{random.randrange(1, 250)}'
    app_ip, origin_ip = subnet + '.10', subnet + '.20'
    volume = f'hopdns-data-{suffix}'
    directory_context = contextlib.nullcontext(tempfile.mkdtemp(prefix='hopdns-lab-')) if args.keep else tempfile.TemporaryDirectory(prefix='hopdns-lab-')
    with directory_context as directory:
        lab = Path(directory)
        (lab / 'openssl.cnf').write_text('[req]\ndistinguished_name=dn\nx509_extensions=ext\nprompt=no\n[dn]\nCN=selected.hopdns.test\n[ext]\nsubjectAltName=DNS:selected.hopdns.test,DNS:other.hopdns.test,DNS:sub.selected.hopdns.test\nbasicConstraints=critical,CA:TRUE\nkeyUsage=critical,digitalSignature,keyEncipherment,keyCertSign\n')
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                        '-days', '2', '-config', str(lab / 'openssl.cnf'),
                        '-keyout', str(lab / 'key.pem'), '-out', str(lab / 'cert.pem')],
                       check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        (lab / 'origin.py').write_bytes((ROOT / 'tests/lab_origin.py').read_bytes())
        # Docker's fixed application UID must read the synthetic test certificate.
        lab.chmod(0o755)
        (lab / 'cert.pem').chmod(0o644)
        try:
            docker('network', 'create', '--internal', '--subnet', subnet + '.0/24', network)
            docker('run', '-d', '--name', origin, '--network', network, '--ip', origin_ip,
                   '-e', 'LAB_IP=' + origin_ip, '-v', str(lab) + ':/lab:ro',
                   '--user', '0:0', '--entrypoint', 'python3', args.image, '/lab/origin.py')
            docker('run', '-d', '--name', app, '--network', network, '--ip', app_ip,
                   '--add-host', 'selected.hopdns.test:' + origin_ip,
                   '--add-host', 'other.hopdns.test:' + origin_ip,
                   '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true',
                   '--sysctl', 'net.ipv4.ip_unprivileged_port_start=0',
                   '-v', volume + ':/data',
                   '--tmpfs', '/run/hopdns:uid=10001,gid=10001,mode=700',
                   '--tmpfs', '/tmp', '-v', str(lab) + ':/lab:ro', args.image)

            def python(code):
                return docker('exec', '-i', app, 'python3', '-', input=code)

            for _ in range(40):
                try:
                    python("import urllib.request; assert urllib.request.urlopen('http://127.0.0.1:8787/healthz',timeout=1).status == 200")
                    break
                except subprocess.CalledProcessError:
                    time.sleep(0.25)
            else:
                raise AssertionError('Application did not become ready')
            config = {'public_ipv4': app_ip, 'upstream_dns': f'udp://{origin_ip}:53',
                      'mode': 'selected', 'domains': ['selected.hopdns.test']}
            common = '''import concurrent.futures,http.client,json,socket,ssl,struct,time,urllib.request
BASE='http://127.0.0.1:8787'
def api(path='/api/status'):
    return json.load(urllib.request.urlopen(BASE+path,timeout=3))
def save(config):
    request=urllib.request.Request(BASE+'/api/config',data=json.dumps(config).encode(),method='PUT',headers={'Content-Type':'application/json','X-HopDNS-Request':'1','Origin':BASE})
    return json.load(urllib.request.urlopen(request,timeout=8))
def ready():
    for _ in range(50):
        state=api()
        if state['state']=='running':return state
        time.sleep(.2)
    raise AssertionError(state)
def dns(name,tcp=False,kind=1):
    query=struct.pack('!HHHHHH',1234,256,1,0,0,0)+b''.join(bytes([len(x)])+x.encode() for x in name.split('.'))+b'\\0'+struct.pack('!HH',kind,1)
    with socket.socket(socket.AF_INET,socket.SOCK_STREAM if tcp else socket.SOCK_DGRAM) as s:
        s.settimeout(3)
        s.connect(('127.0.0.1',53))
        if tcp:
            s.sendall(struct.pack('!H',len(query))+query)
            size=struct.unpack('!H',s.recv(2))[0]
            response=b''
            while len(response)<size:response+=s.recv(size-len(response))
        else:s.send(query);response=s.recv(4096)
    if kind==28:
        assert struct.unpack('!H',response[6:8])[0]==0,response.hex()
        return None
    assert struct.unpack('!H',response[6:8])[0]==1,response.hex()
    result=socket.inet_ntoa(response[-4:])
    print('DNS',name,'TCP' if tcp else 'UDP',result,flush=True)
    return result
def tls(name):
    context=ssl.create_default_context(cafile='/lab/cert.pem')
    with socket.create_connection(('127.0.0.1',443),timeout=3) as raw:
        with context.wrap_socket(raw,server_hostname=name) as stream:
            stream.sendall(('GET / HTTP/1.1\\r\\nHost: '+name+'\\r\\nConnection: close\\r\\n\\r\\n').encode())
            payload=b''
            while True:
                chunk=stream.recv(65536)
                if not chunk:break
                payload+=chunk
            header,separator,body=payload.partition(b'\\r\\n\\r\\n')
            assert separator and b'200 OK' in header and body==b'hopdns-offline-origin\\n'*4096
def request_http(name):
    conn=http.client.HTTPConnection('127.0.0.1',80,timeout=3)
    conn.request('GET','/',headers={'Host':name})
    response=conn.getresponse();body=response.read();conn.close()
    assert response.status==200 and b'hopdns-offline-origin' in body
'''
            python(common + f'''
config={config!r}
assert api()['state']=='setup_required'
assert save(config)['restarted']
idle=ready()
assert idle['rx_bytes']==idle['tx_bytes']==idle['https_proxied']==0, idle
assert dns('selected.hopdns.test',kind=28) is None
assert dns('selected.hopdns.test')=={app_ip!r}
assert dns('selected.hopdns.test',True)=={app_ip!r}
assert dns('sub.selected.hopdns.test')=={app_ip!r}
assert dns('evil-selected.hopdns.test')=={origin_ip!r}
assert dns('other.hopdns.test')=={origin_ip!r}
for _ in range(40):
    idle=api()
    if (idle['dns_requests'] or 0)>=6:break
    time.sleep(.25)
else:raise AssertionError(idle)
assert idle['rx_bytes']==idle['tx_bytes']==0, idle
request_http('selected.hopdns.test')
body_size=len(b'hopdns-offline-origin\\n')*4096
for _ in range(40):
    forwarded=api()
    if forwarded['http_proxied']==1 and (forwarded['rx_bytes'] or 0)>=body_size:break
    time.sleep(.25)
else:raise AssertionError(forwarded)
assert forwarded['rx_bytes']==body_size and forwarded['tx_bytes']==0, forwarded
tls('selected.hopdns.test')
''')
            # Polling is concurrent with real traffic. This is correctness under
            # dashboard load, not a benchmark or a claim of zero overhead.
            python(common + '''
with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
    work=[pool.submit(lambda:[api() for _ in range(20)]) for _ in range(4)]
    work += [pool.submit(lambda:[tls('selected.hopdns.test') for _ in range(5)]),pool.submit(lambda:[request_http('selected.hopdns.test') for _ in range(5)])]
    for task in work:task.result()
conn=http.client.HTTPConnection('127.0.0.1',80,timeout=3)
conn.request('GET','/',headers={'Host':'127.0.0.1:8787'})
assert conn.getresponse().status in (403,404)
conn.close()
for path,headers in [('/api/config',{'Host':'evil.test'}),('/api/status',{'Origin':'https://evil.test'})]:
    conn=http.client.HTTPConnection('127.0.0.1',8787,timeout=3);conn.request('GET',path,headers=headers);assert conn.getresponse().status==403;conn.close()
conn=http.client.HTTPConnection('127.0.0.1',8787,timeout=3)
conn.request('PUT','/api/config',body='{}',headers={'Content-Type':'application/json'})
assert conn.getresponse().status==403
conn.close()
for _ in range(40):
    status=api()
    if (status['dns_requests'] or 0)>=6 and (status['https_proxied'] or 0)>=6 and (status['http_proxied'] or 0)>=6:break
    time.sleep(.25)
else:raise AssertionError(status)
assert status['rx_bytes']>0 and status['tx_bytes']>0
assert len(status['history'])<=120
config=api('/api/config');config['mode']='all';assert save(config)['restarted'];idle=ready()
assert idle['rx_bytes']==idle['tx_bytes']==idle['https_proxied']==0, idle
conn=http.client.HTTPConnection('127.0.0.1',80,timeout=3);conn.request('GET','/',headers={'Host':'127.0.0.1:8787'});assert conn.getresponse().status==404;conn.close()
'''+f"assert dns('other.hopdns.test')=={app_ip!r}\ntls('other.hopdns.test')\n")
            docker('restart', app)
            for _ in range(40):
                try:
                    python(common + "assert api('/api/config')['mode']=='all';ready();tls('other.hopdns.test')")
                    break
                except subprocess.CalledProcessError:
                    time.sleep(.25)
            else:
                raise AssertionError('Configuration did not survive container restart')
            # Metrics remain loopback-only; not even another lab container can scrape.
            docker('exec', origin, 'python3', '-c',
                   f"import socket; s=socket.socket();s.settimeout(1);assert s.connect_ex(({app_ip!r},9090)) != 0")
            print('PASS: selected/all DNS, label boundaries, TCP DNS, HTTP, verified TLS, dashboard concurrency, API isolation, private destination refusal, metrics, restart persistence')
        except Exception as error:
            if isinstance(error, subprocess.CalledProcessError):
                print(error.stdout)
                print(error.stderr)
            for container in (app, origin):
                try:
                    print(docker('logs', '--tail', '30', container))
                except subprocess.CalledProcessError:
                    pass
            raise
        finally:
            if args.keep:
                print(f'Kept lab: app={app}, origin={origin}, network={network}, volume={volume}, files={lab}')
                print(f'For browser review, use Docker --network container:{app} and http://127.0.0.1:8787 (no host ports in this offline lab).')
                print(f'Cleanup: docker rm -f {app} {origin}; docker network rm {network}; docker volume rm {volume}; rm -rf {lab}')
            else:
                for container in (app, origin):
                    subprocess.run(['docker', 'rm', '-f', container], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                subprocess.run(['docker', 'network', 'rm', network], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                subprocess.run(['docker', 'volume', 'rm', volume], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == '__main__':
    main()
