#!/usr/bin/env python3
"""Verify a built release and service/proxy syntax using owned temporary fixtures.
Requires systemd-analyze, Docker and OpenSSL. Does not start host services.
"""
import io
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile

release = Path(sys.argv[1]).resolve()
subprocess.run(['sha256sum', '--check', '--status', 'SHA256SUMS'], cwd=release, check=True)
with tempfile.TemporaryDirectory(prefix='conductor-release-check-') as directory:
    work = Path(directory)
    unit = (release / 'release/systemd/conductor@.service').read_text()
    unit = unit.replace('/opt/conductor/current', str(release))
    unit_path = work / 'conductor@conductord.service'
    unit_path.write_text(unit)
    subprocess.run(['systemd-analyze', 'verify', str(unit_path)], check=True)
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                    '-subj', '/CN=conductor.example.invalid', '-keyout', str(work / 'conductor-key.pem'),
                    '-out', str(work / 'conductor.pem')], check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    config = (release / 'release/nginx.conf.example').read_text()
    (work / 'nginx.conf').write_text('events {}\nhttp {\n' + config + '\n}\n')
    stream = io.BytesIO()
    with tarfile.open(fileobj=stream, mode='w') as archive:
        for name in ['nginx.conf', 'conductor-key.pem', 'conductor.pem']:
            archive.add(work / name, arcname=name)
    subprocess.run(['docker', 'run', '--rm', '-i', '--network', 'none', '--entrypoint', 'sh',
                    'nginx:1.30.4-alpine@sha256:dc5069ad14f19660b141b21236140b91656bf89bbc3e2417c70ae650cd66104c',
                    '-c', 'mkdir -p /etc/nginx/tls; tar -xf - -C /etc/nginx/tls; nginx -t -c /etc/nginx/tls/nginx.conf'],
                   input=stream.getvalue(), check=True)
print('Release checksums, packaged API service and nginx TLS configuration verified.')
