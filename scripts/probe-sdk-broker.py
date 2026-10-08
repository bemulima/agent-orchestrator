#!/usr/bin/env python3
"""Deterministic fake-model transport exercising the real pinned SDK/MCP path.
This is not a real model canary and consumes no model calls.
"""
import argparse,threading,http.server,json,subprocess,pathlib,os,tempfile,sys
ap=argparse.ArgumentParser();ap.add_argument('--image',required=True);ap.add_argument('--output',required=True);ap.add_argument('--go-bundle');ap.add_argument('--dependency-denied',action='store_true');ap.add_argument('--file-count-probe',action='store_true');ap.add_argument('--sdk-failure',action='store_true');ap.add_argument('--metadata-storage-probe',action='store_true');ap.add_argument('--role',choices=['contract','layer','composition','reviewer'],default='layer');a=ap.parse_args()
repo=pathlib.Path(__file__).resolve().parents[1];base=repo.parents[1]/'.cdo-sandbox-certification-20261005';base.mkdir(exist_ok=True)
fixture=pathlib.Path(tempfile.mkdtemp(prefix='sdk-fixture-',dir=base)).resolve();(fixture/'internal').mkdir();(fixture/'cmd').mkdir();(fixture/'contracts').mkdir()
(fixture/'internal/value.go').write_text('package sample\nfunc Value() int{return 0}\n');(fixture/'internal/value_test.go').write_text('package sample\nimport "testing"\nfunc TestValue(t *testing.T){if Value()!=42{t.Fatal("expected42")}}\n');(fixture/'internal/sibling.go').write_text('package sample\n');(fixture/'contracts/frozen.go').write_text('package contracts\n');(fixture/'cmd/main.go').write_text('package main\n')
calls=[('mcp__cdo__read_file',{'path':'internal/value.go'}),('mcp__cdo__propose_file',{'path':'internal/sibling.go','content':'forbidden'}),('mcp__cdo__propose_file',{'path':'contracts/frozen.go','content':'forbidden'}),('mcp__cdo__propose_file',{'path':'cmd/main.go','content':'forbidden'}),('mcp__cdo__propose_file',{'path':'../escape','content':'forbidden'}),('mcp__cdo__propose_file',{'path':'internal/value.go','content':'package sample\nfunc Value() int{return 42}\n'}),('mcp__cdo__command',{'argv':['go','test','./...']})]
owned={'contract':'contracts/current.go','layer':'internal/value.go','composition':'cmd/main.go','reviewer':None}[a.role]
if a.role!='layer':
 (fixture/'internal/value_test.go').write_text('package sample\nimport "testing"\nfunc TestValue(t *testing.T){if Value()!=0{t.Fatal("expected0")}}\n')
 calls=[(n,args) for n,args in calls if n!='mcp__cdo__propose_file' or args['path']!='internal/value.go']
 if owned:calls.insert(-1,('mcp__cdo__propose_file',{'path':owned,'content':'package '+('contracts' if a.role=='contract' else 'main')+'\nvar Wiring=42\n'}))
if a.go_bundle:
 bundle=pathlib.Path(a.go_bundle).resolve();manifest=json.loads((bundle/'manifest.json').read_text());prepared_source=pathlib.Path(manifest['repository'])
 for n in ['go.mod','go.sum']:(fixture/n).write_bytes((prepared_source/n).read_bytes())
 (fixture/'internal/value.go').write_text('package sample\nimport "github.com/google/uuid"\nfunc Value() int { _ = uuid.MustParse("11111111-1111-4111-8111-111111111111");return 0 }\n')
 for name,args in calls:
  if name=='mcp__cdo__propose_file' and args['path']=='internal/value.go':args['content']='package sample\nimport "github.com/google/uuid"\nfunc Value() int {return int(uuid.MustParse("11111111-1111-4111-8111-111111111111").Version())*10+2}\n'
private_reads=['/execution/sdk-private/home/state_5.sqlite','/reference/auth.json','/root/.codex/auth.json','/root/.ssh/id_rsa','/var/run/docker.sock','../sibling-state']
calls=[('mcp__cdo__read_file',{'path':p}) for p in private_reads]+calls
if a.metadata_storage_probe:
 calls=[('update_plan',{'explanation':'x'*600000,'plan':[{'step':'controlled storage probe '+str(i),'status':'in_progress'}]}) for i in range(20)]+calls
calls.insert(0,("tool_search",{"query":"cdo read_file propose_file command","limit":10}))
seen=[];responses=[];toolnames=set();index=0
class Handler(http.server.BaseHTTPRequestHandler):
 def log_message(self,*_):pass
 def do_GET(self):self.send_response(200);self.end_headers();self.wfile.write(b'{"data":[]}')
 def do_POST(self):
  global index
  if a.file_count_probe and index==0:
   index+=1
   names=subprocess.check_output(['docker','ps','--filter','name=cdo-sdk-state-','--format','{{.Names}}'],text=True).split()
   if len(names)!=1:raise RuntimeError('unique disposable SDK namespace required for file-count probe')
   subprocess.run(['docker','exec',names[0],'python3','-c',"import pathlib,time; r=pathlib.Path('/execution/sdk-private'); i=0\ntry:\n while True: (r/('fill-'+str(i))).touch();i+=1\nexcept OSError: time.sleep(1)"],capture_output=True,timeout=10)
   self.send_error(400);return
  if a.sdk_failure:self.send_error(400,'controlled SDK transport failure');return
  v=json.loads(self.rfile.read(int(self.headers['Content-Length'])));tools=v.get('tools',[])
  for t in tools + [t for x in v.get('input',[]) if x.get('type')=='tool_search_output' for t in x.get('tools',[])]:
   if t.get('name'):toolnames.add(t['name'])
   if t.get('type')=='namespace':
    for x in t.get('tools',[]):toolnames.add(t['name']+'__'+x['name'])
  seen.append({'tools':tools,'input_types':[x.get('type') for x in v.get('input',[])]});responses.extend(x for x in v.get('input',[]) if x.get('type')=='function_call_output')
  if index<len(calls):
   name,args=calls[index]
   if name=='tool_search':item={'type':'tool_search_call','execution':'client','arguments':args,'call_id':'call_'+str(index),'id':'ts_'+str(index),'status':'completed'}
   else:item={'type':'function_call',**({'namespace':'mcp__cdo'} if name.startswith('mcp__cdo__') else {}),'name':name.replace('mcp__cdo__',''),'arguments':json.dumps(args),'call_id':'call_'+str(index),'id':'fc_'+str(index),'status':'completed'}
  else:item={'type':'message','role':'assistant','id':'msg_final','status':'completed','content':[{'type':'output_text','text':'{"status":"completed"}'}]}
  index+=1;self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
  response={'id':'resp_'+str(index),'object':'response','status':'completed','output':[item],'usage':{'input_tokens':1,'output_tokens':1,'total_tokens':2}}
  events=[{'type':'response.created','response':{**response,'status':'in_progress','output':[]}},{'type':'response.output_item.added','output_index':0,'item':item},{'type':'response.output_item.done','output_index':0,'item':item},{'type':'response.completed','response':response}]
  for e in events:self.wfile.write(('data: '+json.dumps(e)+'\n\n').encode());self.wfile.flush()
original={n:p.read_bytes() for p in fixture.rglob('*') if p.is_file() for n in [str(p.relative_to(fixture))]}
server=http.server.ThreadingHTTPServer(('0.0.0.0',0),Handler);threading.Thread(target=server.serve_forever,daemon=True).start()
env={**os.environ,'CDO_SDK_STATE_IMAGE':'cdo-sandbox-hardening:20261004-v3','CDO_SANDBOX_EXECUTION_MODE':'hardened-certification','CDO_CERTIFICATION_ROOT':str(fixture),'CDO_BROKER_IMAGE':a.image,'CDO_CERTIFICATION_EVIDENCE':str(base/'sdk-publication.json'),'CDO_CERTIFICATION_PROVIDER_URL':'http://127.0.0.1:'+str(server.server_port)+'/v1'}
if a.go_bundle:
 env.update({'CDO_GO_DEPENDENCY_BUNDLE':str(bundle),'CDO_GO_DEPENDENCY_MANIFEST_SHA256':__import__('hashlib').sha256((bundle/'manifest.json').read_bytes()).hexdigest(),'CDO_BROKER_IMAGE':manifest['toolchain_image']})
request={'role':'reviewer' if a.role=='reviewer' else 'coder','working_directory':str(fixture),'model':'gpt-5.4','reasoning_effort':'low','prompt':'Complete fixture through broker.','output_schema':{'type':'object','properties':{'status':{'type':'string'}},'required':['status'],'additionalProperties':False},'sandbox_scope':{'profile':a.role,'write_paths':[] if owned is None else [owned]}}
r=subprocess.run(['node',str(repo/'runner/dist/index.js')],input=json.dumps(request),env=env,capture_output=True,text=True,timeout=120)
server.shutdown();e={'transport':'FAKE_MODEL_REAL_PINNED_SDK','exit_code':r.returncode,'stdout':r.stdout,'stderr':r.stderr,'request_tools':seen,'tool_names':sorted(toolnames),'tool_results':responses,'fixture':str(fixture),'published_value':(fixture/'internal/value.go').read_text(),'sibling_unchanged':(fixture/'internal/sibling.go').read_text()=='package sample\n','frozen_unchanged':(fixture/'contracts/frozen.go').read_text()=='package contracts\n','composition_unchanged':(fixture/'cmd/main.go').read_text()=='package main\n'}
if a.dependency_denied:
 e['assertions']={'typed':r.returncode!=0 and 'GO_DEPENDENCY_CONTENT_MISMATCH' in r.stderr,'no_result':'"type":"result"' not in r.stdout,'no_sdk_model_transport':len(seen)==0,'source_unchanged':all((fixture/n).read_bytes()==b for n,b in original.items())}
 pathlib.Path(a.output).write_text(json.dumps(e,indent=2));print(json.dumps(e['assertions']));sys.exit(0 if all(e['assertions'].values()) else 1)
e["publication"]=json.loads((base/"sdk-publication.json").read_text())
if a.sdk_failure or a.metadata_storage_probe or a.file_count_probe:
 import hashlib
 journal=base/('sdk-publication.json.attempt-'+hashlib.sha256(str(fixture).encode()).hexdigest()[:16]+'.json')
 e['publication_journal']=json.loads(journal.read_text())
 assert e['publication_journal']['status']=='FAILED'
if a.sdk_failure:
 e['assertions']={'failed':r.returncode!=0,'source_unchanged':all((fixture/n).read_bytes()==b for n,b in original.items()),'cleanup':e['publication']['sdk_private_state']['cleanup'],'no_result':'"type":"result"' not in r.stdout}
 pathlib.Path(a.output).write_text(json.dumps(e,indent=2));print(json.dumps({'assertions':e['assertions'],'output':a.output}));sys.exit(0 if all(e['assertions'].values()) else 1)
if a.metadata_storage_probe or a.file_count_probe:
 e['assertions']={'typed_overflow':r.returncode!=0 and ('SDK_PRIVATE_STATE_FILES_EXCEEDED' if a.file_count_probe else 'SDK_PRIVATE_STATE_BYTES_EXCEEDED') in r.stderr,'source_unchanged':all((fixture/n).read_bytes()==b for n,b in original.items()),'no_success_result':'"type":"result"' not in r.stdout,'hard_capacity':e['publication']['sdk_private_state']['peak']['bytes']<=8*1024*1024,'cleanup':e['publication']['sdk_private_state']['cleanup']}
 pathlib.Path(a.output).write_text(json.dumps(e,indent=2));print(json.dumps({'assertions':e['assertions'],'output':a.output}));sys.exit(0 if all(e['assertions'].values()) else 1)
private_ids=['call_'+str(i) for i,(n,args) in enumerate(calls) if n=='mcp__cdo__read_file' and args['path'] in private_reads]
e["assertions"]={"private_reads_denied":all(any(v.get('call_id')==ident and 'PATH_DENIED' in str(v.get('output')) for v in responses) for ident in private_ids),"publication_occurred":(("return int(uuid.MustParse" in e["published_value"] if a.go_bundle else e["published_value"]=="package sample\nfunc Value() int{return 42}\n") if a.role=="layer" else len(json.loads((base/"sdk-publication.json").read_text())["publication"])==(0 if a.role=="reviewer" else 1)),"sibling_unchanged":e["sibling_unchanged"],"frozen_unchanged":e["frozen_unchanged"],"only_owned_changed":all((fixture/n).read_bytes()==b for n,b in original.items() if n!=owned),"optional_metadata_absent":not any(n in toolnames for n in ["skills","skills__list","skills__read","create_goal","get_goal","update_goal","request_user_input"]),"native_tools_absent":not any(n in toolnames for n in ["apply_patch","exec_command","shell_command","shell","view_image","write_stdin"]),"broker_command_executed":json.loads((base/"sdk-publication.json").read_text())["broker"]["commands"]>0}
pathlib.Path(a.output).write_text(json.dumps(e,indent=2));print(json.dumps({'exit':r.returncode,'tools':sorted(toolnames),'stdout':r.stdout,'stderr':r.stderr[-3000:],'output':a.output}));sys.exit(r.returncode or (0 if all(e["assertions"].values()) else 1))
