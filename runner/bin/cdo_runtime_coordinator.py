"""Trusted coordinator guards. These paths never enter the SDK environment."""
import json
import os
import pathlib
import stat
import sys
import time
import signal
import subprocess
import threading
import errno
import selectors
import hashlib
import uuid

COORDINATOR_CLEANUP_WAIT_SECONDS = 15
REGISTRATION_WAIT_SECONDS = 3
OWNER_FILE = 'host-process-owner.json'
ACK_FILE = 'host-process-admission.json'
TEARDOWN_FILE = 'host-teardown.json'
NODE_OUTCOME_FILE = 'host-node-outcome.json'
NODE_TERMINAL_CAUSES = frozenset({'AGENT_PROCESS_FAILED','SDK_PRIVATE_STATE_BYTES_EXCEEDED','SDK_PRIVATE_STATE_FILES_EXCEEDED','SANDBOX_LOST','BROKER_INTERRUPTED','BROKER_PROCESS_LOST','BROKER_EXITED','EXECUTION_LEASE_EXPIRED','EXECUTION_LEASE_EXPIRED_OR_INVALIDATED','EXECUTION_CANCELLED','COMMAND_POLICY_DENIED','PROPOSAL_SCOPE_DENIED','REVIEWER_WRITE_DENIED','PUBLICATION_SCOPE_DENIED'})

ENVIRONMENT_KEYS = frozenset({
    'CDO_SANDBOX_EXECUTION_MODE','CDO_CERTIFICATION_ROOT','CDO_CERTIFICATION_EVIDENCE',
    'CDO_CERTIFICATION_PROVIDER_URL','CDO_BROKER_IMAGE','CDO_SDK_STATE_IMAGE',
    'CDO_GO_DEPENDENCY_BUNDLE','CDO_GO_DEPENDENCY_MANIFEST_SHA256','CDO_BROKER_PYTHON',
    'CDO_RUNTIME_APPROVED_COMMANDS_JSON','CDO_RUNTIME_NODE_EXECUTABLE',
    'CDO_RUNTIME_NODE_ENTRYPOINT','CDO_DOCKER_EXECUTABLE','CDO_DOCKER_HOST',
    'CDO_LIFECYCLE_GATE','CDO_LIFECYCLE_PHASE',
    'CDO_RUNTIME_LEASE_FILE','CDO_RUNTIME_OWNED_CONFIG','CDO_RUNTIME_CONTROL_ROOT',
    'CDO_RUNTIME_PRIVATE_ROOT',
    'CODEX_SOL_MAX_RUNS_5H','CODEX_BUDGET_MODE',
})


class CoordinatorDenied(Exception):
    def __init__(self,code,detail=None):
        super().__init__(code);self.detail=detail


def load_json(raw):
    def pairs(items):
        result={}
        for key,value in items:
            if key in result:raise CoordinatorDenied('COORDINATOR_DUPLICATE_KEY')
            result[key]=value
        return result
    return json.loads(raw,object_pairs_hook=pairs,parse_constant=lambda _:(_ for _ in ()).throw(CoordinatorDenied('COORDINATOR_NONFINITE_JSON')))


def assert_lease(file, execution_id, attempt):
    """Read the controller-owned lease without following links or accepting drift."""
    if not isinstance(file, str) or not file:
        raise CoordinatorDenied('EXECUTION_LEASE_ABSENT')
    target = pathlib.Path(file)
    try:
        if not target.is_absolute() or target.resolve(strict=True) != target:
            raise CoordinatorDenied('EXECUTION_LEASE_INVALID')
        fd = os.open(target, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        try:
            st = os.fstat(fd)
            if not stat.S_ISREG(st.st_mode) or st.st_nlink != 1 or st.st_uid != os.geteuid() or stat.S_IMODE(st.st_mode) != 0o600 or st.st_size > 4096:
                raise CoordinatorDenied('EXECUTION_LEASE_INVALID')
            raw = os.read(fd, 4097)
        finally:
            os.close(fd)
        lease = load_json(raw)
        if not isinstance(lease, dict) or set(lease) != {'execution_id', 'attempt', 'expires_at', 'status'}:
            raise CoordinatorDenied('EXECUTION_LEASE_INVALID')
        if lease['execution_id'] != execution_id or type(lease['attempt']) is not int or lease['attempt'] != attempt or attempt < 1:
            raise CoordinatorDenied('EXECUTION_LEASE_IDENTITY_CHANGED')
        expires = lease['expires_at']
        if type(expires) not in (int, float) or not __import__('math').isfinite(expires):
            raise CoordinatorDenied('EXECUTION_LEASE_INVALID')
        if lease['status'] != 'ACTIVE' or expires <= time.time():
            raise CoordinatorDenied('EXECUTION_LEASE_EXPIRED_OR_INVALIDATED')
        return lease
    except (OSError, ValueError, TypeError, RecursionError) as error:
        raise CoordinatorDenied('EXECUTION_LEASE_INVALID') from error


def environment_lease(execution_id, attempt=1):
    file = os.environ.get('CDO_RUNTIME_LEASE_FILE')
    # Historical certification probes use the original trusted local boundary.
    # The complete transport cannot enter that path without a lease.
    if file or os.environ.get('CDO_RUNTIME_OWNED_CONFIG'):
        return assert_lease(file, execution_id, attempt)
    return None


def private_json(file, maximum):
    target=pathlib.Path(file)
    try:
        if not target.is_absolute() or target.resolve(strict=True)!=target:raise CoordinatorDenied('COORDINATOR_CONFIG_INVALID')
        fd=os.open(target,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
        try:
            st=os.fstat(fd)
            if not stat.S_ISREG(st.st_mode) or st.st_nlink!=1 or st.st_uid!=os.geteuid() or stat.S_IMODE(st.st_mode)!=0o600 or st.st_size>maximum:raise CoordinatorDenied('COORDINATOR_CONFIG_INVALID')
            value=load_json(os.read(fd,maximum+1))
        finally:os.close(fd)
        if not isinstance(value,dict):raise CoordinatorDenied('COORDINATOR_CONFIG_INVALID')
        return value
    except (OSError,ValueError,TypeError,RecursionError) as error:raise CoordinatorDenied('COORDINATOR_CONFIG_INVALID') from error


def canonical(value):
    return json.dumps(value,sort_keys=True,separators=(',',':'),allow_nan=False).encode()


def atomic_private(file,value):
    """The ledger and receipt survive process interruption, outside work storage."""
    temporary=file.with_name(file.name+'.tmp-'+uuid.uuid4().hex)
    fd=os.open(temporary,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
    try:
        data=canonical(value)
        if len(data)>65536:raise CoordinatorDenied('COORDINATOR_PROOF_LIMIT')
        with os.fdopen(fd,'wb') as stream:
            fd=None;stream.write(data);stream.flush();os.fsync(stream.fileno())
        os.replace(temporary,file)
        directory=os.open(file.parent,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
        try:os.fsync(directory)
        finally:os.close(directory)
    finally:
        if fd is not None:os.close(fd)
        if os.path.lexists(temporary):os.unlink(temporary)


def job_binding(file):
    job=private_json(str(file),1048576)
    if set(job)!={'version','request','environment','lease_file','owned_config','execution_root','process_owner'} or job['version']!=1:raise CoordinatorDenied('COORDINATOR_JOB_INVALID')
    owner=job['process_owner']
    if not isinstance(owner,dict) or set(owner)!={'version','controller_id','boot_id','owner_nonce'} or owner['version']!=1:raise CoordinatorDenied('COORDINATOR_OWNER_INVALID')
    if any(not isinstance(owner.get(key),str) for key in ('controller_id','boot_id','owner_nonce')):raise CoordinatorDenied('COORDINATOR_OWNER_INVALID')
    if len(owner['controller_id'])!=64 or any(c not in '0123456789abcdef' for c in owner['controller_id']) or len(owner['owner_nonce'])!=32 or any(c not in '0123456789abcdef' for c in owner['owner_nonce']):raise CoordinatorDenied('COORDINATOR_OWNER_INVALID')
    try:
        if str(uuid.UUID(owner['boot_id']))!=owner['boot_id']:raise ValueError()
    except ValueError:raise CoordinatorDenied('COORDINATOR_OWNER_INVALID') from None
    root=pathlib.Path(job['execution_root'])
    if not root.is_absolute() or root.resolve(strict=True)!=root or not root.is_dir() or root.stat().st_uid!=os.geteuid() or stat.S_IMODE(root.stat().st_mode)!=0o700:raise CoordinatorDenied('COORDINATOR_CONTROL_ROOT_INVALID')
    if pathlib.Path(file).parent!=root or pathlib.Path(job['owned_config']).parent!=root or pathlib.Path(job['lease_file']).parent!=root:raise CoordinatorDenied('COORDINATOR_AUTHORITY_BINDING_CHANGED')
    owned=private_json(job['owned_config'],65536)
    request=job['request']
    if not isinstance(request,dict) or type(request.get('attempt')) is not int or request['attempt']<1 or not isinstance(request.get('execution_id'),str):raise CoordinatorDenied('COORDINATOR_JOB_INVALID')
    binding={'execution_id':request['execution_id'],'attempt':request['attempt'],'runtime_context':request.get('runtime_context'),
             'job_sha256':hashlib.sha256(canonical(job)).hexdigest(),'owned_config_sha256':hashlib.sha256(canonical(owned)).hexdigest(),'process_owner':owner}
    return job,root,binding


def private_work_root(root,owner):
    return root/('private-execution-'+owner['owner_nonce'])


def check_expected(binding,expected):
    fields={'job_sha256','owned_config_sha256','process_owner','coordinator_pid','coordinator_reaped'}
    if not isinstance(expected,dict) or set(expected) not in (fields,fields|{'never_started'}) or any(expected[key]!=binding[key] for key in ('job_sha256','owned_config_sha256','process_owner')):raise CoordinatorDenied('COORDINATOR_PROOF_BINDING_CHANGED')
    if type(expected['coordinator_reaped']) is not bool:raise CoordinatorDenied('COORDINATOR_PROOF_INVALID')
    if expected.get('never_started') is True:
        if expected['coordinator_pid'] is not None or expected['coordinator_reaped']:raise CoordinatorDenied('COORDINATOR_PROOF_INVALID')
    elif type(expected['coordinator_pid']) is not int or expected['coordinator_pid']<1:raise CoordinatorDenied('COORDINATOR_PROOF_INVALID')


def read_owner(root,binding):
    value=private_json(str(root/OWNER_FILE),65536)
    if set(value)!={'version','binding','coordinator','node','phase','no_unknown'} or value['version']!=1 or value['binding']!=binding or type(value['no_unknown']) is not bool:raise CoordinatorDenied('COORDINATOR_PROOF_BINDING_CHANGED')
    identity=value['coordinator']
    if not isinstance(identity,dict) or set(identity)!={'pid','pgid','sid','uid'} or any(type(v) is not int or v<0 for v in identity.values()) or identity['pid']<1 or identity['pgid']!=identity['pid'] or identity['sid']!=identity['pid'] or identity['uid']!=os.geteuid():raise CoordinatorDenied('COORDINATOR_PROOF_IDENTITY_CHANGED')
    return value


def coordinator_identity():
    identity={'pid':os.getpid(),'pgid':os.getpgrp(),'sid':os.getsid(0),'uid':os.geteuid()}
    if identity['pid']!=identity['pgid'] or identity['pid']!=identity['sid']:raise CoordinatorDenied('COORDINATOR_PROCESS_IDENTITY_CHANGED')
    return identity


def node_terminal_cause(root,binding,node,exit_code):
    """Typed diagnosis only; this journal cannot establish cleanup or success."""
    if not os.path.lexists(root/NODE_OUTCOME_FILE):return None if exit_code==0 else 'AGENT_PROCESS_FAILED'
    outcome=private_json(str(root/NODE_OUTCOME_FILE),65536)
    if set(outcome)!={'version','binding','node','terminal_cause','node_exit_code'} or outcome['version']!=1 or outcome['binding']!=binding or outcome['node']!=node or type(outcome['node_exit_code']) is not int or outcome['node_exit_code']!=1 or exit_code!=1 or outcome['terminal_cause'] not in NODE_TERMINAL_CAUSES:raise CoordinatorDenied('HOST_NODE_OUTCOME_UNKNOWN')
    return outcome['terminal_cause']


def await_coordinator_registration(file,process,timeout=REGISTRATION_WAIT_SECONDS):
    """A live Popen handle, rather than a saved PID, admits the producer once."""
    _,root,binding=job_binding(file)
    if type(process.pid) is not int or process.pid<1 or not 0<timeout<=REGISTRATION_WAIT_SECONDS:raise CoordinatorDenied('COORDINATOR_REGISTRATION_INVALID')
    end=time.monotonic()+timeout
    while not os.path.lexists(root/OWNER_FILE):
        if time.monotonic()>=end:raise CoordinatorDenied('COORDINATOR_REGISTRATION_TIMEOUT')
        time.sleep(.025)
    owner=read_owner(root,binding)
    if owner['coordinator']['pid']!=process.pid or owner['phase']!='REGISTERED' or owner['node'] is not None or owner['no_unknown'] is not True:raise CoordinatorDenied('COORDINATOR_REGISTRATION_IDENTITY_CHANGED')
    # Caller still holds the exact child unreaped; never poll/wait it here.
    acknowledgement={'version':1,'binding':binding,'coordinator':owner['coordinator'],'controller_pid':os.getpid()}
    if os.path.lexists(root/ACK_FILE):raise CoordinatorDenied('COORDINATOR_ADMISSION_REPLAY')
    atomic_private(root/ACK_FILE,acknowledgement)
    return {'job_sha256':binding['job_sha256'],'owned_config_sha256':binding['owned_config_sha256'],'process_owner':binding['process_owner'],'coordinator_pid':process.pid,'coordinator_reaped':False}


def await_registration_ack(root,binding,identity,lease_file):
    end=time.monotonic()+REGISTRATION_WAIT_SECONDS
    while not os.path.lexists(root/ACK_FILE):
        assert_lease(lease_file,binding['execution_id'],binding['attempt'])
        if time.monotonic()>=end:raise CoordinatorDenied('COORDINATOR_REGISTRATION_TIMEOUT')
        time.sleep(.025)
    ack=private_json(str(root/ACK_FILE),65536)
    if ack!={'version':1,'binding':binding,'coordinator':identity,'controller_pid':os.getppid()}:raise CoordinatorDenied('COORDINATOR_ADMISSION_IDENTITY_CHANGED')


def validate_teardown(file,expected_owner):
    _,root,binding=job_binding(file);check_expected(binding,expected_owner)
    owner=read_owner(root,binding)
    proof=private_json(str(root/TEARDOWN_FILE),65536)
    if set(proof)!={'version','binding','coordinator','node','node_group_absent','node_reaped','no_unknown','node_exit_code','terminal_cause','proof_kind'} or proof['version']!=1 or proof['binding']!=binding or proof['coordinator']!=owner['coordinator'] or proof['coordinator']['pid']!=expected_owner['coordinator_pid'] or proof['node']!=owner['node'] or owner['phase']!='TEARDOWN' or owner['no_unknown'] is not True or any(proof[key] is not True for key in ('node_group_absent','no_unknown')):raise CoordinatorDenied('COORDINATOR_TEARDOWN_UNPROVEN')
    if (proof['proof_kind'],proof['node_reaped']) not in {('anchored-teardown',True),('observed-recovery',False)}:raise CoordinatorDenied('COORDINATOR_TEARDOWN_UNPROVEN')
    if proof['node_exit_code'] is not None and type(proof['node_exit_code']) is not int:raise CoordinatorDenied('COORDINATOR_TEARDOWN_UNPROVEN')
    if proof['terminal_cause'] is not None and (not isinstance(proof['terminal_cause'],str) or len(proof['terminal_cause'])>128 or any(c not in 'ABCDEFGHIJKLMNOPQRSTUVWXYZ_0123456789' for c in proof['terminal_cause'])):raise CoordinatorDenied('COORDINATOR_TEARDOWN_UNPROVEN')
    node=proof['node']
    if not isinstance(node,dict) or set(node)!={'pid','pgid','sid','uid'} or any(type(v) is not int or v<0 for v in node.values()) or node['pid']<1 or node['pgid']!=node['pid'] or node['sid']!=node['pid'] or node['uid']!=os.geteuid():raise CoordinatorDenied('COORDINATOR_TEARDOWN_UNPROVEN')
    members=group_members(None)
    if any(member['pid']==node['pid'] or member['pgid']==node['pgid'] for member in members):raise CoordinatorDenied('COORDINATOR_NODE_GROUP_NOT_ABSENT')
    return proof


def remove_private_work(root,work):
    """Delete only the fixed owned directory using fd-relative, no-follow calls."""
    parent=os.open(root,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
    budget=[4096];deadline=time.monotonic()+2
    def walk(directory,depth=0):
        if depth>64:raise CoordinatorDenied('COORDINATOR_PRIVATE_STORAGE_UNKNOWN')
        for name in os.listdir(directory):
            budget[0]-=1
            if budget[0]<0 or time.monotonic()>deadline:raise CoordinatorDenied('COORDINATOR_PRIVATE_STORAGE_UNKNOWN')
            info=os.stat(name,dir_fd=directory,follow_symlinks=False)
            if info.st_uid!=os.geteuid():raise CoordinatorDenied('COORDINATOR_PRIVATE_STORAGE_UNKNOWN')
            if stat.S_ISDIR(info.st_mode):
                child=os.open(name,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=directory)
                try:walk(child,depth+1)
                finally:os.close(child)
                os.rmdir(name,dir_fd=directory)
            elif stat.S_ISREG(info.st_mode) or stat.S_ISLNK(info.st_mode):os.unlink(name,dir_fd=directory)
            else:raise CoordinatorDenied('COORDINATOR_PRIVATE_STORAGE_UNKNOWN')
    try:
        try:info=os.stat(work.name,dir_fd=parent,follow_symlinks=False)
        except FileNotFoundError:return
        if not stat.S_ISDIR(info.st_mode) or info.st_uid!=os.geteuid() or stat.S_IMODE(info.st_mode)!=0o700:raise CoordinatorDenied('COORDINATOR_PRIVATE_STORAGE_UNKNOWN')
        directory=os.open(work.name,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=parent)
        try:walk(directory)
        finally:os.close(directory)
        os.rmdir(work.name,dir_fd=parent);os.fsync(parent)
        try:os.stat(work.name,dir_fd=parent,follow_symlinks=False)
        except FileNotFoundError:return
        raise CoordinatorDenied('COORDINATOR_PRIVATE_STORAGE_UNKNOWN')
    except OSError:raise CoordinatorDenied('COORDINATOR_PRIVATE_STORAGE_UNKNOWN') from None
    finally:os.close(parent)


def reconcile_cleanup(file,expected_binding,oci_absent=False):
    """Gateway calls only after its exact child reaping and positive OCI cleanup."""
    _,root,binding=job_binding(file);check_expected(binding,expected_binding)
    work=private_work_root(root,binding['process_owner'])
    if expected_binding.get('never_started') is True:
        # This branch requires the gateway's MACed never-launched state. File
        # absence by itself can never establish that a producer did not launch.
        if any(os.path.lexists(root/name) for name in (OWNER_FILE,ACK_FILE,TEARDOWN_FILE,NODE_OUTCOME_FILE)) or os.path.lexists(work):raise CoordinatorDenied('COORDINATOR_NEVER_STARTED_PROOF_CHANGED')
    else:
        if oci_absent is not True:raise CoordinatorDenied('COORDINATOR_OCI_CLEANUP_UNPROVEN')
        owner=read_owner(root,binding)
        if owner['coordinator']['pid']!=expected_binding['coordinator_pid'] or owner['no_unknown'] is not True:raise CoordinatorDenied('COORDINATOR_PROOF_IDENTITY_CHANGED')
        if not os.path.lexists(root/TEARDOWN_FILE):
            # Crash recovery observes only. Without the exact durable Node
            # registration a launch race cannot be converted into absence.
            node=owner['node']
            if owner['phase'] not in {'NODE_RUNNING','NODE_REAPED','TEARDOWN'} or not isinstance(node,dict) or set(node)!={'pid','pgid','sid','uid'} or type(node['pid']) is not int or node['pid']<1 or node['pgid']!=node['pid'] or node['sid']!=node['pid'] or node['uid']!=os.geteuid() or oci_absent is not True:raise CoordinatorDenied('COORDINATOR_TEARDOWN_UNPROVEN')
            members=group_members(None)
            if any(member['pid'] in {node['pid'],owner['coordinator']['pid']} or member['pgid'] in {node['pgid'],owner['coordinator']['pgid']} for member in members):raise CoordinatorDenied('COORDINATOR_EXIT_UNPROVEN')
            owner['phase']='TEARDOWN';atomic_private(root/OWNER_FILE,owner)
            atomic_private(root/TEARDOWN_FILE,{'version':1,'binding':binding,'coordinator':owner['coordinator'],'node':node,'node_group_absent':True,'node_reaped':False,'no_unknown':True,'node_exit_code':None,'terminal_cause':'COORDINATOR_CRASH_RECOVERED','proof_kind':'observed-recovery'})
        proof=validate_teardown(file,expected_binding)
        members=group_members(None)
        if any(member['pid']==proof['coordinator']['pid'] or member['pgid']==proof['coordinator']['pgid'] for member in members):raise CoordinatorDenied('COORDINATOR_GROUP_NOT_ABSENT')
        remove_private_work(root,work)
        if os.path.lexists(work):raise CoordinatorDenied('COORDINATOR_PRIVATE_STORAGE_UNKNOWN')
    return {'coordinator_absent':True,'node_groups_absent':True,'private_storage_absent':True,'no_unknown':True}


def wait_unreaped(pid):
    """Keep the child PID reserved until its entire owned group is terminated."""
    if hasattr(os,'waitid') and hasattr(os,'WNOWAIT'):
        value=os.waitid(os.P_PID,pid,os.WEXITED|os.WNOWAIT)
        return {'pid':value.si_pid,'uid':value.si_uid,'code':value.si_code,'status':value.si_status,
                'uid_available':sys.platform!='darwin','backend':'os-waitid','wnowait':True}
    if sys.platform!='darwin':raise CoordinatorDenied('COORDINATOR_PROCESS_OBSERVATION_UNAVAILABLE')
    # Darwin libc exports POSIX waitid although Python does not expose it. Values
    # are from Apple's sys/wait.h: P_PID=1, WEXITED=4, WNOWAIT=0x20. No arbitrary
    # native library, symbol, PID or option is accepted from the model or client.
    import ctypes
    try:
        libc=ctypes.CDLL('/usr/lib/libSystem.B.dylib',use_errno=True)
        call=libc.waitid;call.argtypes=[ctypes.c_int,ctypes.c_uint,ctypes.c_void_p,ctypes.c_int];call.restype=ctypes.c_int
        info=ctypes.create_string_buffer(1024)
        while call(1,pid,ctypes.byref(info),4|0x20)!=0:
            code=ctypes.get_errno()
            if code==errno.EINTR:continue
            raise CoordinatorDenied('COORDINATOR_PROCESS_OBSERVATION_UNKNOWN',{'operation':'waitid','backend':'darwin-libSystem','observer_errno':code,'reason':'WAITID_SYSCALL_FAILED','wnowait':True})
        # Apple's siginfo_t starts with signo, errno, code, pid, uid, status.
        values=(ctypes.c_int*6).from_buffer(info)
        # XNU waitid zeroes siginfo then fills PID/code/status, not si_uid.
        # Keep raw UID for diagnosis; it is not child ownership evidence.
        return {'pid':values[3],'uid':ctypes.c_uint.from_buffer(info,16).value,'code':values[2],'status':values[5],
                'uid_available':False,'backend':'darwin-libSystem-waitid','wnowait':True}
    except (OSError,AttributeError) as error:
        raise CoordinatorDenied('COORDINATOR_PROCESS_OBSERVATION_UNAVAILABLE',{'operation':'waitid-load','backend':'darwin-libSystem','observer_errno':getattr(error,'errno',None),'original_error_type':type(error).__name__,'reason':'WAITID_BINDING_UNAVAILABLE','wnowait':True}) from error


def parse_process_snapshot(raw,observer_pid,caller_pid,uid,*,observer_uid=None,caller_parent_pid=None):
    """A successful but empty/truncated observation is never absence proof."""
    result=[];seen=set()
    def unknown(reason,**detail):
        raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'reason':reason,**detail})
    if observer_uid is None:observer_uid=uid
    if any(type(pid) is not int or pid<1 for pid in (observer_pid,caller_pid)) or observer_pid==caller_pid or any(type(value) is not int or value<0 for value in (uid,observer_uid)) or (caller_parent_pid is not None and (type(caller_parent_pid) is not int or caller_parent_pid<0)):
        unknown('ANCHOR_IDENTITY_INVALID')
    try:
        lines=raw.decode('ascii').splitlines()
    except UnicodeError:unknown('SNAPSHOT_NON_ASCII')
    if not lines:unknown('SNAPSHOT_EMPTY')
    for index,line in enumerate(lines,1):
        fields=line.split()
        if len(fields)!=5:unknown('RECORD_FIELD_COUNT_INVALID',record_index=index,field_count=len(fields))
        if any(not part.isdigit() for part in fields[:4]):unknown('RECORD_NUMERIC_FIELDS_INVALID',record_index=index)
        try:pid,ppid,group,member_uid=map(int,fields[:4])
        except ValueError:unknown('RECORD_NUMERIC_FIELDS_INVALID',record_index=index)
        # Darwin includes the kernel's process/group zero in KERN_PROC_ALL.
        # These are observation records only: owned identity validation and
        # group selection still require positive process/group IDs.
        kernel_zero=sys.platform=='darwin' and (pid,ppid,group,member_uid)==(0,0,0,0)
        if (pid==0 and not kernel_zero) or (group==0 and sys.platform!='darwin'):
            unknown('RECORD_IDENTITY_RANGE_INVALID',record_index=index,pid_zero=pid==0,pgid_zero=group==0)
        if pid in seen:unknown('RECORD_DUPLICATE_PID',record_index=index)
        if not fields[4] or any(c not in 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz+<>Ns' for c in fields[4]):
            unknown('RECORD_STATE_INVALID',record_index=index)
        seen.add(pid);result.append({'pid':pid,'ppid':ppid,'pgid':group,'uid':member_uid,'state':fields[4]})
    anchors={label:next((member for member in result if member['pid']==pid),None) for label,pid in (('OBSERVER',observer_pid),('CALLER',caller_pid))}
    evidence={label.lower():{'present':member is not None,
              'uid_matches':member is not None and member['uid']==expected_uid,
              'parent_matches':None if expected_parent is None else member is not None and member['ppid']==expected_parent}
              for label,expected_uid,expected_parent in (('OBSERVER',observer_uid,caller_pid),('CALLER',uid,caller_parent_pid))
              for member in (anchors[label],)}
    for label in ('CALLER','OBSERVER'):
        if anchors[label] is None:unknown(label+'_ANCHOR_MISSING',affected_anchor=label.lower(),anchors=evidence)
        if not evidence[label.lower()]['uid_matches']:unknown(label+'_UID_MISMATCH',affected_anchor=label.lower(),anchors=evidence)
    for label in ('OBSERVER','CALLER'):
        if evidence[label.lower()]['parent_matches'] is False:unknown('PARENT_IDENTITY_MISMATCH',affected_anchor=label.lower(),anchors=evidence)
    return result


def observer_executable_identity(caller_uid):
    """Fixed trusted ps may have a setuid EUID distinct from its Python parent."""
    target=pathlib.Path('/bin/ps');info=target.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid!=0 or info.st_mode&0o022:
        raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'reason':'OBSERVER_EXECUTABLE_UNTRUSTED'})
    # The root-owned executable must not be replaceable via a writable namespace.
    for parent in set(target.parents)|set(target.resolve(strict=True).parents):
        directory=parent.stat()
        if not stat.S_ISDIR(directory.st_mode) or directory.st_uid!=0 or directory.st_mode&0o022:
            raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'reason':'OBSERVER_EXECUTABLE_UNTRUSTED'})
    setuid=bool(info.st_mode&stat.S_ISUID)
    return {'expected_uid':info.st_uid if setuid else caller_uid,'setuid':setuid,'file_uid':info.st_uid,
            'fingerprint':tuple(getattr(info,name) for name in ('st_dev','st_ino','st_mode','st_uid','st_gid','st_size','st_mtime_ns','st_ctime_ns'))}


def group_members(pgid):
    """Bounded trusted observation, without signaling or selecting foreign PIDs."""
    if pgid is not None and (type(pgid) is not int or pgid<1):
        raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'reason':'OWNED_GROUP_SELECTOR_INVALID'})
    chunks={'stdout':bytearray(),'stderr':bytearray()};code=None;operation='spawn'
    try:
        caller_pid,caller_uid,caller_parent=os.getpid(),os.geteuid(),os.getppid()
        operation='executable-identity';identity=observer_executable_identity(caller_uid)
        operation='spawn'
        process=subprocess.Popen(['/bin/ps','-axo','pid=,ppid=,pgid=,uid=,stat='],stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        operation='selector';selected=selectors.DefaultSelector();end=time.monotonic()+2
        for name in chunks:selected.register(getattr(process,name),selectors.EVENT_READ,name)
        try:
            operation='read'
            while selected.get_map():
                if time.monotonic()>end:raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'reason':'OBSERVER_READ_TIMEOUT'})
                for key,_ in selected.select(.05):
                    data=os.read(key.fileobj.fileno(),4096)
                    if not data:selected.unregister(key.fileobj);continue
                    chunks[key.data].extend(data)
                    if len(chunks[key.data])>(131072 if key.data=='stdout' else 16384):raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'reason':'OBSERVER_OUTPUT_LIMIT','stream':key.data})
            operation='wait'
            code=process.wait(timeout=1)
            if code!=0 or chunks['stderr']:raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'reason':'OBSERVER_NONZERO_EXIT' if code!=0 else 'OBSERVER_STDERR_NOT_EMPTY'})
            operation='executable-identity'
            if observer_executable_identity(caller_uid)!=identity:raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'reason':'OBSERVER_EXECUTABLE_IDENTITY_CHANGED'})
            if os.getpid()!=caller_pid:raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'reason':'CALLER_ANCHOR_MISSING','affected_anchor':'caller'})
            if os.geteuid()!=caller_uid:raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'reason':'CALLER_UID_MISMATCH','affected_anchor':'caller'})
            if os.getppid()!=caller_parent:raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'reason':'PARENT_IDENTITY_MISMATCH','affected_anchor':'caller'})
            operation='parse'
            snapshot=parse_process_snapshot(chunks['stdout'],process.pid,caller_pid,caller_uid,
                       observer_uid=identity['expected_uid'],caller_parent_pid=caller_parent)
            return [member for member in snapshot if pgid is None or member['pgid']==pgid]
        finally:
            selected.close();process.stdout.close();process.stderr.close()
            if process.poll() is None:
                # This only terminates the bounded observer we just created. It
                # never substitutes for signaling the execution process group.
                process.kill();process.wait(timeout=1)
    except CoordinatorDenied as error:
        detail=error.detail or {'reason':'OBSERVER_FAILURE_WITHOUT_DETAIL'}
        uid_evidence={'caller_effective_uid':caller_uid,'observer_expected_uid':identity['expected_uid'],'observer_setuid':identity['setuid']} if 'identity' in locals() else {}
        raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{**detail,**uid_evidence,'observer_operation':operation,'observer_exit_code':code,'stdout_bytes':len(chunks['stdout']),'stderr_bytes':len(chunks['stderr'])}) from None
    except (OSError,ValueError,UnicodeError,subprocess.TimeoutExpired) as error:
        raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'reason':'OBSERVER_TIMEOUT' if isinstance(error,subprocess.TimeoutExpired) else 'OBSERVER_IO_ERROR','observer_operation':operation,'observer_errno':getattr(error,'errno',None),'observer_exit_code':code,'stdout_bytes':len(chunks['stdout']),'stderr_bytes':len(chunks['stderr'])}) from None


def run_job(file):
    """Run only the exact controller-authored job and trusted host entrypoint."""
    job=private_json(file,1048576)
    if set(job)!={'version','request','environment','lease_file','owned_config','execution_root','process_owner'} or job['version']!=1:raise CoordinatorDenied('COORDINATOR_JOB_INVALID')
    request=job['request'];environment=job['environment']
    if not isinstance(request,dict) or not isinstance(environment,dict) or set(environment)-ENVIRONMENT_KEYS or any(not isinstance(v,str) or '\0' in v or len(v)>16384 for v in environment.values()):raise CoordinatorDenied('COORDINATOR_JOB_INVALID')
    if environment.get('CDO_SANDBOX_EXECUTION_MODE')!='hardened-certification' or not environment.get('CDO_CERTIFICATION_PROVIDER_URL'):raise CoordinatorDenied('DETERMINISTIC_CERTIFICATION_REQUIRED')
    for key,expected in {'CDO_RUNTIME_LEASE_FILE':job['lease_file'],'CDO_RUNTIME_OWNED_CONFIG':job['owned_config'],'CDO_RUNTIME_CONTROL_ROOT':job['execution_root'],'CODEX_SOL_MAX_RUNS_5H':'20','CODEX_BUDGET_MODE':'enforce'}.items():
        if key in environment and environment[key]!=expected:raise CoordinatorDenied('COORDINATOR_AUTHORITY_BINDING_CHANGED')
    execution=request.get('execution_id');attempt=request.get('attempt')
    assert_lease(job['lease_file'],execution,attempt)
    private_json(job['owned_config'],65536)
    control=pathlib.Path(job['execution_root'])
    if not control.is_absolute() or control.resolve(strict=True)!=control or not control.is_dir() or control.stat().st_uid!=os.geteuid() or stat.S_IMODE(control.stat().st_mode)!=0o700:raise CoordinatorDenied('COORDINATOR_CONTROL_ROOT_INVALID')
    node=pathlib.Path(environment.get('CDO_RUNTIME_NODE_EXECUTABLE',''))
    entry=pathlib.Path(environment.get('CDO_RUNTIME_NODE_ENTRYPOINT',''))
    for target in (node,entry):
        if not target.is_absolute() or target.resolve(strict=True)!=target or not target.is_file() or target.stat().st_mode&(0o022|stat.S_ISUID|stat.S_ISGID):raise CoordinatorDenied('COORDINATOR_ENTRYPOINT_INVALID')
    interpreter=pathlib.Path(environment.get('CDO_BROKER_PYTHON',''))
    if not interpreter.is_absolute() or interpreter.resolve(strict=True)!=interpreter or interpreter!=pathlib.Path(sys.executable).resolve(strict=True):raise CoordinatorDenied('COORDINATOR_INTERPRETER_IDENTITY_CHANGED')
    if (not (hasattr(os,'waitid') and hasattr(os,'WNOWAIT')) and sys.platform!='darwin') or signal.getsignal(signal.SIGCHLD)!=signal.SIG_DFL:raise CoordinatorDenied('COORDINATOR_PROCESS_OBSERVATION_UNAVAILABLE')
    _,_,binding=job_binding(file)
    work=private_work_root(control,binding['process_owner'])
    if 'CDO_RUNTIME_PRIVATE_ROOT' in environment and environment['CDO_RUNTIME_PRIVATE_ROOT']!=str(work):raise CoordinatorDenied('COORDINATOR_AUTHORITY_BINDING_CHANGED')
    env={'PATH':'/usr/bin:/bin','HOME':str(work),'TMPDIR':str(work),**environment,
         'CDO_RUNTIME_CONTROL_ROOT':str(control),'CDO_RUNTIME_LEASE_FILE':job['lease_file'],
         'CDO_RUNTIME_PRIVATE_ROOT':str(work),
         'CDO_RUNTIME_OWNED_CONFIG':job['owned_config'],
         'CODEX_SOL_MAX_RUNS_5H':'20','CODEX_BUDGET_MODE':'enforce'}
    # Do not knowingly launch a child when the trusted group observer is denied.
    # This observer reads our current group; it sends no signal to any process.
    try:
        preflight=group_members(os.getpgrp())
        if not any(member['pid']==os.getpid() for member in preflight):raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN')
    except CoordinatorDenied as error:
        file=control/'host-signal-diagnostics.json';temporary=file.with_suffix('.tmp')
        temporary.write_text(json.dumps({'version':1,'execution_id':execution,'attempt':attempt,'events':[{'phase':'preflight-observation-denied','at':time.time(),'execution_child_created':False,'parent_pid':os.getpid(),'parent_ppid':os.getppid(),'parent_pgid':os.getpgrp(),'parent_sid':os.getsid(0),'parent_uid':os.getuid(),'parent_euid':os.geteuid(),'detail':error.detail}]}));temporary.chmod(0o600);os.replace(temporary,file)
        raise
    identity=coordinator_identity();parent=os.getppid()
    if any(os.path.lexists(control/name) for name in (OWNER_FILE,ACK_FILE,TEARDOWN_FILE,NODE_OUTCOME_FILE)) or os.path.lexists(work):raise CoordinatorDenied('COORDINATOR_ADMISSION_REPLAY')
    owner={'version':1,'binding':binding,'coordinator':identity,'node':None,'phase':'REGISTERED','no_unknown':True}
    atomic_private(control/OWNER_FILE,owner)
    await_registration_ack(control,binding,identity,job['lease_file'])
    if os.getppid()!=parent:raise CoordinatorDenied('COORDINATOR_PARENT_LOST')
    assert_lease(job['lease_file'],execution,attempt)
    work.mkdir(mode=0o700)
    owner['phase']='NODE_LAUNCHING';atomic_private(control/OWNER_FILE,owner)
    child=subprocess.Popen([str(node),str(entry)],env=env,cwd=str(work),stdin=subprocess.PIPE,stdout=sys.stdout,stderr=sys.stderr,start_new_session=True)
    owner['node']={'pid':child.pid,'pgid':child.pid,'sid':child.pid,'uid':os.geteuid()}
    done=threading.Event();invalid=[];deadline=time.monotonic()+1200;interrupted=[False]
    escalation=threading.Event();forced=threading.Event();reaped=threading.Event()
    force_lock=threading.Lock();cleanup_unknown=threading.Event();observed=threading.Event();force_threads=[];exit_info=[None]
    diagnostics=[];diagnostic_file=control/'host-signal-diagnostics.json'
    terminal_cause=[None];node_exit_code=[None]
    def record(phase,**values):
        if len(diagnostics)>=32:raise CoordinatorDenied('COORDINATOR_DIAGNOSTIC_LIMIT')
        diagnostics.append({'phase':phase,'at':time.time(),'child_pid':child.pid,'parent_pid':os.getpid(),'parent_uid':os.getuid(),'parent_euid':os.geteuid(),'leader_exit_observed':exit_info[0],**values})
        temporary=diagnostic_file.with_suffix('.tmp');temporary.write_text(json.dumps({'version':1,'execution_id':execution,'attempt':attempt,'events':diagnostics}));temporary.chmod(0o600);os.replace(temporary,diagnostic_file)
    def snapshot():
        try:
            try:group=os.getpgid(child.pid);session=os.getsid(child.pid)
            except OSError as error:
                record('group-identity-denied',errno=error.errno);raise CoordinatorDenied('COORDINATOR_GROUP_IDENTITY_UNKNOWN') from None
            try:members=group_members(child.pid)
            except CoordinatorDenied as error:
                record('group-observation-denied',pgid=group,sid=session,detail=error.detail);raise
            record('group-observed',pgid=group,sid=session,members=members)
            if group!=child.pid or session!=child.pid or any(member['uid']!=os.geteuid() for member in members):raise CoordinatorDenied('COORDINATOR_GROUP_IDENTITY_CHANGED')
            if not members:raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN')
            return members
        except CoordinatorDenied:
            cleanup_unknown.set();raise
    def signal_group(signum):
        members=snapshot()
        if all(member['state'].startswith('Z') for member in members):
            record('no-live-members',signal=signum,group_exists=True);return
        record('signal-requested',signal=signum)
        try:os.killpg(child.pid,signum)
        except ProcessLookupError:
            cleanup_unknown.set();record('signal-denied',signal=signum,errno=errno.ESRCH);raise CoordinatorDenied('COORDINATOR_PROCESS_CLEANUP_UNKNOWN') from None
        except OSError:
            error=sys.exc_info()[1];cleanup_unknown.set();record('signal-denied',signal=signum,errno=error.errno);raise CoordinatorDenied('COORDINATOR_PROCESS_CLEANUP_UNKNOWN') from None
    def force_soon():
        with force_lock:
            if escalation.is_set():return
            if cleanup_unknown.is_set():raise CoordinatorDenied('COORDINATOR_PROCESS_CLEANUP_UNKNOWN')
            signal_group(signal.SIGTERM)
            escalation.set()
            def force():
                # WNOWAIT retains the session leader's PID until group cleanup.
                # A recycled unrelated PGID cannot become a signal target.
                time.sleep(2)
                try:
                    with force_lock:
                        if not reaped.is_set():signal_group(signal.SIGKILL)
                except (OSError,CoordinatorDenied):cleanup_unknown.set()
                finally:forced.set()
            thread=threading.Thread(target=force,daemon=True);force_threads.append(thread);thread.start()
    def finish_force():
        if not forced.wait(3):raise CoordinatorDenied('COORDINATOR_PROCESS_CLEANUP_UNKNOWN')
        for thread in force_threads:
            thread.join(timeout=1)
            if thread.is_alive():raise CoordinatorDenied('COORDINATOR_PROCESS_CLEANUP_UNKNOWN')
        if cleanup_unknown.is_set():raise CoordinatorDenied('COORDINATOR_PROCESS_CLEANUP_UNKNOWN')
        # Accepted SIGKILL is not proof of exit. Retain the leader PID until a
        # positive, complete group observation contains no executing member.
        end=time.monotonic()+2
        while True:
            with force_lock:
                try:members=group_members(child.pid)
                except CoordinatorDenied as error:
                    cleanup_unknown.set();record('cleanup-observation-denied',detail=error.detail);raise
                record('cleanup-observed',members=members)
            if all(member['state'].startswith('Z') for member in members):break
            if time.monotonic()>end:cleanup_unknown.set();raise CoordinatorDenied('COORDINATOR_PROCESS_CLEANUP_UNKNOWN')
            time.sleep(.025)
    def persist_teardown():
        # Reaping the leader follows whole-group non-live proof. A separate
        # complete snapshot must now positively show every registered PID/group
        # absent, including zombies; a recycled matching ID is UNKNOWN.
        end=time.monotonic()+2
        while True:
            members=group_members(None)
            if not any(member['pid']==child.pid or member['pgid']==child.pid for member in members):break
            if time.monotonic()>=end:raise CoordinatorDenied('COORDINATOR_PROCESS_CLEANUP_UNKNOWN')
            time.sleep(.025)
        if terminal_cause[0] is None:terminal_cause[0]=node_terminal_cause(control,binding,owner['node'],node_exit_code[0])
        owner['phase']='TEARDOWN';atomic_private(control/OWNER_FILE,owner)
        atomic_private(control/TEARDOWN_FILE,{'version':1,'binding':binding,'coordinator':identity,'node':owner['node'],'node_group_absent':True,'node_reaped':True,'no_unknown':True,'node_exit_code':node_exit_code[0],'terminal_cause':terminal_cause[0],'proof_kind':'anchored-teardown'})
    def stop(*_):
        # A Python signal handler may interrupt the thread while force_lock is
        # held. Notification is one bounded assignment; no lock, I/O or teardown.
        interrupted[0]=True
    prior_signals={signum:signal.getsignal(signum) for signum in (signal.SIGTERM,signal.SIGINT)}
    signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
    def watch():
        while not done.wait(.05):
            try:
                if interrupted[0]:raise CoordinatorDenied('COORDINATOR_INTERRUPTED')
                if os.getppid()!=parent:raise CoordinatorDenied('COORDINATOR_PARENT_LOST')
                if time.monotonic()>deadline:raise CoordinatorDenied('COORDINATOR_EXECUTION_TIMEOUT')
                assert_lease(job['lease_file'],execution,attempt)
            except CoordinatorDenied as error:
                invalid.append(str(error))
                try:force_soon()
                except CoordinatorDenied:pass
                return
    threading.Thread(target=watch,daemon=True).start()
    try:
        # Persist identity before workload; setup failures still use finally.
        owner['phase']='NODE_RUNNING';atomic_private(control/OWNER_FILE,owner)
        child.stdin.write(json.dumps(request,separators=(',',':')).encode());child.stdin.close()
        # Observe exit without reaping; an exited leader still anchors ownership
        # while its children/grandchildren receive bounded group teardown.
        def observe():
            try:exit_info[0]=wait_unreaped(child.pid)
            except (OSError,CoordinatorDenied):cleanup_unknown.set()
            finally:observed.set()
        threading.Thread(target=observe,daemon=True).start()
        while not observed.wait(.025):
            if cleanup_unknown.is_set():raise CoordinatorDenied('COORDINATOR_PROCESS_CLEANUP_UNKNOWN')
        if cleanup_unknown.is_set():raise CoordinatorDenied('COORDINATOR_PROCESS_CLEANUP_UNKNOWN')
        done.set()
        force_soon();finish_force()
        code=child.wait(timeout=2);node_exit_code[0]=code;reaped.set()
        owner['phase']='NODE_REAPED';atomic_private(control/OWNER_FILE,owner)
        assert_lease(job['lease_file'],execution,attempt)
        if interrupted[0] and not invalid:invalid.append('COORDINATOR_INTERRUPTED')
        if invalid:raise CoordinatorDenied(invalid[0])
        return code
    except CoordinatorDenied as error:
        terminal_cause[0]=str(error);raise
    except (OSError,ValueError):
        terminal_cause[0]='COORDINATOR_HOST_IO_FAILED'
        raise CoordinatorDenied('COORDINATOR_HOST_IO_FAILED') from None
    finally:
        done.set()
        try:
            if not reaped.is_set():
                # A concrete control denial is terminal UNKNOWN, never permission
                # to retry through another signal or claim cleanup succeeded.
                if cleanup_unknown.is_set():raise CoordinatorDenied('COORDINATOR_PROCESS_CLEANUP_UNKNOWN')
                force_soon();finish_force()
                node_exit_code[0]=child.wait(timeout=2);reaped.set()
                owner['phase']='NODE_REAPED';atomic_private(control/OWNER_FILE,owner)
            persist_teardown()
        except (OSError,CoordinatorDenied,subprocess.TimeoutExpired):
            owner['no_unknown']=False;owner['phase']='UNKNOWN';atomic_private(control/OWNER_FILE,owner)
            raise CoordinatorDenied('COORDINATOR_PROCESS_CLEANUP_UNKNOWN') from None
        finally:
            for signum,handler in prior_signals.items():signal.signal(signum,handler)


if __name__ == '__main__':
    try:
        if sys.argv[1:2] == ['lease'] and len(sys.argv) == 4:
            environment_lease(sys.argv[2], int(sys.argv[3]))
        elif sys.argv[1:2] == ['cleanup'] and len(sys.argv) == 2:
            from cdo_runtime_owned import cleanup_owned
            cleanup_owned(os.environ['CDO_RUNTIME_OWNED_CONFIG'])
        elif sys.argv[1:2] == ['cleanup-resource'] and len(sys.argv)==3:
            from cdo_runtime_owned import OwnedDocker
            state=private_json(sys.argv[2],8388608)
            result=OwnedDocker(os.environ['CDO_RUNTIME_OWNED_CONFIG']).cleanup_resource(state['container'])
            if result['resources_absent'] is not True:raise CoordinatorDenied('OWNED_RESOURCE_CLEANUP_UNKNOWN')
        elif sys.argv[1:2] == ['reconcile'] and len(sys.argv)==4:
            from cdo_runtime_owned import cleanup_owned
            job,_,_=job_binding(sys.argv[2])
            result=cleanup_owned(job['owned_config'])
            if not isinstance(result,dict) or result.get('resources_absent') is not True or result.get('status')=='UNKNOWN':raise CoordinatorDenied('OWNED_RESOURCE_CLEANUP_UNKNOWN')
            print(json.dumps(reconcile_cleanup(sys.argv[2],private_json(sys.argv[3],65536),oci_absent=True)))
        elif len(sys.argv)==2:
            sys.exit(run_job(sys.argv[1]))
        else:
            raise CoordinatorDenied('COORDINATOR_OPERATION_DENIED')
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
