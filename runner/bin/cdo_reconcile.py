#!/usr/bin/env python3
"""Trusted reconciliation of a retained UNKNOWN execution. Never revives it."""
import json,os,pathlib,re,shutil,subprocess,sys,tempfile,time
from cdo_broker import Denied,container_absent

def reconcile(file):
    file=pathlib.Path(file).resolve(strict=True);audit=json.loads(file.read_text());identity=audit['execution_id']
    if not re.fullmatch('[a-f0-9-]{36}',identity) or audit['status']!='infrastructure_unknown':raise Denied('RECONCILIATION_IDENTITY_DENIED')
    root=pathlib.Path(audit['private_root'])
    if root.parent.resolve()!=pathlib.Path(tempfile.gettempdir()).resolve() or not root.name.startswith('cdo-trusted-broker-') or root.is_symlink():raise Denied('RECONCILIATION_ROOT_DENIED')
    r=subprocess.run(['docker','ps','-a','--filter','label=cdo.execution_id='+identity,'--format','{{.Names}}'],capture_output=True,text=True,timeout=15)
    if r.returncode:raise Denied('DOCKER_CONTAINER_STATE_UNKNOWN')
    removed=[]
    for name in r.stdout.split():
        if not re.fullmatch(r'cdo-(sandbox-broker-[a-f0-9]{16}|sdk-state-[a-f0-9]{32})',name):raise Denied('RECONCILIATION_RESOURCE_DENIED')
        info=subprocess.run(['docker','inspect',name],capture_output=True,text=True,timeout=15)
        if info.returncode:raise Denied('DOCKER_CONTAINER_STATE_UNKNOWN')
        labels=json.loads(info.stdout)[0]['Config']['Labels']
        if labels.get('cdo.execution_id')!=identity or labels.get('cdo.resource') not in ('command','sdk'):raise Denied('RECONCILIATION_LABEL_DENIED')
        subprocess.run(['docker','rm','-f',name],capture_output=True,timeout=15)
        if not container_absent(name):raise Denied('RECONCILIATION_CLEANUP_UNPROVEN')
        removed.append(name)
    # A positive list result is mandatory even if an earlier query saw nothing.
    after=subprocess.run(['docker','ps','-a','--filter','label=cdo.execution_id='+identity,'--format','{{.Names}}'],capture_output=True,text=True,timeout=15)
    if after.returncode or after.stdout.strip():raise Denied('RECONCILIATION_CLEANUP_UNPROVEN')
    if root.exists():shutil.rmtree(root)
    audit['reconciliation']={'at':time.time(),'removed':removed,'private_root_removed':not root.exists(),'resources_absent':True,'old_attempt_invalid':True}
    temporary=file.with_suffix('.reconcile.tmp');temporary.write_text(json.dumps(audit,indent=2));temporary.chmod(0o600)
    fd=os.open(temporary,os.O_RDONLY)
    try:os.fsync(fd)
    finally:os.close(fd)
    os.replace(temporary,file)
    # Preserve infrastructure_unknown; reconciliation is not verification.
    return audit['reconciliation']
if __name__=='__main__':print(json.dumps(reconcile(sys.argv[1])))
