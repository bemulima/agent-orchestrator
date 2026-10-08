"""Trusted broker; model arguments never select OCI flags or publication roots.

Native SDK tools must be absent before admission. Production certification is
separate from this implementation. JSON-RPC stdio is the only model surface.
"""
import base64, hashlib, json, os, pathlib, selectors, shutil, signal, stat, subprocess, sys, tempfile, time, uuid, threading, http.server
from cdo_oci_profile import docker_command
from cdo_go_dependencies import validate, DependencyDenied

class Denied(Exception): pass
class LimitExceeded(Exception): pass

def digest(b): return hashlib.sha256(b).hexdigest()

def container_absent(name):
    """Daemon errors are infrastructure UNKNOWN, never proof of cleanup."""
    try:r=subprocess.run(['docker','inspect',name],capture_output=True,text=True,timeout=15)
    except (subprocess.TimeoutExpired,OSError) as e:raise Denied('DOCKER_CONTAINER_STATE_UNKNOWN') from e
    if r.returncode==0:return False
    if 'no such object:' in r.stderr.lower() or 'no such container:' in r.stderr.lower():return True
    raise Denied('DOCKER_CONTAINER_STATE_UNKNOWN')

def persist_execution_audit(file,value):
    temporary=file.with_suffix('.lifecycle.tmp')
    with open(temporary,'w') as f:
        os.chmod(temporary,0o600);json.dump(value,f);f.flush();os.fsync(f.fileno())
    os.replace(temporary,file)
    fd=os.open(file.parent,os.O_RDONLY|os.O_DIRECTORY)
    try:os.fsync(fd)
    finally:os.close(fd)

def relative(name):
    if not isinstance(name,str) or not name or len(name)>1024 or any(ord(c)<32 for c in name) or any(c in name for c in '\\*?[],'):
        raise Denied('PATH_DENIED')
    parts=name.split('/')
    if any(p in ('','.','..','.git','.codex','.agents','.ssh','.aws') or p=='.env' or p.startswith('.env.') for p in parts): raise Denied('PATH_DENIED')
    return name

def regular(root,name,missing=False):
    relative(name); target=root
    parts=name.split('/')
    for i,part in enumerate(parts):
        target=target/part
        try: s=target.lstat()
        except FileNotFoundError:
            if missing: continue
            raise Denied('MISSING_INPUT')
        if stat.S_ISLNK(s.st_mode) or (i<len(parts)-1 and not stat.S_ISDIR(s.st_mode)) or (i==len(parts)-1 and (not stat.S_ISREG(s.st_mode) or s.st_nlink!=1)):
            raise Denied('LINK_OR_FILE_TYPE_DENIED')
    return target

class Policy:
    def __init__(self,v):
        if set(v)-{'root','role','phase','write_paths','delete_paths','execution_id','image','evidence','state_dir','token','go_dependency_bundle','go_dependency_manifest_sha256'}: raise Denied('UNKNOWN_TRUSTED_FIELD')
        self.root=pathlib.Path(v['root'])
        if not self.root.is_absolute() or self.root.resolve(strict=True)!=self.root or not self.root.is_dir(): raise Denied('ROOT_DENIED')
        self.role=v['role']; self.phase=v['phase']; self.write=sorted(v['write_paths']); self.delete=sorted(v.get('delete_paths',[]))
        if self.role not in ('contract','layer','composition','reviewer') or not self.phase: raise Denied('ROLE_DENIED')
        if len(self.write)>128 or len(set(self.write))!=len(self.write) or not set(self.delete)<=set(self.write): raise Denied('SCOPE_DENIED')
        if self.role=='reviewer' and self.write: raise Denied('REVIEWER_WRITE_DENIED')
        for n in self.write:
            regular(self.root,n,missing=True)
            if self.role=='composition' and not n.startswith('cmd/'): raise Denied('COMPOSITION_SCOPE_DENIED')
            if self.role=='layer' and n.startswith('cmd/'): raise Denied('LAYER_COMPOSITION_DENIED')
        self.token=v.get('token'); self.execution_id=v['execution_id']; self.image=v['image']; self.state_dir=pathlib.Path(v['state_dir']).resolve(); self.evidence=pathlib.Path(v['evidence']).resolve()
        if not self.execution_id or not self.state_dir.is_dir(): raise Denied('BROKER_STATE_ABSENT')
        self.publication_journal=self.evidence.with_name(self.evidence.name+'.attempt-'+digest(str(self.root).encode())[:16]+'.json')
        self.go_bundle=v.get('go_dependency_bundle');self.go_manifest=v.get('go_dependency_manifest_sha256')
        if bool(self.go_bundle)!=bool(self.go_manifest):raise Denied('GO_DEPENDENCY_CONFIGURATION_ABSENT')
        if (self.root/'go.mod').exists() and not self.go_bundle:raise DependencyDenied('GO_DEPENDENCY_PREPARATION_REQUIRED')
        if self.go_bundle:
            try:validate(self.go_bundle,self.go_manifest,self.root,self.image)
            except DependencyDenied:
                Publisher(self,{}).invalidate();raise
        self.file_bytes=262144; self.aggregate_bytes=1048576; self.file_count=128
        self.commands=256; self.output_bytes=1048576; self.command_seconds=60; self.execution_seconds=1200

class Publisher:
    def __init__(self,policy,original): self.p=policy; self.original=original
    def invalidate(self):
        journal=self.p.publication_journal
        if journal.exists() and json.loads(journal.read_text()).get('status') in ('FAILED','PENDING'):return
        temporary=journal.with_suffix('.invalid.tmp')
        with open(temporary,'x') as f:
            json.dump({'execution_id':self.p.execution_id,'status':'FAILED','at':time.time()},f);f.flush();os.fsync(f.fileno())
        os.replace(temporary,journal)
        fd=os.open(journal.parent,os.O_RDONLY|os.O_DIRECTORY)
        try:os.fsync(fd)
        finally:os.close(fd)
    def publish(self,proposals):
        journal=self.p.publication_journal
        if journal.exists():raise Denied('PUBLICATION_ATTEMPT_ALREADY_USED')
        def record(status):
            temporary=journal.with_suffix('.tmp')
            with open(temporary,'w') as f:
                json.dump({'execution_id':self.p.execution_id,'status':status,'at':time.time()},f);f.flush();os.fsync(f.fileno())
            os.replace(temporary,journal)
            fd=os.open(journal.parent,os.O_RDONLY|os.O_DIRECTORY)
            try:os.fsync(fd)
            finally:os.close(fd)
        record('PENDING')
        try:
            lifecycle_checkpoint('publication-validation')
            audit=self._publish(proposals)
            record('COMPLETE');return audit
        except BaseException:
            record('FAILED');raise
    def _publish(self,proposals):
        if len(proposals)>self.p.file_count: raise LimitExceeded('FILE_COUNT_LIMIT')
        if sum(len(base64.b64decode(x['data'],validate=True)) for x in proposals.values() if x['op']=='write')>self.p.aggregate_bytes: raise LimitExceeded('PUBLICATION_BYTES_LIMIT')
        validated=[]
        # Open each destination ancestor without following links. Hold descriptors
        # through replacement; a swapped ancestor cannot redirect publication.
        try:
            for name,item in sorted(proposals.items()):
                relative(name)
                if name not in self.p.write: raise Denied('PUBLICATION_SCOPE_DENIED')
                if item['op']=='delete' and name not in self.p.delete: raise Denied('DELETE_DENIED')
                if item['op'] not in ('write','delete'): raise Denied('METADATA_CHANGE_DENIED')
                data=base64.b64decode(item['data'],validate=True) if item['op']=='write' else None
                if data is not None and len(data)>self.p.file_bytes: raise LimitExceeded('FILE_BYTES_LIMIT')
                regular(self.p.root,name,missing=True)
                fd=os.open(self.p.root,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
                for component in name.split('/')[:-1]:
                    try: nextfd=os.open(component,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=fd)
                    except FileNotFoundError: raise Denied('NEW_PARENT_NOT_APPROVED')
                    os.close(fd);fd=nextfd
                leaf=name.split('/')[-1]
                try:
                    oldfd=os.open(leaf,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=fd)
                    try:
                        st=os.fstat(oldfd)
                        if not stat.S_ISREG(st.st_mode) or st.st_nlink!=1: raise Denied('DESTINATION_TYPE_DENIED')
                        old=os.read(oldfd,self.p.file_bytes+1); mode=stat.S_IMODE(st.st_mode)
                    finally:os.close(oldfd)
                except FileNotFoundError:old=None; mode=0o644
                expected=self.original.get(name)
                if (None if old is None else digest(old))!=expected:os.close(fd);raise Denied('SOURCE_PRECONDITION_CHANGED')
                if mode not in (0o644,0o755):os.close(fd);raise Denied('UNSUPPORTED_METADATA')
                validated.append((name,leaf,fd,data,mode,expected))
            audit=[]
            for name,leaf,fd,data,mode,expected in validated:
                if data is None: os.unlink(leaf,dir_fd=fd)
                else:
                    temporary='.cdo-publish-'+uuid.uuid4().hex
                    out=os.open(temporary,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,mode,dir_fd=fd)
                    try:
                        with os.fdopen(out,'wb') as f:f.write(data);f.flush();os.fsync(f.fileno())
                        os.replace(temporary,leaf,src_dir_fd=fd,dst_dir_fd=fd)
                    finally:
                        try:os.unlink(temporary,dir_fd=fd)
                        except FileNotFoundError:pass
                os.fsync(fd)
                lifecycle_checkpoint('publication-applied-'+str(len(audit)+1))
                audit.append({'path':name,'operation':'delete' if data is None else 'write','original':expected,'proposed':None if data is None else digest(data),'published':None if data is None else digest(regular(self.p.root,name).read_bytes())})
            return audit
        finally:
            for _,_,fd,_,_,_ in validated:os.close(fd)

# Runs only as trusted root in a credential-free OCI container. It creates a
# root-owned readonly source view; untrusted commands always run as UID100.
VIEW_HELPER=r'''
import pathlib,json,os,tempfile,shutil,base64,stat
v=json.load(__import__('sys').stdin);root=pathlib.Path('/tmp/view')
if v['op']=='init':
 shutil.copytree('/workspace',root);paths=sorted(root.rglob('*'),reverse=True)
 for p in paths:p.chmod(0o555 if p.is_dir() or p.stat().st_mode&0o111 else 0o444)
 root.chmod(0o555)
else:
 p=root/v['path'];parent=p.parent
 if not parent.is_dir():raise RuntimeError('NEW_PARENT_NOT_APPROVED')
 parent.chmod(0o755)
 try:
  if v['op']=='delete':p.unlink()
  else:
   fd,n=tempfile.mkstemp(dir=parent)
   with os.fdopen(fd,'wb') as f:f.write(base64.b64decode(v['data'],validate=True))
   os.chmod(n,0o444);os.replace(n,p)
 finally:parent.chmod(0o555)
'''

class Broker:
    def __init__(self,policy):
        self.p=policy;self.started=time.monotonic();self.command_count=0;self.output_count=0;self.proposed_total=0;self.failure=None;self.proposals={};self.original={};self.events=[];self.process=None
        self.name='cdo-sandbox-broker-'+uuid.uuid4().hex[:16]
        self.snapshot=self.p.state_dir/'snapshot';self.snapshot.mkdir(mode=0o755)
        total=0;count=0
        for folder,dirs,files in os.walk(self.p.root,followlinks=False):
            dirs[:]=sorted(d for d in dirs if d not in ('.git','.codex','.agents','.ssh','.aws','.cache','node_modules') and not pathlib.Path(folder,d).is_symlink())
            for file in sorted(files):
                n=str(pathlib.Path(folder,file).relative_to(self.p.root))
                try:relative(n)
                except Denied:continue
                source=regular(self.p.root,n)
                if source.stat().st_size>16777216:raise LimitExceeded('SOURCE_FILE_LIMIT')
                b=source.read_bytes();total+=len(b);count+=1
                if len(b)>16777216 or total>67108864 or count>4096: raise LimitExceeded('SOURCE_SNAPSHOT_LIMIT')
                self.original[n]=digest(b);out=self.snapshot/n;out.parent.mkdir(parents=True,exist_ok=True);out.write_bytes(b);out.chmod(0o555 if source.stat().st_mode&0o111 else 0o444)
        self._save()
    def _save(self):
        state={'original':self.original,'proposals':self.proposals,'failure':self.failure,'container':self.name,'commands':self.command_count,'output_bytes':self.output_count,'proposed_bytes':self.proposed_total,'events':self.events}
        f=self.p.state_dir/'state.json';tmp=f.with_suffix('.tmp');tmp.write_text(json.dumps(state));tmp.chmod(0o600);os.replace(tmp,f)
    def launch(self):
        # Closing the sole stdin writer ends PID1, including after broker SIGKILL.
        keepalive="import sys,select,time; deadline=time.monotonic()+1200\nwhile time.monotonic()<deadline:\n r,_,_=select.select([sys.stdin],[],[],1)\n if r and not sys.stdin.buffer.read1(1):break"
        args=docker_command(self.p.image,self.snapshot,'reviewer',[],['python3','-c',keepalive],self.name)
        args[2:2]=['--label','cdo.execution_id='+self.p.execution_id,'--label','cdo.resource=command']
        if self.p.go_bundle:
            args[2:2]=['--mount','type=bind,src='+self.p.go_bundle+',dst=/dependency,readonly']
        args.insert(2,'-i');args[args.index('--user')+1]='0:0'
        self.process=subprocess.Popen(args,stdin=subprocess.PIPE,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        for _ in range(100):
            if self.process.poll() is not None:raise Denied('OCI_START_FAILED')
            r=subprocess.run(['docker','inspect','--format','{{.State.Running}}',self.name],capture_output=True,text=True)
            if r.returncode==0 and r.stdout.strip()=='true':break
            time.sleep(.1)
        else:raise Denied('OCI_START_TIMEOUT')
        self._trusted({'op':'init'})
    def verify_dependencies(self):
        if not self.p.go_bundle:return
        try:validate(self.p.go_bundle,self.p.go_manifest,self.p.root,self.p.image)
        except DependencyDenied as e:
            self.failure=str(e);self._save();Publisher(self.p,{}).invalidate();raise LimitExceeded(str(e))
    def _trusted(self,v):
        r=subprocess.run(['docker','exec','-i','--user','0:0',self.name,'python3','-c',VIEW_HELPER],input=json.dumps(v),capture_output=True,text=True,timeout=10)
        if r.returncode:raise Denied('TRUSTED_VIEW_FAILED')
    def close(self):
        sdk=self.p.state_dir/'sdk-container.json'
        if sdk.exists():
            name=json.loads(sdk.read_text())['container']
            if not name.startswith('cdo-sdk-state-') or len(name)!=46:raise Denied('SDK_CLEANUP_STATE_INVALID')
            subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)
            if not container_absent(name):raise Denied('SDK_PRIVATE_STATE_CLEANUP_UNPROVEN')
        if self.process and self.process.stdin:self.process.stdin.close()
        subprocess.run(['docker','rm','-f',self.name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)
        if self.process:
            try:self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:self.process.kill();self.process.wait()
        removed=container_absent(self.name)
        self.events.append({'cleanup_container_removed':removed});self._save()
        if not removed:raise Denied('CLEANUP_FAILED')
    def observe_resources(self):
        code="""import pathlib,json,os
p=pathlib.Path
result={name:p('/sys/fs/cgroup/'+name).read_text().strip() for name in ['cpu.stat','cpu.max','memory.current','memory.max','pids.current','pids.max']}
sizes=[];files=0
for root,dirs,names in os.walk('/tmp',followlinks=False):
 if root=='/tmp':dirs[:]=[d for d in dirs if d!='view']
 for name in names:
  f=p(root,name)
  if f.is_file() and not f.is_symlink():sizes.append(f.stat().st_size);files+=1
result.update(scratch_bytes=sum(sizes),scratch_files=files)
print(json.dumps(result))"""
        r=subprocess.run(['docker','exec','--user','0:0',self.name,'python3','-c',code],capture_output=True,text=True,timeout=10)
        if r.returncode:raise LimitExceeded('RESOURCE_INSPECTION_FAILED')
        return json.loads(r.stdout)
    def call(self,tool,a):
        try:
            if self.failure:raise LimitExceeded(self.failure)
            if time.monotonic()-self.started>self.p.execution_seconds:raise LimitExceeded('EXECUTION_TIMEOUT')
            if not isinstance(a,dict):raise Denied('ARGUMENTS_DENIED')
            if tool=='read_file':
                if set(a)!= {'path'}:raise Denied('ARGUMENTS_DENIED')
                n=relative(a['path'])
                if n in self.proposals:
                    item=self.proposals[n]
                    if item['op']=='delete':raise Denied('MISSING_INPUT')
                    b=base64.b64decode(item['data'])
                else:b=regular(self.snapshot,n).read_bytes()
                if len(b)>self.p.file_bytes:raise LimitExceeded('READ_BYTES_LIMIT')
                return {'content':b.decode('utf8'),'sha256':digest(b)}
            if tool in ('propose_file','propose_delete'):
                n=relative(a.get('path'))
                if n not in self.p.write:raise Denied('PROPOSAL_SCOPE_DENIED')
                if tool=='propose_delete':
                    if set(a)!= {'path'} or n not in self.p.delete:raise Denied('DELETE_DENIED')
                    item={'op':'delete','path':n,'data':''}
                else:
                    if set(a)!= {'path','content'} or not isinstance(a['content'],str):raise Denied('ARGUMENTS_DENIED')
                    b=a['content'].encode();self.proposed_total+=len(b)
                    if len(b)>self.p.file_bytes:raise LimitExceeded('FILE_BYTES_LIMIT')
                    item={'op':'write','path':n,'data':base64.b64encode(b).decode()}
                proposed={**self.proposals,n:item}
                if len(proposed)>self.p.file_count:raise LimitExceeded('FILE_COUNT_LIMIT')
                if self.proposed_total>self.p.aggregate_bytes:raise LimitExceeded('STAGING_BYTES_LIMIT')
                regular(self.p.root,n,missing=True)
                if self.p.go_bundle and n in ('go.mod','go.sum'):
                    raise LimitExceeded('GO_DEPENDENCY_REPREPARATION_REQUIRED')
                self._trusted(item);self.proposals=proposed;self._save()
                return {'accepted':True,'path':n,'sha256':digest(b) if tool=='propose_file' else None}
            if tool=='command':
                if set(a)!= {'argv'} or not isinstance(a['argv'],list) or not a['argv'] or len(a['argv'])>64 or any(not isinstance(x,str) or '\0' in x for x in a['argv']) or sum(len(x) for x in a['argv'])>16384:raise Denied('ARGV_DENIED')
                if self.p.go_bundle:
                    joined=' '.join(a['argv'])
                    if any(k+'=' in joined for k in ['GOMODCACHE','GOPROXY','GOSUMDB','GOFLAGS','GOWORK','GOTOOLCHAIN']):raise Denied('GO_DEPENDENCY_ENV_OVERRIDE_DENIED')
                    if any(k in joined for k in ['-modfile','-overlay','-mod=mod','-mod=vendor']):raise Denied('GO_DEPENDENCY_DECLARATION_OVERRIDE_DENIED')
                self.command_count+=1
                if self.command_count>self.p.commands:raise LimitExceeded('COMMAND_COUNT_LIMIT')
                self.verify_dependencies()
                args=['docker','exec','--user','100:101','--workdir','/tmp/view',self.name,'/usr/bin/env','-i','PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin','HOME=/tmp/agent-home','TMPDIR=/tmp','GOCACHE=/tmp/go-build','GOPATH=/tmp/go','GOMODCACHE=/tmp/go-mod','GOPROXY=off','GO111MODULE=off','GOMAXPROCS=2','GOFLAGS=-buildvcs=false -p=2',*a['argv']]
                if self.p.go_bundle:
                    args=[v for v in args if not v.startswith(('GO111MODULE=','GOMODCACHE=','GOPROXY=','GOFLAGS='))]
                    index=args.index('GOPATH=/tmp/go')+1
                    args[index:index]=['GOMODCACHE=/dependency/modules','GOPROXY=off','GOSUMDB=off','GONOSUMDB=','GONOPROXY=','GOPRIVATE=','GOENV=off','GOWORK=off','GOTOOLCHAIN=local','GOFLAGS=-mod=readonly -buildvcs=false -p=2','GOVCS=*:off']
                before=self.observe_resources()
                p=subprocess.Popen(args,stdout=subprocess.PIPE,stderr=subprocess.STDOUT);sel=selectors.DefaultSelector();sel.register(p.stdout,selectors.EVENT_READ);out=bytearray();end=min(self.started+self.p.execution_seconds,time.monotonic()+self.p.command_seconds)
                try:
                    while sel.get_map():
                        if time.monotonic()>end:raise LimitExceeded('COMMAND_TIMEOUT')
                        for key,_ in sel.select(.1):
                            b=os.read(key.fileobj.fileno(),8192)
                            if not b:sel.unregister(key.fileobj);continue
                            self.output_count+=len(b)
                            if self.output_count>self.p.output_bytes:raise LimitExceeded('COMMAND_OUTPUT_LIMIT')
                            out.extend(b)
                    code=p.wait(timeout=1)
                finally:
                    sel.close()
                    if p.poll() is None:p.kill();p.wait()
                    p.stdout.close()
                self.verify_dependencies()
                after=self.observe_resources()
                self.events.append({'resource_before':before,'resource_after':after,'command_index':self.command_count})
                self._save()
                if after['scratch_bytes']>268435456:raise LimitExceeded('SCRATCH_BYTES_LIMIT')
                resource_code="import pathlib,json,os; p=pathlib.Path; events={n:{k:int(v) for k,v in (line.split() for line in p('/sys/fs/cgroup/'+n).read_text().splitlines())} for n in ['pids.events','memory.events']}; sizes=[]; files=0\nfor root,dirs,names in os.walk('/tmp',followlinks=False):\n if root=='/tmp':dirs[:]=[d for d in dirs if d!='view']\n for n in names:\n  f=p(root,n)\n  if f.is_file() and not f.is_symlink():sizes.append(f.stat().st_size);files+=1\nprint(json.dumps({'events':events,'largest_scratch_file':max(sizes,default=0),'scratch_file_count':files}))"
                try:
                    observed=subprocess.run(['docker','exec','--user','0:0',self.name,'python3','-c',resource_code],capture_output=True,text=True,timeout=10)
                    if observed.returncode:raise LimitExceeded('RESOURCE_INSPECTION_FAILED')
                    resources=json.loads(observed.stdout)
                    if resources['events']['pids.events'].get('max',0)>0:raise LimitExceeded('PID_LIMIT')
                    if any(resources['events']['memory.events'].get(k,0)>0 for k in ['max','oom','oom_kill']):raise LimitExceeded('MEMORY_LIMIT')
                    if resources['largest_scratch_file']>=16777216:raise LimitExceeded('SCRATCH_FILE_LIMIT')
                    if resources['scratch_file_count']>4096:raise LimitExceeded('SCRATCH_FILE_COUNT_LIMIT')
                except (subprocess.TimeoutExpired,ValueError):raise LimitExceeded('RESOURCE_INSPECTION_FAILED')
                self.events.append({'command_index':self.command_count,'exit_code':code,'output_sha256':digest(out),'resources':resources,'argv':a['argv'],'output':out[:8192].decode('utf8',errors='replace')});self._save()
                return {'exit_code':code,'output':out.decode('utf8',errors='replace')}
            raise Denied('TOOL_DENIED')
        except LimitExceeded as e:
            self.failure=str(e);self._save()
            if self.p.go_bundle:Publisher(self.p,{}).invalidate()
            self.close();raise

TOOLS=[('read_file','Read an allowed source file.',{'path':{'type':'string'}}),('propose_file','Propose full UTF-8 contents of an approved file; supports atomic replacement/new file.',{'path':{'type':'string'},'content':{'type':'string'}}),('propose_delete','Propose deletion only if separately allowed.',{'path':{'type':'string'}}),('command','Run argv inside the networkless OCI readonly proposal view.',{'argv':{'type':'array','items':{'type':'string'}}})]

def serve(policy):
    broker=Broker(policy)
    def stop(*_):raise SystemExit(1)
    signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
    try:
        broker.launch()
        while True:
            line=sys.stdin.buffer.readline(policy.aggregate_bytes+16385)
            if not line:break
            if len(line)>policy.aggregate_bytes+16384:raise LimitExceeded('RPC_INPUT_LIMIT')
            req=json.loads(line);method=req.get('method');ident=req.get('id')
            if ident is None:continue
            try:
                if method=='initialize':result={'protocolVersion':'2024-11-05','capabilities':{'tools':{}},'serverInfo':{'name':'cdo-broker','version':'1'}}
                elif method=='tools/list':result={'tools':[{'name':n,'description':d,'annotations':{'readOnlyHint':n=='read_file','destructiveHint':n=='propose_delete','openWorldHint':False},'inputSchema':{'type':'object','properties':fields,'required':list(fields),'additionalProperties':False}} for n,d,fields in TOOLS if n!='propose_delete' or policy.delete]}
                elif method=='tools/call':
                    params=req['params'];value=broker.call(params['name'],params.get('arguments',{}));result={'content':[{'type':'text','text':json.dumps(value)}],'isError':False}
                elif method=='ping':result={}
                else:raise Denied('METHOD_DENIED')
                response={'jsonrpc':'2.0','id':ident,'result':result}
            except (Denied,LimitExceeded,ValueError,OSError) as e:
                if method=='tools/call':response={'jsonrpc':'2.0','id':ident,'result':{'content':[{'type':'text','text':str(e)}],'isError':True}}
                else:response={'jsonrpc':'2.0','id':ident,'error':{'code':-32602,'message':'BROKER_REQUEST_DENIED'}}
            print(json.dumps(response),flush=True)
    except BaseException:
        broker.failure=broker.failure or 'BROKER_ABORTED';broker._save();raise
    finally:broker.close()

def serve_http(policy):
    broker=Broker(policy); lock=threading.Lock(); parent=os.getppid(); orphaned=threading.Event()
    if not policy.token or len(policy.token)<32:raise Denied('HTTP_AUTH_ABSENT')
    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self,*_):pass
        def do_GET(self):self.send_error(405)
        def do_POST(self):
            if self.headers.get('Authorization')!='Bearer '+policy.token:self.send_error(403);return
            try:
                size=int(self.headers.get('Content-Length','0'))
                if size<1 or size>policy.aggregate_bytes+16384:raise LimitExceeded('RPC_INPUT_LIMIT')
                req=json.loads(self.rfile.read(size));method=req.get('method');ident=req.get('id');broker.events.append({'rpc_method':method});broker._save()
                if ident is None:self.send_response(202);self.end_headers();return
                with lock:
                    if method=='initialize':result={'protocolVersion':req.get('params',{}).get('protocolVersion','2024-11-05'),'capabilities':{'tools':{}},'serverInfo':{'name':'cdo-broker','version':'1'}}
                    elif method=='tools/list':result={'tools':[{'name':n,'description':d,'annotations':{'readOnlyHint':n=='read_file','destructiveHint':n=='propose_delete','openWorldHint':False},'inputSchema':{'type':'object','properties':fields,'required':list(fields),'additionalProperties':False}} for n,d,fields in TOOLS if n!='propose_delete' or policy.delete]}
                    elif method=='tools/call':
                        params=req['params']
                        try:value=broker.call(params['name'],params.get('arguments',{}));result={'content':[{'type':'text','text':json.dumps(value)}],'isError':False}
                        except (Denied,LimitExceeded,ValueError,OSError) as e:result={'content':[{'type':'text','text':str(e)}],'isError':True}
                    elif method=='ping':result={}
                    else:raise Denied('METHOD_DENIED')
                body=json.dumps({'jsonrpc':'2.0','id':ident,'result':result}).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
            except (Denied,LimitExceeded,ValueError):self.send_error(400)
    server=http.server.ThreadingHTTPServer(('0.0.0.0' if os.environ.get('CDO_SDK_OCI_TRANSPORT')=='1' else '127.0.0.1',0),Handler)
    def stop(*_):raise SystemExit(1)
    signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
    # Parent death ends the trusted server and closes OCI PID1's only stdin writer.
    def watch_parent():
        while True:
            time.sleep(.1)
            if os.getppid()!=parent:orphaned.set();os.kill(os.getpid(),signal.SIGTERM);return
    threading.Thread(target=watch_parent,daemon=True).start()
    try:
        broker.launch();print(json.dumps({'port':server.server_port}),flush=True);server.serve_forever(poll_interval=.1)
    finally:
        if orphaned.is_set():Publisher(policy,{}).invalidate()
        server.server_close()
        try:broker.close()
        except BaseException:
            if orphaned.is_set():
                audit=policy.evidence.with_name(policy.evidence.name+'.execution.json')
                if audit.exists():
                    record=json.loads(audit.read_text());record['status']='infrastructure_unknown';record['events'].append({'status':'infrastructure_unknown','at':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())})
                    persist_execution_audit(audit,record)
            raise
        if orphaned.is_set():
            audit=policy.evidence.with_name(policy.evidence.name+'.execution.json')
            if audit.exists():
                record=json.loads(audit.read_text())
                if record['status'] not in ('completed','failed','cancelled','timed_out','sandbox_lost','infrastructure_unknown'):
                    record['status']='failed';record['events'].append({'status':'failed','at':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())})
                    persist_execution_audit(audit,record)
            if policy.state_dir.parent.name.startswith('cdo-trusted-broker-'):shutil.rmtree(policy.state_dir.parent)

def lifecycle_checkpoint(phase):
    gate=os.environ.get('CDO_LIFECYCLE_GATE')
    if not gate:return
    if os.environ.get('CDO_SANDBOX_EXECUTION_MODE')!='hardened-certification':raise Denied('LIFECYCLE_GATE_ADMISSION_DENIED')
    target=pathlib.Path(gate);(target/(phase+'.ready')).write_text(str(time.time()))
    if os.environ.get('CDO_LIFECYCLE_PHASE')==phase:
        while not (target/(phase+'.release')).exists():time.sleep(.025)

if __name__=='__main__':
    def cancel_publication(*_):raise InterruptedError('PUBLICATION_CANCELLED')
    signal.signal(signal.SIGTERM,cancel_publication)
    policy=Policy(json.loads(pathlib.Path(sys.argv[2]).read_text()))
    if sys.argv[1]=='admit':print(json.dumps({'admitted':True}))
    elif sys.argv[1]=='serve':serve(policy)
    elif sys.argv[1]=='serve-http':serve_http(policy)
    elif sys.argv[1]=='invalidate':Publisher(policy,{}).invalidate()
    elif sys.argv[1]=='verify':
        broker=Broker(policy)
        try:
            broker.launch();result=broker.call('command',{'argv':['go','test','./...']});policy.evidence.write_text(json.dumps({'verification':result},indent=2))
            if result['exit_code']!=0:raise Denied('INDEPENDENT_GREEN_FAILED')
            print(json.dumps(result))
        finally:broker.close()
    elif sys.argv[1]=='publish':
        # Publisher outlives neither its trusted runner nor a cancelled attempt.
        parent=os.getppid();orphaned=threading.Event();finished=threading.Event()
        def watch_publisher_parent():
            while not finished.wait(.05):
                if os.getppid()!=parent:orphaned.set();os.kill(os.getpid(),signal.SIGTERM);return
        threading.Thread(target=watch_publisher_parent,daemon=True).start()
        try:
            state=json.loads((policy.state_dir/'state.json').read_text())
            if state['failure']:raise Denied('FAILED_EXECUTION_PUBLICATION_DENIED')
            if not container_absent(state['container']):raise Denied('LIVE_EXECUTION_PUBLICATION_DENIED')
            if policy.go_bundle:validate(policy.go_bundle,policy.go_manifest,policy.root,policy.image)
            audit=Publisher(policy,state['original']).publish(state['proposals']);policy.evidence.write_text(json.dumps({'publication':audit,'broker':state},indent=2));print(json.dumps({'published':audit}))
        finally:
            finished.set()
            if orphaned.is_set():
                Publisher(policy,{}).invalidate()
                execution=policy.evidence.with_name(policy.evidence.name+'.execution.json')
                if execution.exists():
                    value=json.loads(execution.read_text());value['status']='failed';value['events'].append({'status':'failed','at':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())})
                    persist_execution_audit(execution,value)
                if policy.state_dir.parent.name.startswith('cdo-trusted-broker-'):shutil.rmtree(policy.state_dir.parent)
    else:raise Denied('MODE_DENIED')
