#!/usr/bin/env python3
"""Install the pinned test tools into .cache; never install globally."""
import hashlib,json,os,pathlib,platform,subprocess,sys,urllib.request,zipfile
root=pathlib.Path(__file__).resolve().parent.parent
python=os.environ.get('PYTHON','python3')
subprocess.run([python,'-c','import sys; assert (3,12) <= sys.version_info[:2] <= (3,14), "Python 3.12–3.14 required"'],check=True)
arch={'aarch64':'arm64','arm64':'arm64','x86_64':'amd64'}.get(platform.machine())
system=platform.system().lower();lock=json.loads((root/'runtime/dev-tools.lock.json').read_text());asset=lock['opentofu'][system+'_'+arch]
folder=root/'.cache/tools/opentofu/1.11.5';folder.mkdir(parents=True,exist_ok=True)
with urllib.request.urlopen(asset['url'],timeout=120) as response:data=response.read(100<<20)
if hashlib.sha256(data).hexdigest()!=asset['sha256']:raise RuntimeError('OpenTofu checksum mismatch')
archive=folder/'download.zip';archive.write_bytes(data)
with zipfile.ZipFile(archive) as z:(folder/'tofu').write_bytes(z.read('tofu'))
(folder/'tofu').chmod(0o755);archive.unlink()
venv=root/'.cache/ansible-2.20.1'
subprocess.run([python,'-m','venv',str(venv)],check=True)
subprocess.run([str(venv/'bin/pip'),'install','--require-hashes','-r',str(root/'runtime/requirements.lock')],check=True)
print('Pinned test tools installed under .cache')
