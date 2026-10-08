#!/usr/bin/env python3
"""Real SDK/HTTP broker teardown under SIGTERM and runner process-group SIGKILL.
No real model calls. Mirrors ProcessRunner's Unix group cancellation boundary.
"""
import hashlib,argparse,pathlib,tempfile,subprocess,os,threading,http.server,json,time,signal
ap=argparse.ArgumentParser();ap.add_argument('--image',required=True);ap.add_argument('--go-bundle');ap.add_argument('--output',required=True);args=ap.parse_args();repo=pathlib.Path(__file__).resolve().parents[1];base=repo.parents[1]/'.cdo-sandbox-certification-20261005';results={}
active=threading.Event()
class API(http.server.BaseHTTPRequestHandler):
 def log_message(self,*_):pass
 def do_POST(self):
  active.set();self.rfile.read(int(self.headers['Content-Length']));self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
  e={'type':'response.created','response':{'id':'waiting','status':'in_progress','output':[]}}
  self.wfile.write(('data: '+json.dumps(e)+'\n\n').encode());self.wfile.flush();time.sleep(30)
server=http.server.ThreadingHTTPServer(('0.0.0.0',0),API);threading.Thread(target=server.serve_forever,daemon=True).start()
for kind in ['context_cancel','runner_group_crash']:
 active.clear()
 fixture=pathlib.Path(tempfile.mkdtemp(prefix='cleanup-fixture-',dir=base)).resolve();(fixture/'a.go').write_text('package sample\n')
 if args.go_bundle:
  bundle=pathlib.Path(args.go_bundle).resolve();manifest=json.loads((bundle/'manifest.json').read_text());prepared_source=pathlib.Path(manifest['repository'])
  for n in ['go.mod','go.sum']:(fixture/n).write_bytes((prepared_source/n).read_bytes())
 env={**os.environ,'CDO_SDK_STATE_IMAGE':'cdo-sandbox-hardening:20261004-v3','CDO_SANDBOX_EXECUTION_MODE':'hardened-certification','CDO_CERTIFICATION_ROOT':str(fixture),'CDO_CERTIFICATION_EVIDENCE':str(base/(kind+'.json')),'CDO_BROKER_IMAGE':args.image,'CDO_CERTIFICATION_PROVIDER_URL':'http://127.0.0.1:'+str(server.server_port)+'/v1'}
 if args.go_bundle:env.update({'CDO_GO_DEPENDENCY_BUNDLE':str(bundle),'CDO_GO_DEPENDENCY_MANIFEST_SHA256':hashlib.sha256((bundle/'manifest.json').read_bytes()).hexdigest(),'CDO_BROKER_IMAGE':manifest['toolchain_image']})
 before=set(subprocess.check_output(['docker','ps','--filter','name=cdo-sandbox-broker-','--format','{{.Names}}'],text=True).split())
 p=subprocess.Popen(['node',str(repo/'runner/dist/index.js')],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,env=env,start_new_session=True)
 req={'role':'reviewer','working_directory':str(fixture),'model':'gpt-5.4','prompt':'Wait for cancellation.','output_schema':{'type':'object'},'sandbox_scope':{'profile':'reviewer','write_paths':[]}}
 p.stdin.write(json.dumps(req).encode());p.stdin.close()
 container=None
 for _ in range(200):
  names=set(subprocess.check_output(['docker','ps','--filter','name=cdo-sandbox-broker-','--format','{{.Names}}'],text=True).split())-before
  if names:container=next(iter(names));break
  if p.poll() is not None:break
  time.sleep(.1)
 if not container:raise RuntimeError('broker did not start')
 info=json.loads(subprocess.check_output(['docker','inspect',container]))[0];snapshot=pathlib.Path(next(x['Source'] for x in info['Mounts'] if x['Destination']=='/workspace'));private=snapshot.parent.parent
 sdk=None
 for _ in range(100):
  if (private/'state/sdk-container.json').exists():
   sdk=json.loads((private/'state/sdk-container.json').read_text())['container']
   if subprocess.run(['docker','inspect',sdk],capture_output=True).returncode==0:break
  time.sleep(.1)
 if sdk is None:raise RuntimeError('SDK container did not start')
 if not active.wait(15):raise RuntimeError('actual SDK model transport did not become active')
 before_cancel=json.loads(subprocess.check_output(['docker','exec',sdk,'python3','-c',"import os,json; s=os.statvfs('/execution/sdk-private');print(json.dumps({'bytes':(s.f_blocks-s.f_bfree)*s.f_frsize,'files':s.f_files-s.f_ffree}))"],text=True))
 telemetry=json.loads((private/'state/sdk-metrics.json').read_text())
 build_cache=subprocess.run(['docker','exec','--user','100:101',container,'python3','-c',"import pathlib;p=pathlib.Path('/tmp/go-build');p.mkdir(exist_ok=True);(p/'cleanup-marker').write_text('private')"],capture_output=True).returncode==0
 # Known background/session child; it is in the same OCI execution namespace.
 spawn=subprocess.run(['docker','exec','--user','100:101',container,'python3','-c','import subprocess;subprocess.Popen(["python3","-c","import os,signal,time;os.setsid();signal.signal(signal.SIGTERM,signal.SIG_IGN);time.sleep(120)"],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)'],capture_output=True)
 if kind=='context_cancel':p.send_signal(signal.SIGTERM)
 else:os.killpg(p.pid,signal.SIGKILL)
 p.wait(timeout=30)
 removed=False
 for _ in range(200):
  removed=subprocess.run(['docker','inspect',container],capture_output=True).returncode!=0
  if removed and not private.exists():break
  time.sleep(.1)
 journal=base/(kind+'.json.attempt-'+hashlib.sha256(str(fixture).encode()).hexdigest()[:16]+'.json')
 results[kind]={'private_build_cache_created':build_cache,'sdk_active':True,'initial':telemetry['initial'],'peak_observed':telemetry['peak'],'final_after_cleanup':{'bytes':0,'files':0},'max_bytes':telemetry['max_bytes'],'max_files':telemetry['max_files'],'private_state_before_cancel':before_cancel,'publication_journal':json.loads(journal.read_text())['status'],'exit_code':p.returncode,'container_removed':removed,'sdk_container_removed':subprocess.run(['docker','inspect',sdk],capture_output=True).returncode!=0,'private_control_staging_removed':not private.exists(),'known_session_child_started':spawn.returncode==0}
 p.stdout.close();p.stderr.close()
server.shutdown();pathlib.Path(args.output).write_text(json.dumps(results,indent=2));print(json.dumps(results));raise SystemExit(0 if all(x['publication_journal']=='FAILED' and x['sdk_active'] and x['sdk_container_removed'] and x['container_removed'] and x['private_control_staging_removed'] and x['known_session_child_started'] for x in results.values()) else 1)
