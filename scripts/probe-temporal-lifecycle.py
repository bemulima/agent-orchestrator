#!/usr/bin/env python3
"""Real Temporal/production workflow+activity/SDK; deterministic provider, no API calls."""
import argparse,hashlib,http.server,json,os,pathlib,signal,subprocess,tempfile,threading,time,sys
ap=argparse.ArgumentParser();ap.add_argument('--output',required=True);ap.add_argument('--case',default='execution-layer');a=ap.parse_args()
repo=pathlib.Path(__file__).resolve().parents[1];out=pathlib.Path(a.output).resolve();out.mkdir(parents=True,exist_ok=True)
if a.case=='all':
 cases=['execution-contract','execution-layer','execution-composition','execution-reviewer','workflow-layer','before-publication-layer','verification-layer','publication-validation-layer','publication-applied-1-layer','timeout-layer','runner-interruption-layer','broker-interruption-layer','transport-break-layer','quota-layer','unknown-layer','success-layer']
 results={}
 for case in cases:
  r=subprocess.run([sys.executable,__file__,'--output',str(out/case),'--case',case],timeout=180)
  results[case]={'exit_code':r.returncode}
  (out/'suite.json').write_text(json.dumps(results,indent=2))
 sys.exit(0 if all(r['exit_code']==0 for r in results.values()) else 1)
base=repo.parents[1]/'.cdo-sandbox-certification-20261006';base.mkdir(exist_ok=True)
fixture=pathlib.Path(tempfile.mkdtemp(prefix='temporal-',dir=base)).resolve();(fixture/'a.go').write_text('package sample\n');(fixture/'b.go').write_text('package sample\n')
role=a.case.rsplit('-',1)[-1];phase=a.case.rsplit('-',1)[0]
owned=['cmd/main.go','cmd/other.go'] if role=='composition' else ['a.go','b.go']
if role=='composition':
 (fixture/'cmd').mkdir()
 for n in owned:(fixture/n).write_text('package main\n')
bundle=repo/'.cache/go-dependencies/go-deps-fixture-pcs74yoa';manifest=json.loads((bundle/'manifest.json').read_text());source=pathlib.Path(manifest['repository'])
for n in ['go.mod','go.sum']:(fixture/n).write_bytes((source/n).read_bytes())
active=threading.Event();index=0;requests=[]
code="import pathlib,subprocess,os,signal,time,json;root=pathlib.Path('/tmp/tree');root.mkdir();pathlib.Path('/tmp/go-build').mkdir(exist_ok=True);pathlib.Path('/tmp/go-build/marker').write_text('private');children=[]\nfor kind in ['normal','grandchild','background','setsid','ignoring']:\n script=\"import os,signal,time,pathlib;\"+(\"os.setsid();\" if kind=='setsid' else '')+(\"signal.signal(signal.SIGTERM,signal.SIG_IGN);\" if kind=='ignoring' else '')+(\"import subprocess,pathlib;g=subprocess.Popen(['sleep','120']);pathlib.Path('/tmp/tree/grandchild.pid').write_text(str(g.pid));\" if kind=='grandchild' else '')+\"p=pathlib.Path('/tmp/tree/\"+kind+\"');\\nwhile True: p.write_text(str(time.time()));time.sleep(.05)\";p=subprocess.Popen(['python3','-c',script]);children.append({'kind':kind,'pid':p.pid})\npathlib.Path('/tmp/tree/identities.json').write_text(json.dumps(children));time.sleep(120)"
calls=[('tool_search',{'query':'cdo read_file propose_file command','limit':10})]
if role!='reviewer':
 for n in owned:calls.append(('mcp__cdo__propose_file',{'path':n,'content':'package '+('main' if role=='composition' else 'sample')+'\n// proposed\n'}))
if phase in ['execution','timeout','command-timeout']:calls.append(('mcp__cdo__command',{'argv':['python3','-c',code]}))
elif phase=='quota':calls.append(('update_plan',{'explanation':'x'*600000,'plan':[{'step':'quota stress','status':'in_progress'}]}));calls+=calls[-1:]*19
else:calls.append(('mcp__cdo__command',{'argv':['go','test','./...']}))
class API(http.server.BaseHTTPRequestHandler):
 def log_message(self,*_):pass
 def do_GET(self):self.send_response(200);self.end_headers();self.wfile.write(b'{"data":[]}')
 def do_POST(self):
  global index
  v=json.loads(self.rfile.read(int(self.headers['Content-Length'])));requests.append(v);active.set()
  if phase in ['workflow','worker-interruption','runner-interruption','broker-interruption','transport-break','unknown']:
   self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
   try:self.wfile.write(('data: '+json.dumps({'type':'response.created','response':{'id':'hold','status':'in_progress','output':[]}})+'\n\n').encode());self.wfile.flush();time.sleep(90)
   except (BrokenPipeError,ConnectionResetError):pass
   return
  if index<len(calls):
   name,args=calls[index]
   if name=='tool_search':item={'type':'tool_search_call','execution':'client','arguments':args,'call_id':'call_'+str(index),'id':'ts_'+str(index),'status':'completed'}
   else:item={'type':'function_call',**({'namespace':'mcp__cdo'} if name.startswith('mcp__cdo__') else {}),'name':name.replace('mcp__cdo__',''),'arguments':json.dumps(args),'call_id':'call_'+str(index),'id':'fc_'+str(index),'status':'completed'}
  else:item={'type':'message','role':'assistant','id':'final','status':'completed','content':[{'type':'output_text','text':'{"status":"completed"}'}]}
  index+=1;self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
  response={'id':'resp_'+str(index),'object':'response','status':'completed','output':[item],'usage':{'input_tokens':1,'output_tokens':1,'total_tokens':2}}
  for event in [{'type':'response.created','response':{**response,'status':'in_progress','output':[]}},{'type':'response.output_item.added','output_index':0,'item':item},{'type':'response.output_item.done','output_index':0,'item':item},{'type':'response.completed','response':response}]:
   try:self.wfile.write(('data: '+json.dumps(event)+'\n\n').encode());self.wfile.flush()
   except (BrokenPipeError,ConnectionResetError):break
server=http.server.ThreadingHTTPServer(('0.0.0.0',0),API);server.daemon_threads=True;threading.Thread(target=server.serve_forever,daemon=True).start()
request={'role':'reviewer' if role=='reviewer' else 'coder','working_directory':str(fixture),'model':'gpt-5.4','prompt':'Complete controlled lifecycle fixture.','output_schema':{'type':'object','properties':{'status':{'type':'string'}},'required':['status'],'additionalProperties':False},'sandbox_scope':{'profile':role,'write_paths':[] if role=='reviewer' else owned}}
(out/'request.json').write_text(json.dumps(request));(out/'gate').mkdir(exist_ok=True)
env={**os.environ,'CDO_LIFECYCLE_GATE':str(out/'gate'),'CDO_LIFECYCLE_PHASE':'publication-applied-1' if phase=='runner-publication-interruption' else phase,'CDO_LIFECYCLE_OUTPUT':str(out),'CDO_RUNNER_SCRIPT':str(repo/'runner/dist/index.js'),'CDO_CANCEL_KIND':('workflow' if phase=='workflow' else 'timeout' if phase=='timeout' else 'signal'),'CDO_SANDBOX_EXECUTION_MODE':'hardened-certification','CDO_CERTIFICATION_ROOT':str(fixture),'CDO_CERTIFICATION_EVIDENCE':str(out/'runner-audit.json'),'CDO_BROKER_IMAGE':manifest['toolchain_image'],'CDO_SDK_STATE_IMAGE':'cdo-sandbox-hardening:20261004-v3','CDO_GO_DEPENDENCY_BUNDLE':str(bundle),'CDO_GO_DEPENDENCY_MANIFEST_SHA256':hashlib.sha256((bundle/'manifest.json').read_bytes()).hexdigest(),'CDO_CERTIFICATION_PROVIDER_URL':'http://127.0.0.1:'+str(server.server_port)+'/v1','CODEX_SOL_MAX_RUNS_5H':'20','CODEX_BUDGET_MODE':'enforce'}
def docker(*args):return subprocess.run(['docker',*args],capture_output=True,text=True,timeout=20)
def absent(name):
 r=docker('inspect',name)
 if r.returncode==0:return False
 if 'no such object:' in r.stderr.lower() or 'no such container:' in r.stderr.lower():return True
 raise RuntimeError('DOCKER_CONTAINER_STATE_UNKNOWN: '+r.stderr)
def wait_until(fn,seconds=40):
 end=time.monotonic()+seconds
 while time.monotonic()<end:
  if fn():return
  if p.poll() is not None:raise RuntimeError('driver exited: '+(out/'driver.log').read_text()[-1500:])
  time.sleep(.1)
 raise RuntimeError('checkpoint timeout '+phase)
# UNKNOWN is injected at the exact trusted Docker API boundary, not daemon reset.
if phase=='unknown':
 shim=out/'shim';shim.mkdir(exist_ok=True);actual=subprocess.check_output(['which','docker'],text=True).strip()
 (shim/'docker').write_text('#!/bin/sh\nif [ -f '+str(out/'api-unavailable')+' ]; then echo "Cannot connect to the Docker daemon" >&2; exit 1; fi\nexec '+actual+' "$@"\n');(shim/'docker').chmod(0o755);env['PATH']=str(shim)+':'+os.environ['PATH']
p=subprocess.Popen([str(repo/'.cache/bin/lifecycle-temporal-probe')],env=env,stdout=open(out/'driver.log','w'),stderr=subprocess.STDOUT)
initial={'role':role,'phase':phase,'fixture':str(fixture)}
try:
 wait_until(lambda:active.is_set())
 audit=out/'runner-audit.json.execution.json';wait_until(audit.exists);identity=json.loads(audit.read_text())['execution_id'];private=pathlib.Path(json.loads(audit.read_text())['private_root'])
 wait_until(lambda:(private/'state/sdk-container.json').exists())
 sdk=json.loads((private/'state/sdk-container.json').read_text())['container'];container=json.loads((private/'state/state.json').read_text())['container']
 initial.update(execution_id=identity,private_root=str(private),sdk_container=sdk,command_container=container)
 if phase in ['execution','timeout','command-timeout']:
  wait_until(lambda:docker('exec',container,'test','-f','/tmp/tree/identities.json').returncode==0)
  initial['descendants']=json.loads(docker('exec',container,'cat','/tmp/tree/identities.json').stdout)
  wait_until(lambda:docker('exec',container,'test','-f','/tmp/tree/grandchild.pid').returncode==0)
  initial['grandchild_pid']=int(docker('exec',container,'cat','/tmp/tree/grandchild.pid').stdout)
  initial['trusted_process_inventory']=docker('top',container,'-eo','pid,ppid,args').stdout
  initial['known_active_command']=True
 else:
  if phase not in ['before-publication','verification','publication-validation','publication-applied-1','success','quota']:
   r=docker('exec','--user','100:101',container,'python3','-c',"import pathlib;p=pathlib.Path('/tmp/go-build');p.mkdir(exist_ok=True);(p/'marker').write_text('private')");assert r.returncode==0,r.stderr
 if phase=='runner-publication-interruption':wait_until(lambda:(out/'gate'/'publication-applied-1.ready').exists())
 if phase in ['before-publication','verification','publication-validation','publication-applied-1']:
  wait_until(lambda:(out/'gate'/(phase+'.ready')).exists())
  initial['checkpoint_at']=(out/'gate'/(phase+'.ready')).read_text()
 if phase=='worker-interruption':
  os.kill(p.pid,signal.SIGKILL);p.wait();initial['interrupted_worker_pid']=p.pid
  initial['workflow_id']=json.loads((out/'workflow-start.json').read_text())['workflow_id']
  env['CDO_RESUME_WORKFLOW_ID']=initial['workflow_id']
  p=subprocess.Popen([str(repo/'.cache/bin/lifecycle-temporal-probe')],env=env,stdout=open(out/'resume-driver.log','w'),stderr=subprocess.STDOUT)
 elif phase in ['runner-interruption','runner-publication-interruption','broker-interruption','transport-break']:
  # All target identities derive from this driver's known process/control tree.
  node=int(subprocess.check_output(['pgrep','-P',str(p.pid),'node'],text=True).strip())
  initial['runner_pid']=node
  if phase in ['runner-interruption','runner-publication-interruption']:os.kill(node,signal.SIGKILL)
  else:
   broker=int(subprocess.check_output(['pgrep','-P',str(node),'-f','serve-http'],text=True).strip());initial['broker_pid']=broker
   if phase=='broker-interruption':os.kill(broker,signal.SIGTERM)
   else:
    # Stop the known SDK's MCP transport peer; leave daemon healthy.
    os.kill(broker,signal.SIGKILL)
 elif phase=='unknown':
  paused=docker('pause',container);assert paused.returncode==0,paused.stderr;initial['disposable_resource_paused_for_unknown']=True
  (out/'api-unavailable').touch();(out/'cancel').touch()
 elif phase not in ['timeout','command-timeout','success','quota'] and not (phase=='execution' and role=='layer'):(out/'cancel').touch()
 if phase=='execution' and role=='layer':
  (out/'stale').touch();wait_until(lambda:(out/'stale-sent.json').exists());time.sleep(.3)
  initial['stale_external_green_rejected']=not (out/'workflow-result.json').exists()
  (out/'cancel').touch()
 initial['action_at']=time.time();(out/'observations.json').write_text(json.dumps(initial))
 p.wait(timeout=100)
 if phase=='unknown':
  initial['unknown_before_reconciliation']=json.loads(audit.read_text())
  (out/'api-unavailable').unlink()
  result=subprocess.run(['python3',str(repo/'runner/bin/cdo_reconcile.py'),str(audit)],capture_output=True,text=True,timeout=40)
  initial['reconciliation']={'exit_code':result.returncode,'stdout':result.stdout,'stderr':result.stderr}
  assert result.returncode==0,result.stderr
 end=time.monotonic()+30
 while time.monotonic()<end:
  if absent(container) and absent(sdk) and not private.exists():break
  time.sleep(.1)
 journal=out/('runner-audit.json.attempt-'+hashlib.sha256(str(fixture).encode()).hexdigest()[:16]+'.json')
 initial.update(command_absent=absent(container),sdk_absent=absent(sdk),private_removed=not private.exists(),journal=json.loads(journal.read_text()) if journal.exists() else None,observed_at=time.time(),driver_exit=p.returncode,source_contents={n:(fixture/n).read_text() for n in owned},provider_requests=len(requests))
 history=[json.loads(line) for line in (out/'history.jsonl').read_text().splitlines()];initial['history_events']=[e['eventType'] for e in history];initial['execution_activity_schedules']=sum(e.get('activityTaskScheduledEventAttributes',{}).get('activityType',{}).get('name')=='ExecutePlanTask' for e in history)
 if phase=='worker-interruption':
  lifecycle=json.loads(audit.read_text());ret={'result':{},'time':lifecycle['events'][-1]['at'],'error':'worker process lost; result not received'};initial['trusted_parent_loss_audit']=lifecycle
 else:ret=json.loads((out/'runner-return.json').read_text())
 initial['runner_return']=ret
 assertions={'command_cleanup':initial['command_absent'],'sdk_cleanup':initial['sdk_absent'],'private_cleanup':initial['private_removed'],'one_execution_activity':initial['execution_activity_schedules']==1,'no_result':not ret['result'].get('result') if phase!='success' else bool(ret['result'].get('result')),'journal_safe':initial['journal']['status'] in ('FAILED','PENDING') if phase!='success' else initial['journal']['status']=='COMPLETE'}
 if phase=='publication-applied-1':assertions['partial_invalidated']='proposed' in initial['source_contents'][owned[0]] and 'proposed' not in initial['source_contents'][owned[1]] and initial['journal']['status']!='COMPLETE'
 if phase=='execution' and role=='layer':assertions['stale_external_green_rejected']=initial['stale_external_green_rejected']
 if phase=='worker-interruption':assertions['worker_loss_timeout_no_retry']='EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT' in initial['history_events'] and initial['execution_activity_schedules']==1
 if phase=='command-timeout':assertions['command_timeout']=json.loads((out/'runner-audit.json').read_text())['broker']['failure']=='COMMAND_TIMEOUT'
 if phase=='timeout':assertions['real_activity_timeout']='EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT' in initial['history_events']
 if phase=='unknown':assertions['unknown_fail_closed']=initial['unknown_before_reconciliation']['status']=='infrastructure_unknown' and json.loads(audit.read_text())['status']=='infrastructure_unknown'
 if phase in ['execution','workflow','before-publication','verification','publication-validation','publication-applied-1','unknown']:
  assertions['temporal_cancel']='EVENT_TYPE_ACTIVITY_TASK_CANCEL_REQUESTED' in initial['history_events']
 if phase in ['execution','before-publication','verification','publication-validation','publication-applied-1']:
  terminal=json.loads((out/'workflow-result.json').read_text());assertions['runner_before_terminal']=ret['time']<=terminal['time']
 initial['assertions']=assertions;(out/'observations.json').write_text(json.dumps(initial,indent=2));print(json.dumps({'case':a.case,'assertions':assertions}));sys.exit(0 if all(assertions.values()) else 1)
finally:
 if p.poll() is None:p.kill();p.wait()
 server.shutdown()
