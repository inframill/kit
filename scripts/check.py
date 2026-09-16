#!/usr/bin/env python3
"""Manual verification. Infrastructure effects are limited to disposable local fixtures."""
import contextlib, io, tarfile, json, os, pathlib, platform, shutil, subprocess, sys, tempfile, time, uuid
from daemon_check import daemon_check
from image_check import image_check
from release_config import ROOT, FIXTURES, local_tools, configuration, blueprint_root, driver_ids, fixture_cases, registry_image, bundle_output, bundle_lock

def run(args, **kwargs):
    print('+', ' '.join(map(str,args)),file=sys.stderr,flush=True)
    return subprocess.run(list(map(str,args)),check=True,text=True,**kwargs)
def tools():
    tofu,ansible=local_tools()
    for tool in [tofu,ansible,ROOT/'bin/inframill-kit',ROOT/'bin/inframill']:
        if not tool.is_file():raise RuntimeError(f'Missing prerequisite {tool}; see README setup')
    return tofu,ansible

def config(path,workspace,tofu,ansible):
    path.write_text(json.dumps(configuration(ROOT/'bin/inframill-kit',workspace,{'opentofu':tofu,'ansible':ansible})))

def execute(tmp,conf,instance,name,command,args=None,source=None,env=None):
    token=uuid.uuid4().hex;result=tmp/(token+'-result.json')
    cmd=[ROOT/'bin/inframill','local','exec',command,'--config',conf,'--values-file',instance,'--name',name,'--result-file',result]
    if source:cmd+=['--blueprint-dir',source]
    else:cmd+=['--blueprint',(env or os.environ)['VERIFY_BLUEPRINT_REFERENCE']]
    if args is not None:
        argfile=tmp/(token+'-args.json');argfile.write_text(json.dumps(args));cmd+=['--args-file',argfile]
    run(cmd,env=env,stdout=subprocess.DEVNULL)
    output=json.loads(result.read_text())
    if output['status']!='succeeded':raise RuntimeError(output)
    return output.get('result',{})

def local_flow(tmp,conf,version,reference=None,env=None):
    iid=tmp/('local-'+version+'.json');data=json.loads((FIXTURES/f'examples/local-resource-{version}.json').read_text())
    data['architecture']='arm64' if platform.machine() in ('arm64','aarch64') else 'amd64'
    if reference:env={**(env or os.environ),'VERIFY_BLUEPRINT_REFERENCE':reference}
    iid.write_text(json.dumps(data));src=None if reference else FIXTURES/f'blueprints/local-resource/versions/{version}'
    ids=driver_ids('local-resource',version);prefix=ids['opentofu'];ap=ids['ansible'];name='test/local-'+version.replace('.','-')
    def do(command,args=None):return execute(tmp,conf,iid,name,command,args,src,env)
    do(prefix+'.init');do(prefix+'.validate');plan=do(prefix+'.plan')
    do(prefix+'.apply',{'plan_id':plan['plan_id']});do(ap+'.inventory');do(ap+'.syntax-check',{'playbook':'site'});do(ap+'.check',{'playbook':'site'});do(ap+'.apply',{'playbook':'site'})
    destroy=do(prefix+'.plan',{'destroy':True});do(prefix+'.destroy',{'plan_id':destroy['plan_id']})

def blueprint_check(name,version):
    tofu,ansible=tools()
    with tempfile.TemporaryDirectory(prefix='kit-blueprint-') as td:
        tmp=pathlib.Path(td);conf=tmp/'config.json';config(conf,tmp/'data',tofu,ansible)
        if name=='local-resource':local_flow(tmp,conf,version);return
        if name!='pv-cloud' or version!='0.1.0':raise RuntimeError('No declared integration fixture for this blueprint version')
        # Syntax-check initializes/render-validates the Instance, without cloud access.
        instance=ROOT/'examples/pv-cloud-0.1.0.json';source=ROOT/'blueprints/pv-cloud/versions/0.1.0';ids=driver_ids(name,version)
        for architecture in ['arm64','amd64']:
            execute(tmp,conf,ROOT/f'examples/pv-cloud-{architecture}.json','test/pv-'+architecture,ids['ansible']+'.syntax-check',{'playbook':'site'},source)
        execute(tmp,conf,instance,'test/pv-cloud',ids['ansible']+'.syntax-check',{'playbook':'site'},source)
        dirs=list((tmp/'data').glob('instances/*/workspace/'+ids['opentofu']))
        if len(dirs)!=3:raise RuntimeError('Rendered OpenTofu workspaces not found')
        work=dirs[0]
        run([tofu,'init','-backend=false','-input=false','-lockfile=readonly','-no-color'],cwd=work)
        run([tofu,'validate','-no-color'],cwd=work)
        run([tofu,'test','-no-color'],cwd=work,env={**os.environ,'HCLOUD_TOKEN':'mock-only'})

@contextlib.contextmanager
def registry():
    image=registry_image();name='kit-test-'+uuid.uuid4().hex[:12]
    run(['docker','network','create',name],stdout=subprocess.DEVNULL)
    run(['docker','run','-d','--rm','--network',name,'--network-alias','registry','--name',name,'-p','127.0.0.1::5000',image],stdout=subprocess.DEVNULL)
    try:
        address=run(['docker','port',name,'5000/tcp'],capture_output=True).stdout.strip()
        yield address,name
    finally:
        run(['docker','stop',name],stdout=subprocess.DEVNULL)
        run(['docker','network','rm',name],stdout=subprocess.DEVNULL)

def registry_check():
    tofu,ansible=tools()
    with registry() as (address,network), tempfile.TemporaryDirectory(prefix='kit-registry-') as td:
        tmp=pathlib.Path(td);conf=tmp/'config.json';config(conf,tmp/'data',tofu,ansible)
        env={**os.environ,'KIT_DIST_DIR':str(tmp/'dist'),'REGISTRY':address,'NAMESPACE':'inframill','PLAIN_HTTP':'true','INFRAMILL_HTTP_REGISTRIES':address}
        run(['go','run','./cmd/kit-build','blueprints:package'],cwd=ROOT,env=env)
        run(['go','run','./cmd/kit-build','blueprints:push'],cwd=ROOT,env=env)
        run(['go','run','./cmd/kit-build','blueprints:push'],cwd=ROOT,env=env)
        for case in fixture_cases():
            selection={**env,'BLUEPRINT_ROOT':str(FIXTURES),'BLUEPRINT':case['name'],'BLUEPRINT_VERSION':case['version']}
            for action in ['blueprint:package','blueprint:push','blueprint:push']:
                run(['go','run','./cmd/kit-build',action],cwd=ROOT,env=selection)
        daemon_check(tmp,address,env)
        for v in [case['version'] for case in fixture_cases()]:
            local_flow(tmp,conf,v,f'{address}/inframill/blueprints/local-resource:{v}',env)
        # Attempt to publish newer content under the older tag, without touching
        # source releases or the already built immutable package directories.
        conflict=tmp/'conflict/blueprints/local-resource/0.1.0'
        shutil.copytree(tmp/'dist/blueprints/local-resource/0.2.0',conflict)
        index_path=conflict/'oci/index.json';index=json.loads(index_path.read_text())
        for descriptor in index['manifests']:
            descriptor.setdefault('annotations',{})['org.opencontainers.image.ref.name']='0.1.0'
        index_path.write_text(json.dumps(index))
        rejected=subprocess.run(['go','run','./cmd/kit-build','blueprint:push'],cwd=ROOT,env={**env,'KIT_DIST_DIR':str(tmp/'conflict'),'BLUEPRINT':'local-resource','BLUEPRINT_VERSION':'0.1.0'},text=True,capture_output=True)
        if rejected.returncode==0 or 'release tag already points to different content' not in rejected.stderr:raise RuntimeError('Conflicting release tag was not rejected: '+rejected.stderr)
        print('Conflicting release tag rejected',flush=True)
        # Independent empty cache verifies immutable digest acquisition.
        record=json.loads((tmp/'dist/blueprints/local-resource/0.1.0/build.json').read_text())
        config(conf,tmp/'fresh-data',tofu,ansible)
        local_flow(tmp,conf,'0.1.0',address+'/inframill/blueprints/local-resource@'+record['descriptor']['digest'],env)
        # New Instances must initialize from the verified cache without acquisition.
        # No plain-HTTP registry is authorized, so an attempted fetch would fail.
        offline={**env,'INFRAMILL_HTTP_REGISTRIES':'' ,'VERIFY_BLUEPRINT_REFERENCE':address+'/inframill/blueprints/local-resource@'+record['descriptor']['digest']}
        ids=driver_ids('local-resource','0.1.0')
        execute(tmp,conf,tmp/'local-0.1.0.json','test/offline',ids['ansible']+'.syntax-check',{'playbook':'site'},env=offline)
        cache=tmp/'fresh-data/blueprints'
        cached_schema=next(cache.rglob('schema.json'))
        cached_schema.write_bytes(cached_schema.read_bytes()+b'\n')
        before=set((tmp/'fresh-data/instances').iterdir())
        args_file=tmp/'tampered-args.json';args_file.write_text('{"playbook":"site"}')
        rejected=subprocess.run([str(ROOT/'bin/inframill'),'local','exec',ids['ansible']+'.syntax-check','--config',str(conf),'--values-file',str(tmp/'local-0.1.0.json'),'--name','test/tampered','--blueprint',offline['VERIFY_BLUEPRINT_REFERENCE'],'--args-file',str(args_file)],env=offline,text=True,capture_output=True)
        if rejected.returncode==0:raise RuntimeError('Corrupted cached source was accepted')
        if set((tmp/'fresh-data/instances').iterdir())!=before:raise RuntimeError('Corrupt source created an Instance')
        print('Offline cache reuse and consumer corruption rejection passed',flush=True)
        # Publish the existing bundle only to this disposable test registry.
        if not (bundle_output()/'build.json').is_file():raise RuntimeError('Run task bundle before check-full container verification')
        run(['go','run','./cmd/kit-build','bundle:push'],cwd=ROOT,env={**env,'KIT_DIST_DIR':str(bundle_output().parent.parent)})
        # Import the exact OCI bytes for execution: Docker Desktop's daemon has a
        # different loopback interface from the workstation's registry port.
        image='docker.io/library/inframill-kit-test:'+uuid.uuid4().hex
        layout=bundle_output()/'oci';index=json.loads((layout/'index.json').read_text())
        for manifest in index['manifests']:
            manifest.setdefault('annotations',{})['org.opencontainers.image.ref.name']=image.rsplit(':',1)[1]
            manifest['annotations']['io.containerd.image.name']=image
        archive=tmp/'runtime.tar'
        with tarfile.open(archive,'w') as tar:
            for path in sorted(layout.rglob('*')):
                if path.name=='index.json':continue
                tar.add(path,arcname=str(path.relative_to(layout)),recursive=False)
            data=json.dumps(index).encode();header=tarfile.TarInfo('index.json');header.size=len(data);tar.addfile(header,io.BytesIO(data))
        run(['docker','load','-i',archive])
        try:
            python='/opt/inframill/tools/ansible/'+bundle_lock()['ansible_core']+'/bin/python3'
            for arch in ['arm64','amd64']:
                image_check(tmp,image,arch,network)
                run(['docker','run','--rm','--pull=never','--platform','linux/'+arch,'--network',network,'-e','KIT_TEST_REGISTRY=registry:5000','-e','KIT_TEST_FIXTURES='+json.dumps([{**case,'drivers':driver_ids(case['name'],case['version'])} for case in fixture_cases()]),'--mount','type=bind,src='+str(ROOT/'scripts/runtime-smoke.py')+',dst=/verify.py,readonly','--mount','type=bind,src='+str(ROOT/'examples/pv-cloud-0.1.0.json')+',dst=/pv-example.json,readonly','--entrypoint',python,image,'/verify.py'])
        finally:subprocess.run(['docker','image','rm',image],stdout=subprocess.DEVNULL,check=False)


def main():
    action=sys.argv[1]
    if action=='registry':registry_check();return
    if action=='full':
        for case in fixture_cases()+json.loads((ROOT/'releases/sets/default.yaml').read_text())['blueprints']:blueprint_check(case['name'],case['version'])
        registry_check();return
    if action=='blueprint:check':blueprint_check(os.environ['BLUEPRINT'],os.environ.get('BLUEPRINT_VERSION','0.1.0'));return
    if action=='driver:check':
        name=os.environ['DRIVER'];version=os.environ.get('DRIVER_VERSION','0.1.0')
        run([ROOT/'bin/inframill-kit','driver',name,'--driver-version',version],input=json.dumps({'protocol':1,'method':'info','params':{}}));run(['go','test','-vet=off','./internal/drivers/'+name]);return
    if action=='blueprint:verify':
        reference=os.environ['REFERENCE']
        if '@sha256:' not in reference:raise RuntimeError('REFERENCE must be digest-pinned')
        tofu,ansible=tools()
        with tempfile.TemporaryDirectory(prefix='kit-verify-') as td:
            tmp=pathlib.Path(td);conf=tmp/'config.json';config(conf,tmp/'data',tofu,ansible)
            name=os.environ.get('BLUEPRINT','');version=os.environ.get('BLUEPRINT_VERSION','0.1.0');example=blueprint_root(name)/f'examples/{name}-{version}.json'
            data=json.loads(example.read_text());instance=tmp/'values.json';instance.write_text(json.dumps(data))
            # Blueprint and driver versions are independent release identities.
            release=json.loads(run(['go','run','./cmd/kit-build','blueprint:requirements'],cwd=ROOT,env={**os.environ,'BLUEPRINT_ROOT':str(blueprint_root(name))},capture_output=True).stdout)
            candidates=[d['id'] for d in release['drivers'] if d['name']=='ansible']
            if len(candidates)!=1:raise RuntimeError('Verification requires a fixture selecting one Ansible driver')
            execute(tmp,conf,instance,'verify/blueprint',candidates[0]+'.syntax-check',{'playbook':'site'},env={**os.environ,'VERIFY_BLUEPRINT_REFERENCE':reference})
        return
    if action=='bundle:verify':
        reference=os.environ['REFERENCE']
        if '@sha256:' not in reference:raise RuntimeError('REFERENCE must be digest-pinned')
        data=json.loads(run(['docker','buildx','imagetools','inspect','--raw',reference],capture_output=True).stdout)
        platforms={(m.get('platform',{}).get('os'),m.get('platform',{}).get('architecture')) for m in data.get('manifests',[])}
        if not {('linux','amd64'),('linux','arm64')}<=platforms:raise RuntimeError('Missing runtime architecture')
        run(['docker','run','--rm','--entrypoint','inframill-kit',reference,'version'])
        run(['docker','run','--rm',reference,'local','driver','list','--config','/etc/inframill/inframill.yaml','-o','json']);return
    raise RuntimeError('Unknown check')
if __name__=='__main__':main()
