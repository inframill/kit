#!/usr/bin/env python3
"""Build both platforms to a local OCI layout. Never authenticate or push."""
import hashlib,json,os,pathlib,re,shutil,subprocess,tarfile,tempfile
from bundle_inputs import read_binary
from release_config import bundle_lock, configuration
from source_pin import verify_source
ROOT=pathlib.Path(__file__).resolve().parent.parent
version=os.environ.get('BUNDLE_VERSION','0.3.0')
image_reference=os.environ.get('REGISTRY','ghcr.io')+'/'+os.environ.get('NAMESPACE','inframill')+'/kit:'+version
if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?',version):raise SystemExit('Exact bundle SemVer required')
subprocess.run(['go','run','./cmd/kit-build','bundle:validate'],cwd=ROOT,env={**os.environ,'BUNDLE_VERSION':version},check=True)
lock_path=ROOT/f'releases/bundles/{version}.lock.yaml';lock=bundle_lock(version)
if lock['version']!=version:raise SystemExit('Bundle lock identity mismatch')
source_revision=verify_source(ROOT,lock['inframill']['revision'])
inframill_dist=(ROOT/os.environ.get('INFRAMILL_DIST_DIR','../inframill/dist')).resolve()
inputs=lock['inframill']['binaries']
if set(inputs)!={'amd64','arm64'}:raise SystemExit('Both prebuilt Inframill architectures required')
try:
    inframill_binaries={arch:read_binary(inframill_dist,inputs[arch],arch) for arch in ['amd64','arm64']}
except (ValueError,OSError) as error:raise SystemExit(str(error))
if subprocess.check_output(['go','env','GOVERSION'],text=True).strip()!=lock['go_version']:raise SystemExit('Go toolchain differs from bundle lock')
if set(lock['platforms'])!={'linux/amd64','linux/arm64'}:raise SystemExit('Both platforms required')
# Check the declared components against the executable that will be shipped.
compiled=json.loads(subprocess.check_output(['go','run','./cmd/inframill-kit','drivers','list'],cwd=ROOT,text=True))
catalog={(d['name'],d['version']):d['tool_version'] for d in compiled}
kit=json.loads((ROOT/f"releases/kits/{lock['kit_version']}.yaml").read_text())
if kit['inframill_version']!=lock['inframill']['version']:raise SystemExit('Kit/core version mismatch')
if kit['version']!=lock['kit_version'] or {(d['name'],d['version']) for d in kit['drivers']}!=set(catalog):raise SystemExit('Kit release differs from compiled catalog')
selected=set()
for driver in lock['drivers']:
    key=(driver['name'],driver['version'])
    if key not in catalog or key in selected:raise SystemExit('Unknown or duplicate bundle driver')
    selected.add(key)
    release=json.loads((ROOT/f'releases/drivers/{key[0]}/{key[1]}.yaml').read_text())
    installed={'opentofu':lock['opentofu']['version'],'ansible':lock['ansible_core']}.get(key[0])
    if release['name']!=key[0] or release['version']!=key[1] or release['tool_version']!=catalog[key] or installed!=catalog[key]:raise SystemExit('Driver/tool lock mismatch')
    if release['protocol']!=1 or set(release['platforms'])!=set(lock['platforms']):raise SystemExit('Driver platform/protocol mismatch')
if not selected:raise SystemExit('Bundle must select at least one driver')
output=(ROOT/os.environ.get('KIT_DIST_DIR','dist'))/f'bundles/{version}'
if output.exists():raise SystemExit('Bundle output already exists; push it or select a new version. Explicitly move development output aside before rebuilding.')
output.parent.mkdir(parents=True,exist_ok=True)
def run(args,**kwargs):
    print('+',' '.join(map(str,args)),flush=True);subprocess.run(list(map(str,args)),check=True,**kwargs)
with tempfile.TemporaryDirectory(prefix='.bundle-',dir=output.parent) as td:
    stage=pathlib.Path(td);context=stage/'context';context.mkdir();sources=context/'sources';sources.mkdir()
    for name in ['go.mod','go.sum','LICENSE','README.md','docs','cmd','internal','releases','scripts','runtime','Taskfile.yml']:
        p=ROOT/name
        if p.is_dir():shutil.copytree(p,sources/name,ignore=shutil.ignore_patterns('__pycache__'))
        else:shutil.copy2(p,sources/name)
    # Collect notices using the sibling source module; Inframill itself is only
    # copied from dist, never compiled here. No sibling source is embedded.
    module_dirs=set()
    for directory,target in [(ROOT,'./cmd/inframill-kit'),(ROOT.parent/'inframill','./cmd')]:
        text=subprocess.check_output(['go','list','-deps','-f','{{if .Module}}{{.Module.Dir}}{{end}}',target],cwd=directory,text=True)
        module_dirs.update(pathlib.Path(p) for p in text.splitlines() if p)
    notices=sources/'go-dependency-notices';notices.mkdir()
    for module in sorted(module_dirs):
        if module == ROOT:continue
        for pattern in ['LICENSE*','COPYING*','NOTICE*','AUTHORS*']:
            for file in module.glob(pattern):
                if file.is_file():
                    destination=notices/module.name/file.name;destination.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(file,destination)
    for arch in ['amd64','arm64']:
        bindir=context/f'bin/linux/{arch}';bindir.mkdir(parents=True)
        env={**os.environ,'GOOS':'linux','GOARCH':arch,'CGO_ENABLED':'0'}
        run(['go','build','-trimpath','-buildvcs=false','-ldflags=-X main.version='+lock['kit_version'],'-o',bindir/'inframill-kit','./cmd/inframill-kit'],cwd=ROOT,env=env)
        (bindir/'inframill').write_bytes(inframill_binaries[arch])
        (bindir/'inframill').chmod(0o755)
    (context/'inframill.yaml').write_text(configuration('/usr/local/bin/inframill-kit','/var/lib/inframill',drivers=lock['drivers'],format='yaml'))
    for name in ['Dockerfile','requirements.lock','python-sources.lock.json','NOTICES.md']:shutil.copy2(ROOT/'runtime'/name,context/name)
    shutil.copy2(ROOT/'scripts/fetch-runtime.py',context/'fetch-runtime.py');(context/'bundle.lock.json').write_text(json.dumps(lock,indent=2)+'\n')
    tar=stage/'image.tar'
    run(['docker','buildx','build','--platform','linux/amd64,linux/arm64','--provenance=false','--sbom=false','--build-arg','OPENTOFU_VERSION='+lock['opentofu']['version'],'--build-arg','ANSIBLE_VERSION='+lock['ansible_core'],'--build-arg','INFRAMILL_VERSION='+lock['inframill']['version'],'--build-arg','BASE_IMAGE='+lock['base_image'],'--build-arg','SOURCE_DATE_EPOCH='+str(lock['source_date_epoch']),'--output','type=oci,dest='+str(tar)+',rewrite-timestamp=true','--tag',image_reference,context])
    layout=stage/'oci';layout.mkdir()
    with tarfile.open(tar) as archive:
        for item in archive:
            parts=pathlib.PurePosixPath(item.name)
            if parts.is_absolute() or '..' in parts.parts or not (item.isfile() or item.isdir()):raise RuntimeError('Unsafe OCI archive entry')
            archive.extract(item,layout)
    index=json.loads((layout/'index.json').read_text())
    if len(index['manifests'])!=1:raise RuntimeError('Expected one combined runtime index')
    descriptor=index['manifests'][0];descriptor.setdefault('annotations',{})['org.opencontainers.image.ref.name']=version
    (layout/'index.json').write_text(json.dumps(index,separators=(',',':')))
    rootmanifest=json.loads((layout/'blobs/sha256'/descriptor['digest'].split(':')[1]).read_text())
    found={m.get('platform',{}).get('architecture') for m in rootmanifest['manifests']}
    if found!={'amd64','arm64'}:raise RuntimeError('Missing runtime platform')
    record={'dependency_revision':source_revision,'inframill':lock['inframill'],'source_files':{str(p.relative_to(sources)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sources.rglob('*') if p.is_file()},'version':version,'lock_sha256':hashlib.sha256(lock_path.read_bytes()).hexdigest(),'descriptor':descriptor,'platforms':rootmanifest['manifests'],'binaries':{str(p.relative_to(context)):hashlib.sha256(p.read_bytes()).hexdigest() for p in (context/'bin').rglob('*') if p.is_file()}}
    output.mkdir();shutil.move(str(layout),output/'oci');(output/'build.json').write_text(json.dumps(record,indent=2)+'\n')
    print('Built local bundle:',output,'digest:',descriptor['digest'])
