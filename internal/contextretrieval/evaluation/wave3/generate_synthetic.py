"""One-time authoring from frozen selectors; never reads target content.
The public corpus is self-contained. No retrieval output is an input.
"""
import argparse,hashlib,json,pathlib,re
parser=argparse.ArgumentParser(
 description="Author independent synthetic fixtures from external frozen selector metadata. No target source files are opened."
)
parser.add_argument("--inputs", required=True, type=pathlib.Path,
 help="External normalized frozen selector JSON; not a source snapshot or repository root.")
parser.add_argument("--output", required=True, type=pathlib.Path,
 help="Explicit fixture output directory; no default or inferred proof directory.")
arguments=parser.parse_args()
INPUT=arguments.inputs
OUTPUT=arguments.output
H=lambda s:hashlib.sha256(s.encode()).hexdigest()
raw=json.loads(INPUT.read_text())
canonical={
 'backend.domain':('domain','internal/domain'),
 'backend.usecase':('usecase','internal/usecase'),
 'backend.transport.http':('http_transport','internal/transport/http'),
 'backend.transport.message':('message_transport','internal/transport/message'),
 'backend.infrastructure.persistence':('persistence','internal/infrastructure/persistence'),
 'backend.infrastructure.client':('client','internal/infrastructure/http'),
 'backend.infrastructure.messaging':('messaging','internal/infrastructure/messaging'),
 'backend.migration':('migration','db/migrations'),
 'backend.composition':('composition','internal/app'),
 'project.infrastructure':('infrastructure','internal/infrastructure'),
 'project.websocket_transport':('websocket_transport','internal/transport/ws'),
}
concepts={
 'AI-01':'HTTP request decoding and error mapping', 'AI-02':'template version and repository boundaries',
 'AI-03':'provider port and request result types', 'AI-04':'idempotent repository state and migration evidence',
 'AI-05':'provider adapter source and bounded outputs', 'AI-06':'authoring schema and provider port',
 'AI-07':'cancellation failure mapping and persistence evidence', 'AI-08':'semantic input normalization and source tests',
 'D01':'HTTP message window and member access', 'D02':'conversation ownership and context binding',
 'D03':'turn identity and domain envelope', 'D04':'idempotent repository lookup and replay comparison',
 'D05':'event envelope and broker transport source', 'D06':'receipt persistence and token boundary',
 'D07':'read cursor source and member state', 'D08':'websocket ticket and ordered lifecycle source',
 'W3-S01':'workspace lifecycle ports and composition source', 'W3-S02':'workspace file revision and failed operation source',
 'W3-S03':'actor permission and ticket boundary', 'W3-S04':'runtime profile and resource policy declarations',
 'W3-S05':'live validation request and advisory result', 'W3-S06':'archive event and idle lifecycle source',
 'W3-S07':'immutable snapshot and retained generation source', 'W3-S08':'workspace digest and attestation source',
 'V01':'HTTP validation request guards and composition source', 'V02':'stage selection and dependency ordering source',
 'V03':'domain contract shape and inline files', 'V04':'retained snapshot reader and digest source',
 'V05':'outbound engine registry and delegation source', 'V06':'correlated validation result and completion boundary',
 'V07':'contradictory result and technical failure source', 'V08':'contract inspection and receipt source tests',
 'X1':'conversation persistence and pedagogy read boundary', 'X2':'authoring provider request and candidate read boundary',
 'X3':'workspace validation request and selection read boundary', 'X4':'validation result and completion evidence read boundary',
}
cases=[];lineage=[];manifest=[]
for c in raw:
 cid=c['ID'];slug=re.sub('[^A-Za-z0-9]','',cid);documents={};decls={};map_labels={};ordinal=[0]
 def put(source,path,body):
  documents[(source,path)]={'Source':source,'Path':path,'Content':body,'Hash':H(body)}
 def role_body(sym,typ,serial,path):
  prefix='package fixture\n\n'
  if typ=='test':return prefix+'import "testing"\n\nfunc '+sym+'(t *testing.T) {\n if got := '+str(serial)+'; got < 1 { t.Fatal("synthetic invariant") }\n}\n'
  if typ=='value':return prefix+'const '+sym.split('.')[-1]+' = "independent specimen '+str(serial)+'"\n'
  if typ=='type':return prefix+'type '+sym+' struct {\n Key string\n Revision uint64\n Outcome []string\n}\n'
  receiver=sym.split('.')[0] if '.' in sym else ''; name=sym.split('.')[-1]
  header=''
  if 'infrastructure/http' in path or '/client' in path:header='import "net/http"\n\n'
  definitions='type Input'+str(serial)+' struct { Key string; Mode string }\ntype Result'+str(serial)+' struct { Code int; Values []string }\ntype Port'+str(serial)+' interface { Observe(Input'+str(serial)+') (Result'+str(serial)+', error) }\n'
  if receiver:definitions+='type '+receiver+' struct { Port Port'+str(serial)+' }\n'
  func='func '
  if receiver:func+='(n *'+receiver+') '
  func+=name+'(input Input'+str(serial)+') (Result'+str(serial)+', error) {\n'
  if header:func+=' req, err := http.NewRequest(http.MethodGet, "https://fixture.invalid/specimen", nil)\n if err != nil { return Result'+str(serial)+'{}, err }; _ = req\n'
  func+=' if input.Key == "" { return Result'+str(serial)+'{Code: '+str(serial)+'}, nil }\n return Result'+str(serial)+'{Values: []string{input.Key}}, nil\n}\n'
  return prefix+header+definitions+func
 def map_label(f):
  key=(f['source_identity'],f['path'],f.get('symbol',''))
  if key in map_labels:return map_labels[key]
  ordinal[0]+=1;serial=ordinal[0];source=f['source_identity'];oldpath=f['path'];oldsym=f.get('symbol','');p=pathlib.PurePosixPath(oldpath);extension=p.suffix
  if oldpath in ('.ai/service.yaml','.ai/architecture.yaml'):newpath=oldpath
  elif extension=='.go':newpath=str(p.parent/('specimen_'+slug.lower()+'_'+str(serial)+('_test' if oldpath.endswith('_test.go') else '')+'.go'))
  elif extension=='.sql':newpath=str(p.parent/('001_specimen_'+slug.lower()+'_'+str(serial)+'.up.sql'))
  else:newpath=str(p.parent/('specimen-'+slug.lower()+'-'+str(serial)+extension))
  sym=''
  if extension=='.go':
   if oldpath.endswith('_test.go'):sym='TestSpecimen'+slug+str(serial);typ='test'
   elif '/schema/' in oldpath:sym='fixture.specimen'+slug+str(serial);typ='value'
   elif '.' in oldsym:sym='Node'+slug+str(serial)+'.Observe';typ='func'
   elif oldsym and oldsym[0].islower():sym='inspect'+slug+str(serial);typ='func'
   else:sym='Shape'+slug+str(serial);typ='type'
   body=role_body(sym,typ,serial,newpath)
  elif extension=='.sql':body='CREATE TABLE specimen_'+slug.lower()+'_'+str(serial)+' (specimen_key TEXT PRIMARY KEY, specimen_revision BIGINT NOT NULL);\n'
  elif extension in ('.yaml','.yml'):body='schema_version: 1\nname: synthetic-'+slug.lower()+'-'+str(serial)+'\nkind: read-evidence\nsource_only: true\n'
  else:body='# Independent synthetic contract '+slug+' '+str(serial)+'\n\nA specimen request contains Key and Revision. A specimen result contains Code and Values.\nThis fixture is source evidence. It grants no write, execution, provider, or completion authority.\n'
  put(source,newpath,body)
  label={'Source':source,'Path':newpath,'Symbol':sym,'Hash':H(body)};map_labels[key]=label
  lineage.append({'CaseID':cid,'OriginalSource':source,'OriginalPath':oldpath,'OriginalHash':f['expected_hash'],'SyntheticSource':source,'SyntheticPath':newpath,'SyntheticSymbol':sym,'SyntheticHash':label['Hash']})
  return label
 prepare=[map_label(f) for f in c['Request']['required_facets']]
 expand=[map_label(f) for f in c['Expands']]
 def map_external_label(x):
  return map_label({'source_identity':x['Source'],'path':x['Path'],'symbol':x.get('Symbol',''),'expected_hash':x['Hash']})
 tests=[map_external_label(x) for x in c['Tests']];contracts=[map_external_label(x) for x in c['Contracts']]
 sources={s['identity']:dict(s) for s in c['Request']['sources']}
 source_layers={s:{} for s in sources};expected_layers=[];additional=[]
 for i,l in enumerate(c['LayerObligations']):
  src=l['Source'];orig=l['Layer']
  if orig in canonical:role,directory=canonical[orig];expected=orig
  elif orig.startswith('outbound:'):role='outbound';directory=orig.split(':',1)[1];expected=orig
  elif ':' in orig:
   lead,directory=orig.split(':',1);role=re.sub('[^a-z0-9]+','_',lead.lower()).strip('_');expected='project.'+role
  else:raise ValueError(orig)
  source_layers[src].setdefault(role,[])
  if directory not in source_layers[src][role]:source_layers[src][role].append(directory)
  expected_layers.append({'Source':src,'Layer':expected,'OriginalLayer':orig,'Obligation':i})
  # An independently selected exact anchor tests every retained category, including
  # roles with no canonical directory in the frozen real-source selector set.
  serial=900+i;path=directory+'/layer_'+slug.lower()+'_'+str(i)+('.up.sql' if orig=='backend.migration' else '.go')
  symbol='' if orig=='backend.migration' else 'Layer'+slug+str(i)
  body=('CREATE TABLE synthetic_layer_'+slug.lower()+'_'+str(i)+' (specimen_key TEXT);\n') if not symbol else role_body(symbol,'type',serial,path)
  put(src,path,body)
  additional.append({'Source':src,'Path':path,'Symbol':symbol,'Hash':H(body)})
 # Authored fixture metadata is independent; private metadata bodies were not read.
 for src,s in sources.items():
  yaml='schema_version: 1\nlayers:\n'
  # Canonical shape is independently present even when a scenario starts at domain.
  layers=dict(source_layers[src]);layers.setdefault('domain',['internal/domain']);layers.setdefault('usecase',['internal/usecase'])
  for role,paths in sorted(layers.items()):yaml+='  '+role+': ['+', '.join(paths)+']\n'
  put(src,'.ai/architecture.yaml',yaml)
  put(src,'.ai/service.yaml','schema_version: 1\nname: '+s['route_identity']+'\nstack: [go]\n')
  put(src,'go.mod','module fixture.invalid/'+slug.lower()+'/'+s['route_identity']+'\n\ngo 1.23\n')
  put(src,'internal/domain/specimen_shape.go','package fixture\n\ntype SpecimenIdentity struct { Key string }\n')
  put(src,'internal/usecase/specimen_operation.go','package fixture\n\ntype SpecimenOperation struct {}\nfunc (SpecimenOperation) Inspect() bool { return true }\n')
  for role in ['transport','infrastructure']:
   put(src,'internal/'+role+'/specimen_profile_shape.go','package fixture\n\ntype IndependentProfile'+role.title()+'Shape struct { SpecimenID string }\n')
  put(src,'internal/irrelevant/decoy.go','package decoy\n\ntype UnrelatedMaterial struct { Ignored int }\n')
 # Rebind metadata facet hashes after authoring final metadata, not after retrieval.
 for label in prepare+expand+tests+contracts:
  label['Hash']=documents[(label['Source'],label['Path'])]['Hash']
 facets=[]
 def facet(l,id,kind=None):
  go=l['Path'].endswith('.go');contract=kind=='contract'
  return {'id':id,'kind':'contract' if contract else ('symbol' if go else 'document'),'query_kind':'contract' if contract else ('definition' if go else 'exact'),'source_identity':l['Source'],'path':l['Path'],'symbol':l['Symbol'],'resolver':'contract' if contract else ('go' if go else 'exact'),'claim_type':'public_api_contract' if contract else 'implementation_behavior','expected_hash':l['Hash'],'required':True}
 for i,l in enumerate(prepare):facets.append(facet(l,cid+'-prepare-'+str(i)))
 expands=[]
 for i,(f,l) in enumerate(zip(c['Expands'],expand)):expands.append(facet(l,cid+'-expand-'+str(i),'contract' if f['kind']=='contract' else None))
 existing={(f['source_identity'],f['path'],f['symbol']) for f in facets+expands}
 for i,l in enumerate(tests+contracts+additional):
  k=(l['Source'],l['Path'],l['Symbol'])
  if k not in existing:
   expands.append(facet(l,cid+'-supplement-'+str(i),'contract' if l in contracts else None));existing.add(k)
 admissions=[]
 for src,s in sorted(sources.items()):
  admissions.append({'identity':src,'route_identity':s['route_identity'],'read_paths':['go.mod','.ai','docs','internal','cmd','db/migrations','migrations','test','tests','contracts','api','dto','mapper','public','config'],'read_only_neighbor':s['read_only_neighbor'],'revision':'1'*40})
 # Every body, path and role included in the relevant set has a prior independent
 # obligation or supports admission/shape; the deliberately unrelated decoy is excluded.
 relevant=[{'Source':src,'Path':p} for (src,p),d in sorted(documents.items()) if '/irrelevant/' not in p]
 original_expand_indexes=[]
 for item in c['Expand']:
  key=(item['Source'],item['Path'],item.get('Symbol',''))
  original_expand_indexes.append(next(i for i,f in enumerate(c['Expands']) if (f['source_identity'],f['path'],f.get('symbol',''))==key))
 category=c['OriginalScenario'].get('Category') or c['OriginalScenario'].get('category') or c['OriginalScenario'].get('Categories') or 'cross-service boundary'
 task='Analyze '+concepts[cid]+' in '+c['Target']+'. Inspect associated source contracts and tests. No execution is requested.'
 cases.append({'ID':cid,'Target':c['Target'],'Category':category,'Task':task,'Owner':c['Owner'],'Prepare':prepare,'Expand':expand,'Tests':tests,'Contracts':contracts,'Layers':expected_layers,'LayerAnchors':additional,'Relevant':relevant,'Sources':admissions,'RequiredFacets':facets,'Expands':expands,'Documents':list(documents.values()),'OriginalExpandOccurrences':original_expand_indexes,'FrozenOriginalCounts':{k:len(c[k]) for k in ['Prepare','Expand','Tests','Contracts','LayerObligations']},'SyntheticOnly':True})
 for (src,p),d in sorted(documents.items()):manifest.append({'CaseID':cid,'Source':src,'Path':p,'SHA256':d['Hash'],'Origin':'INDEPENDENT_SYNTHETIC_AUTHORING','TargetBodyRead':False})
for x in lineage:
 for c in cases:
  if c['ID']==x['CaseID']:
   for d in c['Documents']:
    if d['Source']==x['SyntheticSource'] and d['Path']==x['SyntheticPath']:x['SyntheticHash']=d['Hash']
OUTPUT.mkdir(parents=True,exist_ok=True)
for name,value in [('cases.json',cases),('lineage.json',lineage),('safe-body-manifest.json',manifest)]:
 (OUTPUT/name).write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n')
# The lineage carries original path/hash only. No private source symbols or prose.
report={'schema':'wave3-synthetic-gold-design.v1','case_count':len(cases),'expectations_frozen_before_retrieval':True,'retrieval_output_used':False,'target_bodies_read':False,'all_bodies_independently_authored':True,'original_frozen_denominators':{k:sum(c['FrozenOriginalCounts'][k] for c in cases) for k in ['Prepare','Expand','Tests','Contracts','LayerObligations']},'additional_exact_role_anchors':sum(len(c['LayerAnchors']) for c in cases),'source_binding':'filesystem snapshot/content hash; Git object unsupported','body_manifest_sha256':H((OUTPUT/'safe-body-manifest.json').read_text()),'cases_sha256':H((OUTPUT/'cases.json').read_text()),'lineage_sha256':H((OUTPUT/'lineage.json').read_text()),'negative_controls':['wrong hash','stale snapshot','missing required business contract','untrusted template injection','read-only execution interface','budget omission','truncated source scan','authority conflict','forged Expand cache','read-only neighbor owner promotion','unsupported Git pin'],'scope':'package internal/contextretrieval/evaluation/wave3 only'}
# Report to stdout only: a caller may retain it in their own evidence location.
print(json.dumps(report,indent=2))
