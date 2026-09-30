#!/usr/bin/env python3
"""Installer verification with local release fixtures; no network or login."""
import hashlib
import os
import pathlib
import subprocess
import tempfile
import io
import tarfile

installer = pathlib.Path(__file__).resolve().parents[1] / 'install.sh'
with tempfile.TemporaryDirectory() as d:
    root = pathlib.Path(d)
    fixture = root / 'release'
    fixture.mkdir()
    mock = root / 'mock'
    mock.mkdir()
    # Only release download is allowed; fallback cloning would make this fail.
    gh = mock / 'gh'
    gh.write_text('''#!/usr/bin/env python3
import os,pathlib,shutil,sys
if sys.argv[1:3] != ['release','download']: sys.exit(1)
out=pathlib.Path(sys.argv[sys.argv.index('-D')+1])
for p in pathlib.Path(os.environ['FIXTURE']).iterdir(): shutil.copy2(p,out/p.name)
''')
    gh.chmod(0o755)
    dest = root / 'bin'
    dest.mkdir()
    system = subprocess.check_output(['uname', '-s'], text=True).strip().lower()
    arch = subprocess.check_output(['uname', '-m'], text=True).strip()
    arch = {'x86_64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}[arch]
    asset = fixture / f'agentbus-{system}-{arch}.tar.gz'
    helper = '#!/bin/sh\necho "agentbus-iroh 0.4.0 (official iroh 1.3.0)"\n'
    binary = '#!/bin/sh\ncase "$1" in help) echo "agentbus ab1 tickets";; version) echo "agentbus v0.4.0 (fixture)";; esac\n'
    env = dict(os.environ, PATH=str(mock)+':'+os.environ['PATH'], FIXTURE=str(fixture), AGENTBUS_DEST=str(dest), AGENTBUS_VERSION='v0.4.0')
    def check(valid, content=binary, transport=helper, extra=None):
        with tarfile.open(asset, 'w:gz') as archive:
            files = {'agentbus': content}
            if transport is not None:
                files['agentbus-iroh'] = transport
            if extra:
                files.update(extra)
            for name, data in files.items():
                info = tarfile.TarInfo(name)
                info.size = len(data.encode())
                info.mode = 0o755
                archive.addfile(info, io.BytesIO(data.encode()))
        digest = hashlib.sha256(asset.read_bytes()).hexdigest() if valid else '0'*64
        (fixture / 'SHA256SUMS').write_text(f'{digest}  {asset.name}\n'+'1'*64+'  other-platform\n')
        (dest / 'agentbus').write_text('existing install')
        result = subprocess.run(['sh', str(installer)], env=env, capture_output=True, text=True)
        return result
    assert check(True).returncode == 0
    assert (dest / 'agentbus').read_text() == binary
    assert (dest / 'agentbus').resolve().with_name('agentbus-iroh').read_text() == helper
    assert check(False).returncode != 0
    assert (dest / 'agentbus').read_text() == 'existing install'
    assert check(True, binary.replace('ab1', 'tc')).returncode != 0
    assert (dest / 'agentbus').read_text() == 'existing install'
    assert check(True, binary.replace('v0.4.0', 'v0.3.1')).returncode != 0
    assert (dest / 'agentbus').read_text() == 'existing install'
    assert check(True, transport=None).returncode != 0
    assert (dest / 'agentbus').read_text() == 'existing install'
    assert check(True, transport=helper.replace('0.4.0', '0.3.1')).returncode != 0
    assert (dest / 'agentbus').read_text() == 'existing install'
    assert check(True, extra={'../escape': 'bad'}).returncode != 0
    assert (dest / 'agentbus').read_text() == 'existing install'
    (fixture / 'SHA256SUMS').unlink()
    assert subprocess.run(['sh', str(installer)], env=env, capture_output=True).returncode != 0
    assert (dest / 'agentbus').read_text() == 'existing install'
    assert not list(dest.glob('.agentbus-install.*'))
print('PASS: checksum verification, matching executable pair, archive traversal denial, atomic replacement and cleanup')
