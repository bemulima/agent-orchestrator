#!/usr/bin/env python3
"""Kernel quota / fallback / isolation probes using the exact SDK OCI profile."""
import argparse,json,pathlib,subprocess,sys,tempfile,uuid
repo=pathlib.Path(__file__).resolve().parents[1];sys.path.insert(0,str(repo/'runner/bin'))
from cdo_sdk_state import oci_command,ROOT,MAX_BYTES,MAX_FILES
from cdo_broker import regular,Denied
p=argparse.ArgumentParser();p.add_argument('--image',required=True);p.add_argument('--output',required=True);a=p.parse_args()
results={};containers=[]
def run(code):
 name='cdo-sdk-state-'+uuid.uuid4().hex;containers.append(name)
 command=oci_command(name,a.image,[]);command+=['--entrypoint','python3',a.image,'-c',code]
 r=subprocess.run(command,capture_output=True,text=True,timeout=30)
 if r.returncode:raise RuntimeError(r.stderr)
 return json.loads(r.stdout)
try:
 results['bytes']=run("""import os,json,errno
root='/execution/sdk-private';n=0
try:
 with open(root+'/fill','wb',buffering=0) as f:
  while True:n+=f.write(b'x'*4096)
except OSError as e:
 s=os.statvfs(root);print(json.dumps({'errno':e.errno,'bytes':n,'used':(s.f_blocks-s.f_bfree)*s.f_frsize,'typed':'SDK_PRIVATE_STATE_BYTES_EXCEEDED' if e.errno==errno.ENOSPC else 'FAIL'}))
""")
 results['files']=run("""import os,json,errno
root='/execution/sdk-private';n=0
try:
 while True:
  open(root+'/'+str(n),'xb').close();n+=1
except OSError as e:
 s=os.statvfs(root);print(json.dumps({'errno':e.errno,'created':n,'used_inodes':s.f_files-s.f_ffree,'typed':'SDK_PRIVATE_STATE_FILES_EXCEEDED' if e.errno==errno.ENOSPC else 'FAIL'}))
""")
 name='cdo-sdk-state-'+uuid.uuid4().hex;containers.append(name)
 command=oci_command(name,a.image,[]);command=[v.replace(ROOT+':rw,',ROOT+':ro,') for v in command]
 command+=['--entrypoint','python3',a.image,'/reference/supervisor.py','--supervise','exec','--experimental-json','--skip-git-repo-check','--cd',ROOT+'/control','test']
 unavailable=subprocess.run(command,capture_output=True,text=True,timeout=30)
 results['unavailable']={'typed':unavailable.returncode!=0 and 'SDK_PRIVATE_STATE_UNAVAILABLE' in unavailable.stderr}
 results['fallback']=run("""import pathlib,json,os
r={}
for p in ['/tmp/escape','/dev/shm/escape','/dev/escape','/app/escape','/root/escape','/home/escape','/execution/sdk-private/../../escape','/var/tmp/escape','/workspace/escape','/var/run/docker.sock','/root/.codex/auth.json','/root/.ssh/id_rsa']:
 try:pathlib.Path(p).write_text('escape');r[p]='POSSIBLE'
 except OSError:r[p]='BLOCKED'
s=pathlib.Path('/execution/sdk-private/source-link');s.symlink_to('/workspace/source')
try:s.write_text('escape');r['state_symlink_to_source']='POSSIBLE'
except OSError:r['state_symlink_to_source']='BLOCKED'
try:os.link('/workspace/source','/execution/sdk-private/hardlink');r['hardlink_source']='POSSIBLE'
except OSError:r['hardlink_source']='BLOCKED'
print(json.dumps(r))
""")
 # Keep first namespace alive while the second probes exactly the same pathname.
 name='cdo-sdk-state-'+uuid.uuid4().hex;containers.append(name)
 command=oci_command(name,a.image,[]);command.remove('-i');command+=['--entrypoint','python3',a.image,'-c',"import pathlib,time;pathlib.Path('/execution/sdk-private/sibling-secret').write_text('private');print('ready',flush=True);time.sleep(30)"]
 first=subprocess.Popen(command,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
 if first.stdout.readline().strip()!='ready':raise RuntimeError('sibling start failed')
 results['sibling']=run("""import pathlib,json
p=pathlib.Path('/execution/sdk-private/sibling-secret');before=p.exists();p.write_text('second');print(json.dumps({'first_inaccessible':not before}))
""")
 original=subprocess.check_output(['docker','exec',name,'cat',ROOT+'/sibling-secret'],text=True)
 results['sibling']['first_unchanged']=original=='private'
 with tempfile.TemporaryDirectory() as d:
  source=pathlib.Path(d);(source/'link').symlink_to(ROOT+'/sibling-secret')
  try:regular(source,'link');results['source_symlink_to_state']='POSSIBLE'
  except Denied:results['source_symlink_to_state']='BLOCKED'
 results['assertions']={'unavailable_typed':results['unavailable']['typed'],'bytes_hard':results['bytes']['typed']=='SDK_PRIVATE_STATE_BYTES_EXCEEDED' and results['bytes']['used']<=MAX_BYTES,'files_hard':results['files']['typed']=='SDK_PRIVATE_STATE_FILES_EXCEEDED' and results['files']['used_inodes']<=MAX_FILES,'fallback_blocked':all(v=='BLOCKED' for v in results['fallback'].values()),'source_symlink_blocked':results['source_symlink_to_state']=='BLOCKED','siblings_isolated':all(results['sibling'].values())}
finally:
 for name in containers:subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)
 results['cleanup']=all(subprocess.run(['docker','inspect',n],capture_output=True).returncode!=0 for n in containers)
pathlib.Path(a.output).write_text(json.dumps(results,indent=2));print(json.dumps(results));sys.exit(0 if all(results['assertions'].values()) and results['cleanup'] else 1)
