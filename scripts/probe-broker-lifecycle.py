#!/usr/bin/env python3
"""Actual OCI lifecycle/resource tests of the same Broker.call implementation."""
import argparse,pathlib,tempfile,json,sys,subprocess,os,time
sys.path.insert(0,str(pathlib.Path(__file__).resolve().parents[1]/'runner/bin'))
from cdo_broker import Policy,Broker,Denied,LimitExceeded
ap=argparse.ArgumentParser();ap.add_argument('--image',required=True);ap.add_argument('--output',required=True);a=ap.parse_args();results={};evidence=[]
with tempfile.TemporaryDirectory() as t:
 base=pathlib.Path(t).resolve();root=base/'fixture';root.mkdir();root.chmod(0o755);(root/'internal').mkdir();(root/'internal/a.go').write_text('package sample\n')
 def instance():
  state=base/('state-'+str(len(evidence)));state.mkdir();p=Policy({'root':str(root),'role':'layer','phase':'implementation','write_paths':['internal/a.go'],'execution_id':'lifecycle','image':a.image,'state_dir':str(state),'evidence':str(base/'audit')});b=Broker(p);b.launch();return b
 def removed(b):return subprocess.run(['docker','inspect',b.name],capture_output=True).returncode!=0
 for name,argv in [('normal',['python3','-c','print("ok")']),('command_failure',['python3','-c','raise SystemExit(7)']),('session_escape',['python3','-c','import os,subprocess; p=subprocess.Popen(["python3","-c","import os,signal,time;os.setsid();signal.signal(signal.SIGTERM,signal.SIG_IGN);time.sleep(120)"],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL);print(p.pid)'])]:
  b=instance()
  try:r=b.call('command',{'argv':argv});evidence.append({'case':name,'result':r,'container':b.name,'broker_events':list(b.events)});b.close();results[name+'_cleanup']=removed(b)
  finally:
   if not removed(b):b.close()
 for name,settings,tool,arg in [('pid_exhaustion',{},'command',{'argv':['python3','-c','import subprocess,time; children=[]\ntry:\n while True:children.append(subprocess.Popen(["sleep","120"]))\nexcept OSError:print("PID exhaustion reached")']}),('oversized_scratch',{},'command',{'argv':['python3','-c','open("/tmp/oversize","wb").write(b"x"*17000000)']}),('timeout',{'command_seconds':1},'command',{'argv':['python3','-c','import time;time.sleep(120)']}),('output',{'output_bytes':1024},'command',{'argv':['python3','-c','print("x"*100000)']}),('command_count',{'commands':0},'command',{'argv':['true']}),('oversized_proposal',{},'propose_file',{'path':'internal/a.go','content':'x'*262145}),('aggregate_staging',{'aggregate_bytes':10},'propose_file',{'path':'internal/a.go','content':'x'*11})]:
  b=instance()
  for k,v in settings.items():setattr(b.p,k,v)
  try:
   try:b.call(tool,arg);results[name+'_typed_failure']=False
   except LimitExceeded as ex:results[name+'_typed_failure']=True;evidence.append({'case':name,'failure':str(ex),'container':b.name})
   results[name+'_cleanup']=removed(b)
  finally:
   if not removed(b):b.close()
 # Known host process: use host PID + sentinel file. PID namespace access must
 # not signal it even if a coincident namespace PID exists.
 known=subprocess.Popen(['sleep','120'])
 try:
  b=instance();r=b.call('command',{'argv':['python3','-c',f'import os,errno; p={known.pid};\ntry:os.kill(p,15);print("unexpected signal")\nexcept OSError as e:print(e.errno)']});results['known_host_process_survives']=known.poll() is None;results['known_process_signal_denied']=r['output'].strip() in ('3','1');b.close();evidence.append({'case':'known_process','host_pid':known.pid,'result':r})
 finally:known.terminate();known.wait()
 # Uncertified items are explicit; never infer cumulative fork accounting from
 # a simultaneous PID ceiling or absence of a random PID.
 report={'results':results,'evidence':evidence,'uncertified':['Temporal activity cleanup','aggregate scratch churn accounting','result/session storage quota']}
 pathlib.Path(a.output).write_text(json.dumps(report,indent=2));print(json.dumps(results));raise SystemExit(0 if all(results.values()) else 1)
