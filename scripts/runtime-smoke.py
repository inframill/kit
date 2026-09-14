#!/usr/bin/env python3
"""Executed inside the built runtime against disposable built-in OpenTofu resources."""
import json,os,pathlib,platform,subprocess,tempfile,uuid
registry=os.environ['KIT_TEST_REGISTRY'];os.environ['INFRAMILL_HTTP_REGISTRIES']=registry
arch='arm64' if platform.machine() in ('arm64','aarch64') else 'amd64'
with tempfile.TemporaryDirectory(prefix='kit-runtime-') as td:
    tmp=pathlib.Path(td);conf=json.loads(pathlib.Path('/etc/inframill/config.json').read_text());conf['engine']['dataDir']=str(tmp/'data');config=tmp/'config.json';config.write_text(json.dumps(conf))
    for version in ['0.1.0','0.2.0']:
        fixture_name='smoke/v'+version.replace('.','-')
        instance=tmp/f'instance-{version}.json';instance.write_text(json.dumps({'blueprint':f'{registry}/inframill/blueprints/local-resource:{version}','values':{'architecture':arch}}))
        def execute(command,args=None):
            token=uuid.uuid4().hex;result=tmp/(token+'.json');cmd=['inframill','exec',command,'--config',str(config),'--instance',str(instance),'--name',fixture_name,'--result-file',str(result)]
            if args is not None:
                argfile=tmp/(token+'-args.json');argfile.write_text(json.dumps(args));cmd+=['--args-file',str(argfile)]
            subprocess.run(cmd,check=True,stdout=subprocess.DEVNULL)
            return json.loads(result.read_text())['result']
        prefix='opentofu-'+version.replace('.','-');ap='ansible-'+version.replace('.','-')
        execute(prefix+'.init');plan=execute(prefix+'.plan');execute(prefix+'.apply',{'plan_id':plan['plan_id']});execute(ap+'.apply',{'playbook':'site'});plan=execute(prefix+'.plan',{'destroy':True});execute(prefix+'.destroy',{'plan_id':plan['plan_id']})
    version='0.1.0';fixture_name='smoke/pv-cloud';instance=tmp/'pv.json'
    data=json.loads(pathlib.Path('/pv-example.json').read_text());data['blueprint']=registry+'/inframill/blueprints/pv-cloud:0.1.0';instance.write_text(json.dumps(data))
    execute('ansible-0-1-0.syntax-check',{'playbook':'site'})
    work=next(p.parent for p in (tmp/'data').glob('instances/*/workspace/opentofu-0-1-0/main.tf') if 'hcloud' in p.read_text())
    tofu='/opt/inframill/tools/opentofu/1.11.5/tofu'
    for args in [['init','-backend=false','-input=false','-lockfile=readonly','-no-color'],['validate','-no-color'],['test','-no-color']]:
        subprocess.run([tofu]+args,cwd=work,env={**os.environ,'HCLOUD_TOKEN':'mock-only'},check=True)
print('Runtime workflow and provider mocks passed on '+arch)
