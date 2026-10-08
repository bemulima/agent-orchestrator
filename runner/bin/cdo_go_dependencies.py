#!/usr/bin/env python3
"""Trusted canonical Go dependency preparation; never exposed as a model tool."""
import argparse,datetime,hashlib,json,os,pathlib,re,shutil,stat,subprocess,tempfile,uuid
class DependencyDenied(Exception):pass

def sha(data):return hashlib.sha256(data).hexdigest()
def inventory(root):
 result={};total=0
 for folder,dirs,files in os.walk(root,followlinks=False):
  for name in dirs+files:
   p=pathlib.Path(folder,name);s=p.lstat()
   if p.is_symlink() or (p.is_file() and s.st_nlink!=1) or not (p.is_file() or p.is_dir()):raise DependencyDenied('GO_DEPENDENCY_LINK_DENIED')
  for name in files:
   p=pathlib.Path(folder,name);total+=p.stat().st_size
   if total>512*1024*1024 or len(result)>=20000:raise DependencyDenied('GO_DEPENDENCY_INPUT_LIMIT')
   result[str(p.relative_to(root))]=sha(p.read_bytes())
 return dict(sorted(result.items()))
def validate(bundle,expected,source=None,image=None):
 bundle=pathlib.Path(bundle)
 if re.search(r'[,\x00-\x1f\x7f]',str(bundle)):raise DependencyDenied('GO_DEPENDENCY_ROOT_DENIED')
 if not bundle.is_absolute() or bundle.resolve(strict=True)!=bundle:raise DependencyDenied('GO_DEPENDENCY_ROOT_DENIED')
 manifest=bundle/'manifest.json'
 if manifest.is_symlink() or manifest.stat().st_nlink!=1 or sha(manifest.read_bytes())!=expected:raise DependencyDenied('GO_DEPENDENCY_MANIFEST_MISMATCH')
 v=json.loads(manifest.read_text())
 if (bundle/'modules').is_symlink() or not (bundle/'modules').is_dir():raise DependencyDenied('GO_DEPENDENCY_LINK_DENIED')
 if v.get('schema_version')!=1 or inventory(bundle/'modules')!=v['files']:raise DependencyDenied('GO_DEPENDENCY_CONTENT_MISMATCH')
 if sha(json.dumps(v['files'],sort_keys=True,separators=(',',':')).encode())!=v['cache_identity']:raise DependencyDenied('GO_DEPENDENCY_CACHE_MISMATCH')
 if source:
  source=pathlib.Path(source)
  for name in ['go.mod','go.sum']:
   p=source/name
   if p.is_symlink() or not p.is_file() or sha(p.read_bytes())!=v['inputs'][name]:raise DependencyDenied('GO_DEPENDENCY_REPREPARATION_REQUIRED')
 if image and image!=v['toolchain_image']:raise DependencyDenied('GO_DEPENDENCY_TOOLCHAIN_MISMATCH')
 return v

def prepare(source,out,image,mod_hash,sum_hash,execution):
 source=pathlib.Path(source).resolve(strict=True);out=pathlib.Path(out).absolute()
 if any(re.search(r'[,\x00-\x1f\x7f]',str(p)) for p in [source,out]):raise DependencyDenied('GO_DEPENDENCY_ROOT_DENIED')
 if out.exists():raise DependencyDenied('GO_DEPENDENCY_OUTPUT_EXISTS')
 if (source/'go.work').exists():raise DependencyDenied('GO_DEPENDENCY_WORKSPACE_UNSUPPORTED')
 inputs={}
 for name,expected in [('go.mod',mod_hash),('go.sum',sum_hash)]:
  p=source/name
  if p.is_symlink() or not p.is_file() or p.stat().st_nlink!=1 or p.stat().st_size>1048576 or sha(p.read_bytes())!=expected:raise DependencyDenied('GO_DEPENDENCY_APPROVED_INPUT_MISMATCH')
  inputs[name]=expected
 identity=subprocess.check_output(['docker','image','inspect',image,'--format','{{.Id}}'],text=True,timeout=15).strip()
 out.parent.mkdir(parents=True,exist_ok=True);stage=pathlib.Path(tempfile.mkdtemp(prefix='go-dependency-preparing-',dir=out.parent));name='cdo-go-preparer-'+uuid.uuid4().hex
 try:
  (stage/'inputs').mkdir();(stage/'modules').mkdir()
  for n in inputs:shutil.copyfile(source/n,stage/'inputs'/n)
  prefix=['docker','run','--rm','--name',name,'--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--pids-limit','128','--memory','1g','--memory-swap','1g','--cpus','2','--log-driver','none','--tmpfs','/tmp:rw,size=67108864','--mount','type=bind,src='+str(stage)+',dst=/prepared','--workdir','/prepared/inputs','--entrypoint','/usr/bin/env',identity,'-i','PATH=/usr/local/go/bin:/usr/bin:/bin','HOME=/tmp','GOMODCACHE=/prepared/modules','GOCACHE=/tmp/build','GOPATH=/tmp/go','GOENV=off','GOWORK=off','GOTOOLCHAIN=local','GOPROXY=https://proxy.golang.org','GOSUMDB=sum.golang.org','GONOSUMDB=','GONOPROXY=','GOPRIVATE=','GOVCS=*:off']
  def go(*args):
   r=subprocess.run(prefix+['go',*args],capture_output=True,text=True,timeout=180)
   if r.returncode:raise DependencyDenied('GO_DEPENDENCY_RESOLUTION_INCOMPLETE')
   return r.stdout
  parsed=json.loads(go('mod','edit','-json'))
  if parsed.get('Replace'):raise DependencyDenied('GO_DEPENDENCY_REPLACE_UNSUPPORTED')
  version=go('version').strip();go('mod','download','all');go('mod','verify')
  for n in inputs:
   if sha((stage/'inputs'/n).read_bytes())!=inputs[n]:raise DependencyDenied('GO_DEPENDENCY_SUM_INCOMPLETE')
  raw=go('list','-m','-json','all');decoder=json.JSONDecoder();modules=[]
  while raw.strip():
   value,index=decoder.raw_decode(raw.lstrip());raw=raw.lstrip()[index:]
   if not value.get('Main'):
    if not value.get('Sum') or not value.get('GoModSum'):raise DependencyDenied('GO_DEPENDENCY_CHECKSUM_ABSENT')
    modules.append({k:value[k] for k in ['Path','Version','Sum','GoModSum']})
  files=inventory(stage/'modules');v={'schema_version':1,'toolchain':version,'toolchain_image':identity,'inputs':inputs,'modules':sorted(modules,key=lambda x:x['Path']),'files':files,'cache_identity':sha(json.dumps(files,sort_keys=True,separators=(',',':')).encode()),'prepared_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'repository':str(source),'execution_id':execution}
  (stage/'manifest.json').write_text(json.dumps(v,sort_keys=True,indent=2)+'\n')
  for folder,dirs,fs in os.walk(stage):
   for n in fs:pathlib.Path(folder,n).chmod(0o444)
  os.rename(stage,out)
  for folder,dirs,fs in os.walk(out):pathlib.Path(folder).chmod(0o555)
  expected=sha((out/'manifest.json').read_bytes());validate(out,expected,source,identity)
  return {'bundle':str(out),'manifest_sha256':expected,'toolchain_image':identity}
 except BaseException:
  for folder,dirs,fs in os.walk(stage):
   pathlib.Path(folder).chmod(0o700)
   for n in fs:pathlib.Path(folder,n).chmod(0o600)
  shutil.rmtree(stage,ignore_errors=True);raise
 finally:subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)

if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--source',required=True);p.add_argument('--output',required=True);p.add_argument('--image',required=True);p.add_argument('--go-mod-sha256',required=True);p.add_argument('--go-sum-sha256',required=True);p.add_argument('--execution-id',required=True);a=p.parse_args()
 print(json.dumps(prepare(a.source,a.output,a.image,a.go_mod_sha256,a.go_sum_sha256,a.execution_id)))
