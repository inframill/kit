#!/usr/bin/env python3
"""Fetch only checksum-pinned runtime assets inside a build stage."""
import hashlib,json,pathlib,sys,urllib.request,zipfile
lock=json.loads(pathlib.Path('/build/bundle.lock.json').read_text());arch=sys.argv[1]
def download(url,sha,path):
    path=pathlib.Path(path);path.parent.mkdir(parents=True,exist_ok=True)
    with urllib.request.urlopen(url,timeout=120) as r, path.open('wb') as f:
        h=hashlib.sha256()
        while True:
            b=r.read(1024*1024)
            if not b:break
            h.update(b);f.write(b)
    if h.hexdigest()!=sha:raise RuntimeError('Checksum mismatch for '+url)
    return path
asset=lock['opentofu']['assets']['linux/'+arch];archive=download(asset['url'],asset['sha256'],'/build/tofu.zip')
out=pathlib.Path('/opt/inframill/tools/opentofu')/lock['opentofu']['version'];out.mkdir(parents=True,exist_ok=True)
with zipfile.ZipFile(archive) as z:
    (out/'tofu').write_bytes(z.read('tofu'))
    for name in z.namelist():
        if 'LICENSE' in name.upper() and '/' not in name:(out/name).write_bytes(z.read(name))
(out/'tofu').chmod(0o755)
for source in [package['source'] for package in lock['python_dependencies']]+[lock['opentofu']['source']]:
    download(source['url'],source['sha256'],'/usr/share/inframill/sources/'+source['url'].rsplit('/',1)[-1])
