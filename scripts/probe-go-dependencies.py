#!/usr/bin/env python3
"""Owner-authorized disposable external-module fixture and networkless broker."""
import argparse,pathlib,sys,tempfile,subprocess,json,hashlib,os,shutil
repo=pathlib.Path(__file__).resolve().parents[1];sys.path.insert(0,str(repo/'runner/bin'))
from cdo_go_dependencies import prepare,validate,DependencyDenied
from cdo_broker import Policy,Broker,Publisher,Denied,LimitExceeded,container_absent
p=argparse.ArgumentParser();p.add_argument('--image',required=True);p.add_argument('--output',required=True);a=p.parse_args()
base=repo.parents[1]/'.cdo-sandbox-certification-20261006';base.mkdir(exist_ok=True)
fixture=pathlib.Path(tempfile.mkdtemp(prefix='go-deps-fixture-',dir=base)).resolve()
(fixture/'go.mod').write_text('module example.test/availability\n\ngo 1.25.0\n\nrequire github.com/google/uuid v1.6.0\n')
(fixture/'value.go').write_text('package availability\nimport "github.com/google/uuid"\nfunc Valid() bool{return uuid.MustParse("11111111-1111-4111-8111-111111111111").Version()==4}\n')
(fixture/'value_test.go').write_text('package availability\nimport "testing"\nfunc TestExternal(t *testing.T){if !Valid(){t.Fatal("external UUID behavior")}}\n')
# Trusted fixture bootstrap creates its approved checksum input, before model admission.
bootstrap=['docker','run','--rm','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--tmpfs','/tmp:rw,size=67108864','--mount','type=bind,src='+str(fixture)+',dst=/fixture','--workdir','/fixture','--entrypoint','/usr/bin/env',a.image,'-i','PATH=/usr/local/go/bin:/usr/bin:/bin','HOME=/tmp','GOMODCACHE=/tmp/mod','GOCACHE=/tmp/build','GOPROXY=https://proxy.golang.org','GOSUMDB=sum.golang.org','GOTOOLCHAIN=local','GOENV=off','GOWORK=off','GOVCS=*:off','go','mod','download','all']
subprocess.run(bootstrap,check=True,timeout=180)
cache=repo/'.cache/go-dependencies'/fixture.name
prepared=prepare(fixture,cache,a.image,hashlib.sha256((fixture/'go.mod').read_bytes()).hexdigest(),hashlib.sha256((fixture/'go.sum').read_bytes()).hexdigest(),fixture.name)
results={'fixture':str(fixture),'prepared':prepared,'manifest':json.loads((cache/'manifest.json').read_text()),'roles':{},'cleanup':{}}
def make(role,scope,bundle=cache,manifest=prepared['manifest_sha256']):
 state=pathlib.Path(tempfile.mkdtemp(prefix='go-dep-broker-',dir=base));evidence=base/(state.name+'.json')
 return Broker(Policy({'root':str(fixture),'role':role,'phase':'go-dependencies','write_paths':scope,'execution_id':state.name,'image':prepared['toolchain_image'],'state_dir':str(state),'evidence':str(evidence),'go_dependency_bundle':str(bundle),'go_dependency_manifest_sha256':manifest}))
for role in ['contract','layer','composition','reviewer']:
 scope={'contract':['contracts/api.go'],'layer':['value.go'],'composition':['cmd/main.go'],'reviewer':[]}[role]
 b=make(role,scope)
 try:
  b.launch();r=b.call('command',{'argv':['go','test','./...']});assert r['exit_code']==0,r
  results['roles'][role]=r['exit_code'];info=json.loads(subprocess.check_output(['docker','inspect',b.name]))[0]
  assert info['HostConfig']['NetworkMode']=='none';assert next(m for m in info['Mounts'] if m['Destination']=='/dependency')['RW']==False
  if role=='layer':
   network=b.call('command',{'argv':['python3','-c',"import socket,json,os; r={};\nfor host,port in [('1.1.1.1',443),('host.docker.internal',80),('127.0.0.1',80)]:\n try: socket.create_connection((host,port),.5);r[host]=False\n except OSError:r[host]=True\nr['socket_absent']=not os.path.exists('/var/run/docker.sock');print(json.dumps(r))"]});assert all(json.loads(network['output']).values());results['network']=json.loads(network['output'])
   readonly=b.call('command',{'argv':['python3','-c',"import pathlib,json; r={};\nfor p in ['/dependency/manifest.json','/dependency/modules/escape','/tmp/view/go.mod','/tmp/view/go.sum','/tmp/view/module-cache','/dependency/../escape']:\n try:pathlib.Path(p).write_text('tamper');r[p]=False\n except OSError:r[p]=True\nprint(json.dumps(r))"]});assert all(json.loads(readonly['output']).values());results['readonly']=json.loads(readonly['output'])
   denied=[]
   for argv in [['env','GOMODCACHE=/tmp/view','go','test','./...'],['go','test','-modfile=/tmp/evil.mod','./...'],['env','GOPROXY=http://127.0.0.1:8080','go','test','./...']]:
    try:b.call('command',{'argv':argv});raise AssertionError('override accepted')
    except Denied as e:denied.append(str(e))
   results['overrides_denied']=denied
   env=b.call('command' ,{'argv':['go','env','GOPROXY','GOSUMDB','GONOSUMDB','GOMODCACHE','GOWORK','GOTOOLCHAIN']});results['go_env']=env['output'];assert env['output'].splitlines()==['off','off','','/dependency/modules','off','local']
   try:b.call('propose_file',{'path':'go.mod','content':'module evil'});raise AssertionError('declaration scope accepted')
   except Denied:results['declaration_scope_denied']=True
   failed=b.call('command',{'argv':['go','test','./missing-package']});assert failed['exit_code']!=0;results['test_failure']=failed['exit_code']
 finally:
  b.close();results['cleanup'][role]=container_absent(b.name);shutil.rmtree(b.p.state_dir)
# Host corruption discovered during execution poisons the existing attempt.
corrupt=pathlib.Path(tempfile.mkdtemp(prefix='corrupt-go-',dir=base));shutil.copytree(cache,corrupt/'bundle')
b=make('layer',['value.go'],(corrupt/'bundle').resolve())
try:
 b.launch();target=next(q for q in (corrupt/'bundle/modules').rglob('uuid.go') if q.is_file());target.chmod(0o644);target.write_text('corrupt')
 try:b.call('command',{'argv':['go','test','./...']});raise AssertionError('tamper admitted')
 except LimitExceeded as e:results['tamper']=str(e)
 results['tamper_journal']=json.loads(b.p.publication_journal.read_text())['status'];assert results['tamper_journal']=='FAILED'
 results['corrupt_bundle']=str((corrupt/'bundle').resolve())
finally:b.close();results['cleanup']['integrity']=container_absent(b.name);shutil.rmtree(b.p.state_dir)
# Missing input rejected by manifest, before worker admission.
missing=pathlib.Path(tempfile.mkdtemp(prefix='missing-go-',dir=base));shutil.copytree(cache,missing/'bundle');(missing/'bundle').chmod(0o755)
modules=missing/'bundle/modules';target=next(p for p in modules.rglob('uuid.go') if p.is_file());target.parent.chmod(0o755);target.unlink()
try:validate((missing/'bundle').resolve(),prepared['manifest_sha256'],fixture,prepared['toolchain_image']);raise AssertionError('missing accepted')
except DependencyDenied as e:results['missing']=str(e)
# Coherent incomplete cache still cannot make Go use network (trusted diagnostic only).
v=json.loads((missing/'bundle/manifest.json').read_text());v['files']={}
for folder,dirs,files in os.walk(modules):
 for n in files:
  q=pathlib.Path(folder,n);v['files'][str(q.relative_to(modules))]=hashlib.sha256(q.read_bytes()).hexdigest()
v['cache_identity']=hashlib.sha256(json.dumps(v['files'],sort_keys=True,separators=(',',':')).encode()).hexdigest();mf=missing/'bundle/manifest.json';mf.chmod(0o644);mf.write_text(json.dumps(v));newhash=hashlib.sha256(mf.read_bytes()).hexdigest()
# Delete all prepared input and create an intentionally empty manifest.
for folder,dirs,files in os.walk(modules):
 pathlib.Path(folder).chmod(0o755)
shutil.rmtree(modules);modules.mkdir();v['files']={};v['cache_identity']=hashlib.sha256(b'{}').hexdigest();mf.write_text(json.dumps(v));newhash=hashlib.sha256(mf.read_bytes()).hexdigest()
b=make('reviewer',[],(missing/'bundle').resolve(),newhash)
try:
 b.launch();r=b.call('command',{'argv':['go','test','./...']});assert r['exit_code']!=0 and ('GOPROXY=off' in r['output'] or 'module lookup disabled' in r['output'] or 'read-only file system' in r['output']),r;results['offline_missing']=r
finally:b.close();results['cleanup']['missing']=container_absent(b.name);shutil.rmtree(b.p.state_dir)
# Authorized declaration mutation stops for new trusted preparation.
b=make('layer',['go.mod'])
try:
 b.launch()
 try:b.call('propose_file',{'path':'go.mod','content':'module different'});raise AssertionError('no reprepare')
 except LimitExceeded as e:results['approved_declaration_change']=str(e)
 assert b.p.publication_journal.exists();results['failed_journal']=json.loads(b.p.publication_journal.read_text())['status']
finally:b.close();results['cleanup']['declaration']=container_absent(b.name);shutil.rmtree(b.p.state_dir)
pathlib.Path(a.output).write_text(json.dumps(results,indent=2));print(json.dumps({'roles':results['roles'],'network':results['network'],'missing':results['missing'],'cleanup':results['cleanup'],'output':a.output}))
