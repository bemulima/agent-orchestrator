#!/usr/bin/env python3
"""Owner-authorized disposable runtime probes. No credentials or product checkout.

Writes JSON evidence; exit1 for a failed adversarial invariant. This does not
certify SDK compatibility or run an agent. No worktree lifecycle is performed.
"""
import argparse, hashlib, json, os, pathlib, subprocess, sys, tempfile, uuid
sys.path.insert(0,str(pathlib.Path(__file__).resolve().parents[1]/'runner/bin'))
from cdo_oci_profile import docker_command, writable_files

PROBE = r'''
import os, pathlib, socket, subprocess, resource, json, ctypes, errno, signal, fcntl, struct
p=pathlib.Path; results={}
def deny(name,fn):
 try:fn()
 except (OSError,ValueError):results[name]=True
 else:results[name]=False
for name,target in {'host_home':'/Users/marat','codex_auth':'/data/codex/auth.json','sibling_worktree':'/data/worktrees','other_repository':'/projects','ssh':'/root/.ssh','docker_data':'/var/lib/docker','docker_socket':'/var/run/docker.sock'}.items(): results[name]=not p(target).exists() or (p(target).is_dir() and not any(p(target).iterdir()))
for name,target in {'frozen_write':'/workspace/internal/domain/contract.go','sibling_scope_write':'/workspace/internal/sibling.go','composition_write':'/workspace/cmd/main.go','outside_write':'/etc/escape','absolute_escape':'/outside','traversal_escape':'/workspace/../../outside'}.items():deny(name,lambda target=target:p(target).write_text('forbidden'))
os.symlink('/etc/passwd','/tmp/escape');deny('symlink_escape',lambda:p('/tmp/escape').write_text('forbidden'))
for name,address in [('internet',('1.1.1.1',443)),('unrelated_localhost',('127.0.0.1',18083))]:
 s=socket.socket();s.settimeout(.5);deny(name,lambda:s.connect(address));s.close()
s=socket.socket(socket.AF_UNIX);deny('docker_daemon',lambda:s.connect('/var/run/docker.sock'));s.close()
lib=ctypes.CDLL(None,use_errno=True)
results['namespace_create_blocked']=lib.unshare(0x10000000)==-1 and ctypes.get_errno() in (errno.EPERM,errno.EACCES)
results['mount_blocked']=lib.mount(b'none',b'/tmp',b'tmpfs',0,None)==-1 and ctypes.get_errno() in (errno.EPERM,errno.EACCES)
results['unrelated_signaling']=lib.kill(999999,0)==-1 and ctypes.get_errno()==errno.ESRCH
results['git_core_unavailable']=not p('/usr/libexec/git-core').exists()
# LinuxKit creates dormant tunnel devices even in network=none. Check actual
# namespace-local interface flags and routes, not an assumed device-name list.
network_interfaces={}
with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as network_probe:
 for line in p('/proc/net/dev').read_text().splitlines()[2:]:
  name=line.split(':')[0].strip()
  raw=fcntl.ioctl(network_probe.fileno(),0x8913,struct.pack('256s',name.encode()))
  network_interfaces[name]=struct.unpack_from('H',raw,16)[0]
network_routes=p('/proc/net/route').read_text().splitlines()[1:]
ipv6_routes=p('/proc/net/ipv6_route').read_text().splitlines()
results['network_interface_isolation']=all(name=='lo' or not flags&1 for name,flags in network_interfaces.items()) and not network_routes and all(line.split()[-1]=='lo' for line in ipv6_routes)
status=p('/proc/self/status').read_text();results['seccomp']=('Seccomp:\t2' in status);results['capabilities_dropped']='CapEff:\t0000000000000000' in status and 'CapBnd:\t0000000000000000' in status;results['no_new_privileges']='NoNewPrivs:\t1' in status
results['credential_env']=not any(k in os.environ for k in ['CODEX_HOME','CODEX_API_KEY','OPENAI_API_KEY','DATABASE_URL','GITHUB_TOKEN','GITLAB_TOKEN','SSH_AUTH_SOCK','TEST_DATABASE_URL'])
results['host_process_inspection']=not any(b'course-dev-orchestrator' in p(x).read_bytes() for x in pathlib.Path('/proc').glob('[0-9]*/cmdline'))
for name,args in [('worktree_management',['git','worktree','add','/tmp/unapproved']),('git_init_scratch',['git','init','/tmp/new-repository']),('worktree_remove',['git','worktree','remove','/tmp/unapproved']),('worktree_move',['git','worktree','move','/tmp/a','/tmp/b']),('git_commit',['git','commit','-m','unapproved']),('git_push',['git','push']),('git_fetch',['git','fetch','https://example.invalid/repository']),('git_credential',['git','credential','fill']),('global_git_mutation',['git','config','--global','fixture.forbidden','yes']),('global_git_override',['env','GIT_CONFIG_GLOBAL=/workspace/internal/owned.go','git','config','--global','fixture.forbidden','yes']),('absolute_git_credential',['/usr/bin/git','-c','credential.helper=!printf credential','credential','fill'])]:
 r=subprocess.run(args,input=b'',stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL);results[name]=r.returncode==126
p('/workspace/internal/owned.go').write_text('package fixture\nfunc Value() int {return 42}\n');results['owned_write']=p('/workspace/internal/owned.go').read_text().endswith('42}\n')
r=subprocess.run(['go','test','./...'],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,env={**os.environ,'GO111MODULE':'off'},timeout=60);results['allowed_go_tests']=r.returncode==0; go_output=r.stdout.decode(errors='replace')[:4096]
limits={'fsize':resource.getrlimit(resource.RLIMIT_FSIZE),'nofile':resource.getrlimit(resource.RLIMIT_NOFILE),'pids':p('/sys/fs/cgroup/pids.max').read_text().strip(),'memory':p('/sys/fs/cgroup/memory.max').read_text().strip(),'cpu':p('/sys/fs/cgroup/cpu.max').read_text().strip()};results['resource_limits']=limits['fsize'][0]==16777216 and limits['pids']=='128' and limits['memory']=='2147483648' and limits['cpu'].split()[0]=='200000'
# A bound file mount cannot support atomic replacement in its readonly parent.
# Preserve this as an explicit compatibility blocker rather than weaken scope.
p('/tmp/replacement').write_text('replacement');deny('atomic_replacement_blocked',lambda:os.replace('/tmp/replacement','/workspace/internal/owned.go'))
print(json.dumps({'results':results,'limits':limits,'network_interfaces':network_interfaces,'network_routes':network_routes,'ipv6_routes':ipv6_routes,'go_test_output':go_output,'all_probe_invariants_pass':all(results.values()),'namespaces':{n:os.readlink('/proc/self/ns/'+n) for n in ['user','pid','mnt','net']}}))
raise SystemExit(0 if all(results.values()) else 1)
'''

def main():
 ap=argparse.ArgumentParser();ap.add_argument('--image',required=True);ap.add_argument('--output',required=True);args=ap.parse_args()
 with tempfile.TemporaryDirectory(prefix='cdo-sandbox-probe-') as temporary:
  root=pathlib.Path(temporary).resolve();os.chmod(root,0o755)
  for relative in ['internal/domain/contract.go','internal/sibling.go','cmd/main.go','internal/owned.go','internal/owned_test.go']:
   f=root/relative;f.parent.mkdir(parents=True,exist_ok=True);f.write_text('fixture' if relative not in ['internal/owned.go','internal/owned_test.go'] else ('package fixture\nfunc Value() int{return 0}\n' if relative.endswith('owned.go') else 'package fixture\nimport "testing"\nfunc TestValue(t *testing.T){if Value()!=42{t.Fatal("semantic failure")}}\n'));os.chmod(f,0o666 if relative=='internal/owned.go' else 0o644)
  # Keep other fixture paths outside the Go package so full suite remains valid.
  (root/'internal/domain/contract.go').write_text('package domain\n');(root/'internal/sibling.go').write_text('package fixture\n');(root/'cmd/main.go').write_text('package cmd\n')
  (root/'probe.py').write_text(PROBE);os.chmod(root/'probe.py',0o644)
  name='cdo-sandbox-probe-'+uuid.uuid4().hex[:12]
  command=docker_command(args.image,root,'layer',['internal/owned.go'],['python3','/workspace/probe.py'],name)
  try:r=subprocess.run(command,capture_output=True,text=True,timeout=90)
  except subprocess.TimeoutExpired:
   subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL);raise
  evidence={'profile':'OCI_COMMAND_CANDIDATE','image':args.image,'exit_code':r.returncode,'stdout':r.stdout[:1048576],'stderr':r.stderr[:4096],'scope':['internal/owned.go'],'SDK_AGENT_CANARY':'NOT_RUN','PRODUCTION_SANDBOX_READY':'NO'}
  try:evidence['probe']=json.loads(r.stdout)
  except ValueError:pass
  evidence['orchestrator_verification']={
   'owned_result_matches':(root/'internal/owned.go').read_text()=='package fixture\nfunc Value() int {return 42}\n',
   'frozen_contract_unchanged':(root/'internal/domain/contract.go').read_text()=='package domain\n',
   'sibling_scope_unchanged':(root/'internal/sibling.go').read_text()=='package fixture\n',
   'composition_scope_unchanged':(root/'cmd/main.go').read_text()=='package cmd\n',
   'container_removed':subprocess.run(['docker','inspect',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode!=0
  }
  # Negative loader probes independently reject escapes before container launch.
  (root/'link').symlink_to('/etc/passwd');rejections={}
  for bad in ['../outside','/absolute','internal/../../escape','link','.git/config','cmd/main.go']:
   try:writable_files(root,'layer' if bad!='cmd/main.go' else 'reviewer',[bad])
   except ValueError:rejections[bad]=True
   else:rejections[bad]=False
  evidence['scope_loader_rejections']=rejections
  out=pathlib.Path(args.output);out.parent.mkdir(parents=True,exist_ok=True);out.write_text(json.dumps(evidence,indent=2));print(json.dumps({'exit_code':r.returncode,'probes':evidence.get('probe',{}).get('results'),'output':str(out)}));return r.returncode or (0 if all(rejections.values()) and all(evidence['orchestrator_verification'].values()) else 1)
if __name__=='__main__':raise SystemExit(main())
