#!/usr/bin/env python3
"""Trusted SDK CLI adapter. Only private tmpfs is mutable; never mount source.
The filesystem enforces capacity. Sampling only latches failures and evidence.
"""
import hashlib, json, os, pathlib, signal, subprocess, sys, threading, time, uuid
MAX_BYTES=8*1024*1024
MAX_FILES=256
ROOT='/execution/sdk-private'
PREFIX='CDO_SDK_STATE_METRICS='

def supervise(args):
    root=pathlib.Path(ROOT)
    for name in ('home','tmp','control','cache','data','config','runtime'):(root/name).mkdir(mode=0o700)
    (root/'home/environments.toml').write_text('include_local = false\n')
    env={'PATH':'/usr/local/bin:/usr/bin:/bin','HOME':str(root/'home'),'CODEX_HOME':str(root/'home'),'TMPDIR':str(root/'tmp'),'TMP':str(root/'tmp'),'TEMP':str(root/'tmp'),'XDG_CACHE_HOME':str(root/'cache'),'XDG_DATA_HOME':str(root/'data'),'XDG_CONFIG_HOME':str(root/'config'),'XDG_RUNTIME_DIR':str(root/'runtime')}
    # Authentication is an immutable reference, outside mutable SDK state.
    if pathlib.Path('/reference/auth.json').exists():(root/'home/auth.json').symlink_to('/reference/auth.json')
    if os.environ.get('CODEX_API_KEY'):env['CODEX_API_KEY']=os.environ['CODEX_API_KEY']
    mounts=[];unknown=[]
    for line in pathlib.Path('/proc/self/mountinfo').read_text().splitlines():
        fields=line.split();mount=fields[4];options=fields[5].split(',');fstype=fields[fields.index('-')+1]
        if 'rw' in options:
            category=('PRIVATE_EXECUTION_STATE' if mount==ROOT else 'TRUSTED_RUNNER_STATE' if fstype in ('proc','devpts','mqueue','sysfs') or mount.startswith('/proc/') or mount=='/dev' else 'UNKNOWN')
            mounts.append({'path':mount,'type':fstype,'classification':category})
            if category=='UNKNOWN':unknown.append(mount)
    if unknown:raise RuntimeError('SDK_PRIVATE_STATE_UNKNOWN_WRITABLE_MOUNT')
    records={};peak={'bytes':0,'files':0};failure=None
    def observe():
        nonlocal failure
        st=os.statvfs(ROOT);used=(st.f_blocks-st.f_bfree)*st.f_frsize;inodes=st.f_files-st.f_ffree
        peak['bytes']=max(peak['bytes'],used);peak['files']=max(peak['files'],inodes)
        if st.f_ffree==0:failure='SDK_PRIVATE_STATE_FILES_EXCEEDED'
        elif st.f_bavail==0:failure='SDK_PRIVATE_STATE_BYTES_EXCEEDED'
        for p in root.rglob('*'):
            try:
                if len(records)>=256 and str(p.relative_to(root)) not in records:continue
                s=p.lstat();records[str(p.relative_to(root))]={'classification':'PRIVATE_EXECUTION_STATE','size':s.st_size,'symlink':p.is_symlink()}
            except FileNotFoundError:pass
        return {'bytes':used,'files':inodes}
    initial=observe()
    def report(phase,final=None):
        metrics={'initial':initial,'peak':dict(peak),'final':final,'max_bytes':MAX_BYTES,'max_files':MAX_FILES,'hard_quota':'tmpfs size/nr_inodes','paths':dict(records),'failure':failure,'writable_mounts':mounts,'unknown_writable_paths':unknown,'phase':phase}
        print(PREFIX+json.dumps(metrics),file=sys.stderr,flush=True)
    report('RUNNING')
    cli=subprocess.Popen(['/app/runner/node_modules/.bin/codex',*args],env=env,stderr=subprocess.PIPE)
    # Untrusted CLI diagnostics cannot forge the trusted metrics channel.
    stderr_hash=hashlib.sha256();stderr_bytes=0
    def drain():
        nonlocal stderr_bytes
        while True:
            chunk=cli.stderr.read(4096)
            if not chunk:return
            stderr_hash.update(chunk);stderr_bytes+=len(chunk)
    drainer=threading.Thread(target=drain,daemon=True);drainer.start()
    signal.signal(signal.SIGTERM,lambda *_:cli.terminate())
    signal.signal(signal.SIGINT,lambda *_:cli.terminate())
    deadline=time.monotonic()+1200;next_report=0
    while cli.poll() is None:
        if time.monotonic()>deadline:failure='SDK_PRIVATE_STATE_EXECUTION_TIMEOUT'
        observe()
        if time.monotonic()>next_report:report('RUNNING');next_report=time.monotonic()+.1
        if failure:report('RUNNING');cli.kill()
        time.sleep(.01)
    final=observe()
    drainer.join(timeout=1)
    metrics={'initial':initial,'peak':peak,'final':final,'max_bytes':MAX_BYTES,'max_files':MAX_FILES,'hard_quota':'tmpfs size/nr_inodes','paths':records,'failure':failure,'writable_mounts':mounts,'unknown_writable_paths':unknown,'phase':'FINAL','cli_stderr_bytes':stderr_bytes,'cli_stderr_sha256':stderr_hash.hexdigest()}
    print(PREFIX+json.dumps(metrics),file=sys.stderr,flush=True)
    return 75 if failure else cli.returncode

def oci_command(name,image,mounts):
    return ['docker','run','--name',name,'--log-driver','none','-i','--read-only','--user','100:101','--cap-drop','ALL','--security-opt','no-new-privileges','--pids-limit','128','--memory','512m','--memory-swap','512m','--cpus','2','--ulimit','nofile=256:256','--tmpfs',ROOT+':rw,noexec,nosuid,nodev,size='+str(MAX_BYTES)+',nr_inodes='+str(MAX_FILES)+',uid=100,gid=101,mode=0700','--tmpfs','/tmp:ro,noexec,nosuid,nodev,size=4096','--tmpfs','/dev/shm:ro,noexec,nosuid,nodev,size=4096','--mount','type=bind,src='+str(pathlib.Path(__file__).resolve())+',dst=/reference/supervisor.py,readonly',*mounts]

def adapter(args):
    state=pathlib.Path(os.environ['CDO_SDK_STATE_CONTROL']);name='cdo-sdk-state-'+uuid.uuid4().hex
    image=os.environ['CDO_SDK_STATE_IMAGE'];metrics=state/'sdk-metrics.json'
    def save_summary(value):
        temporary=metrics.with_suffix('.tmp');temporary.write_text(json.dumps(value));temporary.chmod(0o600);os.replace(temporary,metrics)
    translated=list(args);mounts=[]
    for flag in ('--cd','--output-schema'):
        if flag in translated:
            i=translated.index(flag)+1
            if flag=='--cd':translated[i]=ROOT+'/control'
            else:
                schema=pathlib.Path(translated[i]).resolve(strict=True)
                if not schema.is_file() or schema.stat().st_size>5*1024*1024:raise RuntimeError('RESULT_SCHEMA_INPUT_DENIED')
                mounts+=['--mount','type=bind,src='+str(schema)+',dst=/reference/schema.json,readonly'];translated[i]='/reference/schema.json'
    # CLI config is trusted; remap only loopback endpoints to Docker Desktop host.
    translated=[a.replace('http://127.0.0.1:', 'http://host.docker.internal:') for a in translated]
    command=oci_command(name,image,mounts)
    if os.environ.get("CDO_EXECUTION_ID"):command[2:2]=["--label","cdo.execution_id="+os.environ["CDO_EXECUTION_ID"],"--label","cdo.resource=sdk"]
    credential=os.environ.get('CDO_SDK_AUTH_REFERENCE')
    if credential:command+=['--mount','type=bind,src='+str(pathlib.Path(credential).resolve(strict=True))+',dst=/reference/auth.json,readonly']
    if os.environ.get('CODEX_API_KEY'):command+=['--env','CODEX_API_KEY']
    command+=['--entrypoint','python3',image,'/reference/supervisor.py','--supervise',*translated]
    (state/'sdk-container.json').write_text(json.dumps({'container':name}))
    child=None;chunks=[];summary=None;parent=os.getppid();done=threading.Event()
    def stop(*_):
        if child and child.poll() is None:child.terminate()
    signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
    def watch():
        while not done.wait(.1):
            if os.getppid()!=parent:stop();return
    threading.Thread(target=watch,daemon=True).start()
    try:
        child=subprocess.Popen(command,stderr=subprocess.PIPE)
        for line in child.stderr:
            if line.startswith(PREFIX.encode()):
                summary=json.loads(line[len(PREFIX):]);save_summary(summary)
            elif sum(map(len,chunks))<65536:chunks.append(line)
        code=child.wait(timeout=15)
        if not summary or summary.get('phase')!='FINAL':raise RuntimeError('SDK_PRIVATE_STATE_EVIDENCE_UNAVAILABLE')
        if summary['failure']:print(summary['failure'],file=sys.stderr)
        elif code:sys.stderr.buffer.write(b''.join(chunks))
        return code
    finally:
        done.set()
        subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)
        check=subprocess.run(['docker','inspect',name],capture_output=True,text=True,timeout=15)
        removed=check.returncode!=0 and ('no such object:' in check.stderr.lower() or 'no such container:' in check.stderr.lower())
        if summary:summary['cleanup']=removed;save_summary(summary)
        if not removed:raise RuntimeError('SDK_PRIVATE_STATE_CLEANUP_UNPROVEN')

if __name__=='__main__':
    try:sys.exit(supervise(sys.argv[2:]) if sys.argv[1:2]==['--supervise'] else adapter(sys.argv[1:]))
    except Exception as e:print('SDK_PRIVATE_STATE_UNAVAILABLE' if isinstance(e,OSError) else str(e),file=sys.stderr);sys.exit(1)
