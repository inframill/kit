#!/usr/bin/env python3
"""Record all distributions and source archives for an explicitly resolved pip report."""
import json, pathlib, sys, urllib.request
report=json.loads(pathlib.Path(sys.argv[1]).read_text())
requirements=[];sources=[]
for item in sorted(report['install'],key=lambda x:x['metadata']['name'].lower()):
    name=item['metadata']['name'];version=item['metadata']['version']
    with urllib.request.urlopen(f'https://pypi.org/pypi/{name}/{version}/json',timeout=60) as response:
        metadata=json.load(response)
    hashes=sorted({f['digests']['sha256'] for f in metadata['urls']})
    requirements.append(f'{name}=={version}'+''.join(' \\\n    --hash=sha256:'+h for h in hashes))
    for f in metadata['urls']:
        if f['packagetype']=='sdist':sources.append({'name':name,'version':version,'url':f['url'],'sha256':f['digests']['sha256']})
pathlib.Path('runtime/requirements.lock').write_text('\n'.join(requirements)+'\n')
pathlib.Path('runtime/python-sources.lock.json').write_text(json.dumps(sources,indent=2)+'\n')
