#!/usr/bin/env python3
"""Snapshot exact production inputs, then build only that snapshot. No Git mutation."""
import argparse,hashlib,json,pathlib,shutil,subprocess,time
p=argparse.ArgumentParser();p.add_argument('--output',required=True);p.add_argument('--tag',required=True);a=p.parse_args()
repo=pathlib.Path(__file__).resolve().parents[1];out=pathlib.Path(a.output).resolve();out.mkdir(parents=True,exist_ok=False);snapshot=out/'source';snapshot.mkdir()
files=[]
for name in ['cmd','internal','runner','docker','scripts','agent-system','.ai','go.mod','go.sum']:
 source=repo/name
 files.extend([source] if source.is_file() else [f for f in source.rglob('*') if f.is_file()])
manifest={}
for f in sorted(files):
 rel=f.relative_to(repo)
 if any(x in ['node_modules','dist','.cache','__pycache__'] or x.startswith('._') for x in rel.parts):continue
 if f.is_symlink():raise RuntimeError('SYMLINK_BUILD_INPUT_DENIED')
 if f.name=='.env' or f.name.startswith('.env.'):raise RuntimeError('SECRET_BUILD_INPUT_DENIED')
 digest=hashlib.sha256(f.read_bytes()).hexdigest();manifest[str(rel)]=digest
 dest=snapshot/rel;dest.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(f,dest)
 if hashlib.sha256(dest.read_bytes()).hexdigest()!=digest:raise RuntimeError('SOURCE_SNAPSHOT_MISMATCH')
canonical=json.dumps(manifest,sort_keys=True,separators=(',',':')).encode();identity=hashlib.sha256(canonical).hexdigest()
(out/'source-manifest.json').write_bytes(canonical)
(snapshot/'release').mkdir();(snapshot/'release/source-manifest.json').write_bytes(canonical)
(snapshot/'release/source-identity').write_text(identity)
# Freeze the captured bytes. No source edits are applied to the snapshot afterwards.
for f in snapshot.rglob('*'):
 if f.is_file():f.chmod(f.stat().st_mode&~0o222)
with (out/'build.log').open('w') as log:
 result=subprocess.run(['docker','build','--label','cdo.source_manifest='+identity,'-t',a.tag,'-f',str(snapshot/'docker/ReleaseDockerfile'),str(snapshot)],stdout=log,stderr=subprocess.STDOUT)
(out/'build-result.json').write_text(json.dumps({'source_manifest_sha256':identity,'exit_code':result.returncode,'tag':a.tag,'built_at':time.time()},indent=2))
if result.returncode:raise SystemExit(result.returncode)
image=json.loads(subprocess.check_output(['docker','image','inspect',a.tag],timeout=30))[0]
(out/'image-identity.json').write_text(json.dumps({'image_id':image['Id'],'repo_digests':image['RepoDigests'],'source_manifest_sha256':identity},indent=2))
print(image['Id'])
