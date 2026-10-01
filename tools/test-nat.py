#!/usr/bin/env python3
"""Local, offline two-NAT lab. Never changes host firewall rules or uses SSH.

Requires Docker and a local Rust Iroh 1.3.0 relay image (RELAY_IMAGE override).
Run from any directory: python3 tools/test-nat.py
"""
import ipaddress
import json
import os
import pathlib
import re
import subprocess
import time
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[1]
PREFIX = 'agentbus-nat-' + uuid.uuid4().hex[:8]
IMAGE = PREFIX + ':test'
networks, containers = [], []

def docker(*args, input=None, timeout=40):
    p = subprocess.run(['docker', *args], input=input, text=True,
                       capture_output=True, timeout=timeout)
    if p.returncode:
        # Tickets can occur in container logs; never include those in errors.
        raise RuntimeError(re.sub(r'ab1[A-Za-z0-9_-]{60,}', '[TICKET]', p.stderr[-2500:]))
    return p.stdout

def execute(name, command):
    return docker('exec', PREFIX+'-'+name, 'sh', '-ec', command)

def write(name, path, text):
    docker('exec', '-i', PREFIX+'-'+name, 'sh', '-c', 'cat > '+path, input=text)

def start(name, network, ip, admin=False):
    full = PREFIX+'-'+name
    containers.append(full)
    args = ['run', '-d', '--name', full, '--network', PREFIX+'-'+network,
            '--ip', ip, '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges']
    if admin:
        args += ['--cap-add', 'NET_ADMIN', '--sysctl', 'net.ipv4.ip_forward=1']
        if name.startswith('router-'):
            args += ['--cap-add', 'NET_RAW', '--cap-add', 'SETUID', '--cap-add', 'SETGID']
    else:
        args += ['--cap-add', 'NET_BIND_SERVICE']
    docker(*args, IMAGE)

def wait(check, timeout=35):
    deadline = time.monotonic()+timeout
    while time.monotonic() < deadline:
        result = check()
        if result:
            return result
        time.sleep(.4)
    raise RuntimeError('NAT lab condition timed out')

def rows():
    lines = execute('b', 'cat /tmp/probe.log').splitlines()
    return [json.loads(s) for s in lines if s.startswith('{')]

def traffic(phase, relayed):
    snapshots = [r for r in rows() if r['phase'] == phase]
    totals = [sum(p['BytesSent'] for p in row['paths'] or []
                  if p['Validated'] and p['Selected'] and p['Relayed'] == relayed)
              for row in snapshots]
    # Real PONGs, native per-path bytes and relay-denial controls prove traffic.
    return len(totals) > 8 and totals[-1] > totals[0]

try:
    relay_image = os.environ.get('RELAY_IMAGE', 'agentbus-nat-relay:1.3.0')
    assert docker('run', '--rm', '--network', 'none', '--cap-drop', 'ALL',
                  '--entrypoint', '/usr/local/bin/iroh-relay', relay_image, '-V').strip() == 'iroh-relay 1.3.0', 'relay fixture version mismatch'
    subnets = ['10.253.230.0/24', '10.253.231.0/24', '10.253.232.0/24']
    existing = docker('network', 'ls', '-q').split()
    for network in json.loads(docker('network', 'inspect', *existing)):
        for config in network['IPAM']['Config'] or []:
            if config.get('Subnet'):
                assert not any(ipaddress.ip_network(config['Subnet']).overlaps(ipaddress.ip_network(s))
                               for s in subnets), 'test subnet overlaps an existing Docker network'
    docker('build', '-f', str(ROOT/'tools/nat-test/Dockerfile'),
           '--build-arg', 'RELAY_IMAGE='+relay_image,
           '-t', IMAGE, str(ROOT), timeout=600)
    for name, subnet in zip(['wan', 'a', 'b'], subnets):
        networks.append(PREFIX+'-'+name)
        # --internal bridges drop routed packets before our NAT container sees
        # them. Disable Docker masquerading; isolate in container rules instead.
        docker('network', 'create', '--opt', 'com.docker.network.bridge.enable_ip_masquerade=false', '--subnet', subnet, PREFIX+'-'+name)
    start('relay', 'wan', '10.253.230.10', True)
    execute('relay', 'ip route del default')
    for name, octet, wan in [('a', 231, 2), ('b', 232, 3)]:
        start('router-'+name, 'wan', '10.253.230.'+str(wan), True)
        docker('network', 'connect', '--ip', f'10.253.{octet}.2', PREFIX+'-'+name, PREFIX+'-router-'+name)
        execute('router-'+name, 'ip route del default')
        start(name, name, f'10.253.{octet}.3', True)
        execute(name, f'ip route replace default via 10.253.{octet}.2')
        execute(name, f'iptables -A OUTPUT -o lo -j ACCEPT; iptables -A OUTPUT -d 10.253.230.0/24 -j ACCEPT; iptables -A OUTPUT -d 10.253.{octet}.0/24 -j ACCEPT; iptables -P OUTPUT DROP; ip6tables -P OUTPUT DROP')
        # Docker assigns WAN eth0 and the subsequently attached LAN eth1.
        # Endpoint-independent SNAT mapping, with stateful inbound filtering.
        execute('router-'+name, f'''
iptables -P FORWARD DROP
iptables -A FORWARD -p udp -d 10.253.230.0/24
iptables -A FORWARD -d 10.253.{463-octet}.0/24 -j DROP
iptables -A FORWARD -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -A FORWARD -i eth1 -o eth0 -d 10.253.230.0/24 -j ACCEPT
iptables -t nat -A PREROUTING -i eth0 -p udp -d 10.253.230.{wan} -j DNAT --to-destination 10.253.{octet}.3
iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE
''')
        execute('router-'+name, 'tcpdump -l -n -i eth0 udp > /tmp/udp.log 2>&1 &')
    execute('relay', '''openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
 -keyout /tmp/key.pem -out /tmp/cert.pem -subj /CN=nat-relay \
 -addext basicConstraints=critical,CA:FALSE -addext subjectAltName=IP:10.253.230.10 >/dev/null 2>&1''')
    cert = execute('relay', 'cat /tmp/cert.pem')
    for name in ['a', 'b']:
        write(name, '/tmp/cert.pem', cert)
    write('relay', '/tmp/relay.toml', '''enable_quic_addr_discovery = true
enable_metrics = false
http_bind_addr = "0.0.0.0:80"
[tls]
https_bind_addr = "0.0.0.0:443"
quic_bind_addr = "0.0.0.0:7842"
cert_mode = "Manual"
manual_cert_path = "/tmp/cert.pem"
manual_key_path = "/tmp/key.pem"
''')
    execute('relay', 'iroh-relay -c /tmp/relay.toml > /tmp/relay.log 2>&1 &')
    wait(lambda: ':443' in execute('relay', 'ss -ltn'), 10)
    execute('a', "python3 -c 'import socket,ssl; c=ssl.create_default_context(cafile=\"/tmp/cert.pem\"); s=c.wrap_socket(socket.create_connection((\"10.253.230.10\",443),3),server_hostname=\"10.253.230.10\"); s.close()'")
    execute('a', 'QLOGDIR=/tmp/qlog SSL_CERT_FILE=/tmp/cert.pem AGENTBUS_RELAY=https://10.253.230.10/ agentbus host --name hub > /tmp/host.log 2>&1 &')
    ticket = wait(lambda: re.search(r'ab1[A-Za-z0-9_-]{60,}', execute('a', 'cat /tmp/host.log')))
    write('b', '/tmp/ticket', ticket.group())
    # Test actual packet reachability, not just route-table intent.
    execute('a', "python3 -c 'import socket,time; s=socket.socket(); s.bind((\"0.0.0.0\",9999)); s.listen(); time.sleep(110)' >/tmp/listener.log 2>&1 &")
    wait(lambda: ':9999' in execute('a', 'ss -ltn'), 5)
    assert execute('b', "python3 -c 'import socket; s=socket.socket(); s.settimeout(2); print(s.connect_ex((\"10.253.231.3\",9999)) != 0)' ").strip() == 'True'
    print('PASS: peer private-LAN shortcut blocked; endpoints behind separate stateful NAT gateways', flush=True)
    write('a', '/tmp/udp-control.py', '''import socket,pathlib
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM)
s.bind(("0.0.0.0",9998)); s.settimeout(2)
pathlib.Path('/tmp/udp-ready').touch()
try:
 s.recvfrom(100); result='leaked'
except socket.timeout:
 result='blocked'
pathlib.Path('/tmp/udp-control').write_text(result)
''')
    execute('a', 'python3 /tmp/udp-control.py &')
    wait(lambda: execute('a', 'test -f /tmp/udp-ready && echo ready || true').strip(), 5)
    execute('relay', "python3 -c 'import socket; s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.sendto(b\"unsolicited\",(\"10.253.230.2\",9998))'")
    result = wait(lambda: execute('a', 'cat /tmp/udp-control 2>/dev/null || true').strip(), 5)
    assert result == 'blocked', 'NAT unexpectedly admits unsolicited UDP'
    print('PASS: unsolicited WAN UDP rejected despite address translation', flush=True)
    write('b', '/tmp/phase', 'direct')
    execute('b', 'QLOGDIR=/tmp/qlog SSL_CERT_FILE=/tmp/cert.pem probe -test.run ^TestNATProbe$ -test.v > /tmp/probe.log 2>&1 &')
    wait(lambda: traffic('direct', False), 75)
    for name in ['a', 'b']:
        execute('router-'+name, 'iptables -I FORWARD 1 -p tcp -d 10.253.230.10 --dport 443 -j DROP')
    write('b', '/tmp/phase', 'direct-no-relay')
    wait(lambda: traffic('direct-no-relay', False), 10)
    print('PASS: validated direct path carries application bytes across both NAT gateways', flush=True)
    for name in ['a', 'b']:
        execute('router-'+name, 'iptables -D FORWARD -p tcp -d 10.253.230.10 --dport 443 -j DROP')
    for cycle in [1, 2]:
        # Deny only peer-to-peer UDP. QUIC discovery to the relay and HTTPS remain available.
        for name, peer in [('a', 3), ('b', 2)]:
            execute('router-'+name, f'iptables -I FORWARD 1 -p udp -d 10.253.230.{peer} -j DROP')
        write('b', '/tmp/phase', 'blocked-'+str(cycle))
        wait(lambda: traffic('blocked-'+str(cycle), True), 60)
        print('PASS: same connection carries application bytes through relay with peer UDP blocked', flush=True)
        for name, peer in [('a', 3), ('b', 2)]:
            execute('router-'+name, f'iptables -D FORWARD -p udp -d 10.253.230.{peer} -j DROP')
        write('b', '/tmp/phase', 'recovery-'+str(cycle))
        wait(lambda: traffic('recovery-'+str(cycle), False), 75)
        for name in ['a', 'b']:
            execute('router-'+name, 'iptables -I FORWARD 1 -p tcp -d 10.253.230.10 --dport 443 -j DROP')
        write('b', '/tmp/phase', 'recovery-no-relay-'+str(cycle))
        wait(lambda: traffic('recovery-no-relay-'+str(cycle), False), 10)
        print('PASS: direct application traffic recovers after peer UDP is restored', flush=True)
        for name in ['a', 'b']:
            execute('router-'+name, 'iptables -D FORWARD -p tcp -d 10.253.230.10 --dport 443 -j DROP')
    execute('b', 'touch /tmp/stop')
    wait(lambda: 'PASS' in execute('b', 'cat /tmp/probe.log'), 10)
except Exception:
    for name, path in [('relay', '/tmp/relay.log'), ('a', '/tmp/host.log'), ('b', '/tmp/probe.log'), ('router-a', '/tmp/udp.log'), ('router-b', '/tmp/udp.log')]:
        try:
            log = execute(name, '(cat '+path+' 2>/dev/null || true); ip route; ss -ltnu; iptables -L FORWARD -nv 2>/dev/null || true; iptables -t nat -L POSTROUTING -nv 2>/dev/null || true')
            print(name+': '+re.sub(r'ab1[A-Za-z0-9_-]{60,}', '[TICKET]', log[-3000:]), flush=True)
        except Exception:
            pass
    raise
finally:
    # Restrict cleanup to this invocation's names. No host firewall commands.
    for container in reversed(containers):
        subprocess.run(['docker', 'rm', '-f', container], capture_output=True)
    for network in reversed(networks):
        subprocess.run(['docker', 'network', 'rm', network], capture_output=True)
    subprocess.run(['docker', 'image', 'rm', IMAGE], capture_output=True)
    assert not any(n.startswith(PREFIX+'-') for n in docker('ps', '-a', '--format', '{{.Names}}').splitlines()), 'test container cleanup failed'
    assert not any(n.startswith(PREFIX+'-') for n in docker('network', 'ls', '--format', '{{.Name}}').splitlines()), 'test network cleanup failed'
    print('CLEANUP: disposable local containers, networks and image removed', flush=True)
