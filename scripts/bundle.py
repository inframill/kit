#!/usr/bin/env python3
"""Build both platforms to a local OCI layout. Never authenticate or push."""
import hashlib,json,os,pathlib,re,shutil,subprocess,tarfile,tempfile
from bundle_inputs import read_binary, read_driver
from release_config import bundle_lock, project_version, python_requirements
ROOT=pathlib.Path(__file__).resolve().parent.parent
version=project_version()
image_reference=os.environ.get('REGISTRY','ghcr.io')+'/'+os.environ.get('NAMESPACE','inframill')+'/kit:'+version
if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?',version):raise SystemExit('Exact bundle SemVer required')
subprocess.run(['go','run','./cmd/kit-build','bundle:validate'],cwd=ROOT,check=True)
lock_path=ROOT/f'releases/{version}.lock.json';lock=bundle_lock(version)
if lock['version']!=version:raise SystemExit('Bundle lock identity mismatch')
source_revision=lock['inframill']['revision']
drivers_dist=(ROOT/os.environ.get('DRIVERS_DIST_DIR','drivers/dist')).resolve()
inframill_dist=(ROOT/os.environ.get('INFRAMILL_DIST_DIR','../inframill/dist')).resolve()
inputs={arch:lock['inframill']['binaries']['linux/'+arch] for arch in ['amd64','arm64']}
try:
    inframill_binaries={arch:read_binary(inframill_dist,inputs[arch],arch) for arch in ['amd64','arm64']}
except (ValueError,OSError) as error:raise SystemExit(str(error))
if set(lock['platforms'])!={'linux/amd64','linux/arm64'}:raise SystemExit('Both platforms required')
# Only prebuilt driver artifacts enter the image. No driver source checkout or compiler is used.
try:
    driver_inputs={(entry['name'],entry['version'],target):read_driver(drivers_dist,entry,target,lock)
                   for entry in lock['drivers'] for target in lock['platforms']}
except (ValueError,OSError,KeyError) as error:raise SystemExit(str(error))
output=(ROOT/os.environ.get('KIT_DIST_DIR','dist'))/f'bundles/{version}'
if output.exists():raise SystemExit('Bundle output already exists; push it or select a new version. Explicitly move development output aside before rebuilding.')
output.parent.mkdir(parents=True,exist_ok=True)
def run(args,**kwargs):
    print('+',' '.join(map(str,args)),flush=True);subprocess.run(list(map(str,args)),check=True,**kwargs)
with tempfile.TemporaryDirectory(prefix='.bundle-',dir=output.parent) as td:
    stage=pathlib.Path(td);context=stage/'context';context.mkdir();sources=context/'sources';sources.mkdir()
    for name in ['VERSION','go.mod','go.sum','LICENSE','README.md','docs','cmd','internal','releases','scripts','runtime','Taskfile.yml']:
        p=ROOT/name
        if p.is_dir():shutil.copytree(p,sources/name,ignore=shutil.ignore_patterns('__pycache__'))
        else:shutil.copy2(p,sources/name)
    for arch in ['amd64','arm64']:
        bindir=context/f'bin/linux/{arch}';bindir.mkdir(parents=True)
        (bindir/'inframill').write_bytes(inframill_binaries[arch])
        (bindir/'inframill').chmod(0o755)
        for entry in lock['drivers']:
            folder,record,data=driver_inputs[(entry['name'],entry['version'],'linux/'+arch)]
            destination=context/'drivers'/arch/entry['name']/entry['version']
            destination.mkdir(parents=True)
            (destination/'driver').write_bytes(data)
            (destination/'driver').chmod(0o755)
            shutil.copy2(folder/'build.json',destination/'build.json')
            # Each architecture's sources and dependency notices accompany its executable.
            for name in ['sources','notices']:
                if (folder/name).is_dir():shutil.copytree(folder/name,destination/name)
    shutil.copy2(ROOT/'runtime/inframill.yaml',context/'inframill.yaml')
    for name in ['Dockerfile','NOTICES.md']:shutil.copy2(ROOT/'runtime'/name,context/name)
    (context/'requirements.lock').write_text(python_requirements(lock))
    shutil.copy2(ROOT/'scripts/fetch-runtime.py',context/'fetch-runtime.py');(context/'bundle.lock.json').write_text(json.dumps(lock,indent=2)+'\n')
    tar=stage/'image.tar'
    run(['docker','buildx','build','--platform','linux/amd64,linux/arm64','--provenance=false','--sbom=false','--build-arg','OPENTOFU_VERSION='+lock['opentofu']['version'],'--build-arg','ANSIBLE_VERSION='+lock['ansible_core'],'--build-arg','INFRAMILL_VERSION='+lock['inframill']['version'],'--build-arg','DEBIAN_SNAPSHOT='+lock['debian_snapshot'],'--build-arg','BASE_IMAGE='+lock['base_image'],'--build-arg','SOURCE_DATE_EPOCH='+str(lock['source_date_epoch']),'--output','type=oci,dest='+str(tar)+',rewrite-timestamp=true','--tag',image_reference,context])
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
    record={'dependency_revision':source_revision,'inframill':lock['inframill'],'source_files':{str(p.relative_to(sources)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sources.rglob('*') if p.is_file()},'version':version,'lock_sha256':hashlib.sha256(lock_path.read_bytes()).hexdigest(),'descriptor':descriptor,'platforms':rootmanifest['manifests'],'drivers':{name+'@'+version+'/'+target:record for (name,version,target),(_,record,_) in driver_inputs.items()},'binaries':{str(p.relative_to(context)):hashlib.sha256(p.read_bytes()).hexdigest() for p in (context/'bin').rglob('*') if p.is_file()}}
    output.mkdir();shutil.move(str(layout),output/'oci');(output/'build.json').write_text(json.dumps(record,indent=2)+'\n')
    print('Built local bundle:',output,'digest:',descriptor['digest'])
