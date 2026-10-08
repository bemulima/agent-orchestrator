#!/usr/bin/env python3
"""Trusted positive runtime observation; unavailable Docker never equals a match."""
import hashlib,json,os,pathlib,subprocess

def sha(p):return hashlib.sha256(pathlib.Path(p).read_bytes()).hexdigest()
def run(*a):return subprocess.check_output(a,timeout=30,text=True).strip()
def observe():
 root=pathlib.Path('/app'); build=json.loads((root/'release/build.json').read_text())
 own=json.loads(run('docker','inspect',os.environ['HOSTNAME']))[0]
 if any(m['Destination']=='/app' or m['Destination'].startswith('/app/') for m in own['Mounts']):raise RuntimeError('SOURCE_OVERLAY_DENIED')
 if own['Config']['Labels'].get('cdo.source_manifest')!=build['source_manifest_sha256']:raise RuntimeError('SOURCE_BUILD_MISMATCH')
 for name,digest in build['installed_files'].items():
  p=root/name
  if p.is_symlink() or not p.is_file() or sha(p)!=digest:raise RuntimeError('BUILD_FILE_MISMATCH')
 for name,target in build['installed_symlinks'].items():
  p=root/name
  if not p.is_symlink() or str(p.readlink())!=target or not p.resolve().is_relative_to(root):raise RuntimeError('BUILD_LINK_MISMATCH')
 version=run('docker','version','--format','{{.Server.Version}}')
 command=json.loads(run('docker','image','inspect',os.environ['CDO_BROKER_IMAGE']))[0]['Id']
 sdk=json.loads(run('docker','image','inspect',os.environ['CDO_SDK_STATE_IMAGE']))[0]['Id']
 ident=dict(build['identity']);ident.update(runner_image=own['Image'],command_image=command,sdk_image=sdk,docker_version=version)
 return ident
if __name__=='__main__':
 try:print(json.dumps(observe(),sort_keys=True))
 except Exception:raise SystemExit('CERTIFIED_RUNTIME_MISMATCH: RUNTIME_IDENTITY_UNKNOWN')
