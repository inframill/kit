#!/usr/bin/env python3
"""Install the pinned test tools into .cache; never install globally."""
import hashlib,json,os,pathlib,platform,subprocess,sys,urllib.request,zipfile,tempfile
from release_config import bundle_lock, python_requirements
from python_runtime import find_python
root=pathlib.Path(__file__).resolve().parent.parent
try:
    python,python_version=find_python(os.environ.get('PYTHON'))
except ValueError as error:
    raise SystemExit(str(error))
print(f'Using Python {python_version}: {python}',flush=True)
arch={'aarch64':'arm64','arm64':'arm64','x86_64':'amd64'}.get(platform.machine())
system=platform.system().lower();lock=bundle_lock();asset=lock['opentofu']['assets'][system+'/'+arch]
folder=root/f".cache/tools/opentofu/{lock['opentofu']['version']}";folder.mkdir(parents=True,exist_ok=True)
with urllib.request.urlopen(asset['url'],timeout=120) as response:data=response.read(100<<20)
if hashlib.sha256(data).hexdigest()!=asset['sha256']:raise RuntimeError('OpenTofu checksum mismatch')
archive=folder/'download.zip';archive.write_bytes(data)
with zipfile.ZipFile(archive) as z:(folder/'tofu').write_bytes(z.read('tofu'))
(folder/'tofu').chmod(0o755);archive.unlink()
venv=root/f".cache/ansible-{lock['ansible_core']}"
subprocess.run([python,'-m','venv',str(venv)],check=True)
with tempfile.TemporaryDirectory(prefix='kit-python-') as td:
    requirements=pathlib.Path(td)/'requirements.lock'
    requirements.write_text(python_requirements(lock))
    subprocess.run([str(venv/'bin/pip'),'install','--require-hashes','-r',str(requirements)],check=True)
print('Pinned test tools installed under .cache')
