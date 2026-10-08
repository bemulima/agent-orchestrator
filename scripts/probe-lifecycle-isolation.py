#!/usr/bin/env python3
"""Known outside process in the same Docker VM, observed from trusted host."""
import hashlib,json,pathlib,subprocess,sys,tempfile,uuid
repo=pathlib.Path(__file__).resolve().parents[1];sys.path.insert(0,str(repo/'runner/bin'))
from cdo_broker import Broker,Policy,container_absent
out=pathlib.Path(sys.argv[1]).resolve();out.parent.mkdir(parents=True,exist_ok=True)
base=repo.parents[1]/'.cdo-sandbox-certification-20261006'
fixture=pathlib.Path(tempfile.mkdtemp(prefix='pid-isolation-',dir=base));(fixture/'a.go').write_text('package sample\n')
state=pathlib.Path(tempfile.mkdtemp(prefix='cdo-lifecycle-isolation-'));name='cdo-lifecycle-sentinel-'+uuid.uuid4().hex[:12]
image='cdo-sandbox-command:20261004-v1';broker=None
try:
 subprocess.run(['docker','run','-d','--name',name,'--network','none','--cap-drop','ALL','--security-opt','no-new-privileges=true','--entrypoint','sleep',image,'120'],check=True,capture_output=True)
 sentinel=json.loads(subprocess.check_output(['docker','inspect',name]))[0];pid=sentinel['State']['Pid'];assert pid>0
 policy=Policy({'root':str(fixture),'role':'reviewer','phase':'isolation','write_paths':[],'execution_id':str(uuid.uuid4()),'image':image,'state_dir':str(state),'evidence':str(out)})
 broker=Broker(policy);broker.launch()
 code='import os,signal,json;pid='+str(pid)+'\ntry:os.kill(pid,signal.SIGTERM);print("SIGNAL_ESCAPED");raise SystemExit(9)\nexcept ProcessLookupError:print(json.dumps({"outside_pid":pid,"signal":"BLOCKED_PID_NAMESPACE"}))'
 result=broker.call('command',{'argv':['python3','-c',code]})
 alive=json.loads(subprocess.check_output(['docker','inspect',name]))[0]['State']['Running']
 inside=json.loads(subprocess.check_output(['docker','inspect',broker.name]))[0]
 evidence={'outside_container':name,'outside_vm_pid':pid,'sandbox_container':broker.name,'pid_mode':inside['HostConfig']['PidMode'],'cap_drop':inside['HostConfig']['CapDrop'],'signal_attempt':result,'outside_still_running':alive}
 broker.close();evidence['sandbox_removed']=container_absent(broker.name)
 evidence['pass']=result['exit_code']==0 and 'BLOCKED_PID_NAMESPACE' in result['output'] and alive and inside['HostConfig']['PidMode']=='' and inside['HostConfig']['CapDrop']==['ALL'] and evidence['sandbox_removed']
 out.write_text(json.dumps(evidence,indent=2));print(json.dumps(evidence));assert evidence['pass']
finally:
 if broker and not container_absent(broker.name):broker.close()
 subprocess.run(['docker','rm','-f',name],capture_output=True,timeout=15)
 import shutil;shutil.rmtree(state)
