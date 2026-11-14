#!/usr/bin/env bash
# Exercise the packaged daemon, JVM, web auth and restart hook without an account.
# Uses only new, named Docker volumes. Never mounts user backup/config directories.
# Usage: CPH_SMOKE_EVIDENCE_DIR=/private/output scripts/smoke-test.sh IMAGE
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - "${1:?usage: smoke-test.sh IMAGE}" <<'PY'
import gzip, http.cookiejar, json, os, pathlib, secrets, socket, subprocess, sys, tempfile, time, urllib.error, urllib.request
image=sys.argv[1]
nonce=secrets.token_hex(12)
volumes=[];containers=[]
report={'image':image,'scope':'new account-free local fixture; no user config/source mounts','checks':[]}
output=os.environ.get('CPH_SMOKE_EVIDENCE_DIR')
if output:
 evidence=pathlib.Path(output)
 if evidence.is_symlink():raise SystemExit('SMOKE FAIL: evidence path is a symlink')
 evidence.mkdir(mode=0o700,parents=True,exist_ok=True)
else:evidence=None

def docker(*args,check=True,timeout=45):
 r=subprocess.run(['docker',*args],capture_output=True,text=True,timeout=timeout)
 if check and r.returncode:raise RuntimeError('Docker '+args[0]+' failed: rc='+str(r.returncode)+' stderrBytes='+str(len(r.stderr)))
 return r

def record(name,**fields):report['checks'].append({'check':name,**fields})
def volume(label):
 name='cph-smoke-'+nonce+'-'+label
 docker('volume','create','--label','dev.cph.smoke='+nonce,name);volumes.append(name);return name

def health(name):
 r=docker('exec',name,'/usr/local/bin/cphctl','health',check=False)
 if not r.stdout.strip():return None
 try:return json.loads(r.stdout)['data']
 except (ValueError,KeyError):return None

def wait(name,predicate,seconds=150):
 until=time.monotonic()+seconds
 while time.monotonic()<until:
  value=health(name)
  if value is not None and predicate(value):return value
  state=json.loads(docker('inspect','--format','{{json .State}}',name).stdout)
  if not state['Running']:raise RuntimeError('daemon exited before readiness')
  time.sleep(.3)
 raise RuntimeError('readiness deadline exceeded')

def stop(name):
 docker('stop','--time','150',name,timeout=180)
 state=json.loads(docker('inspect','--format','{{json .State}}',name).stdout)
 if state['Running'] or state['ExitCode']!=0:raise RuntimeError('unclean daemon stop')
 docker('rm',name);containers.remove(name)
 record('clean-stop',exitCode=state['ExitCode'])

try:
 with tempfile.TemporaryDirectory(prefix='cph-smoke-env-') as envdir:
  oldconfig=volume('upgrade');config=volume('config');source=volume('source');restore=volume('restore')
  docker('run','--rm','--network','none','--read-only','--entrypoint','sh','-v',oldconfig+':/config:rw','-v',source+':/source:rw',image,'-ec',
   'umask 077; mkdir -p /config/conf /config/cache; printf -- "-Xmx2G\\n" > /config/conf/jvm_args; printf keep-config > /config/conf/preserved; printf keep-cache > /config/cache/preserved; printf "12.0.0\\n" > /config/cp_version; touch /source/.cph-source-ready')
  name='cph-smoke-'+nonce+'-headless'
  docker('run','-d','--name',name,'--network','none','--read-only','--tmpfs','/tmp:rw,nosuid,nodev','--tmpfs','/run:rw,nosuid,nodev,noexec','-v',oldconfig+':/config:rw','-e','CPH_SOURCE_ROOTS=["/source/missing"]',image);containers.append(name)
  state=wait(name,lambda x:x['status']=='waiting_source',30)
  assert state['pid']==0,'missing source started the JVM'
  docker('exec',name,'sh','-ec','test "$(cat /config/conf/jvm_args)" = -Xmx2G; test "$(cat /config/conf/preserved)" = keep-config; test "$(cat /config/cache/preserved)" = keep-cache')
  network=docker('exec',name,'cat','/proc/net/tcp','/proc/net/tcp6').stdout
  assert len(network.splitlines())==2,'headless TCP listener detected'
  record('upgrade-preservation',settings=True,cache=True,heap=True)
  record('missing-source-refused',pid=0,tcpListeners=0)
  stop(name)
  s=socket.socket();s.bind(('127.0.0.1',0));port=s.getsockname()[1];s.close()
  origin='http://127.0.0.1:'+str(port)
  password=secrets.token_urlsafe(32)
  env=pathlib.Path(envdir)/'web-password'
  env.write_text(password+'\n');env.chmod(0o600)
  name='cph-smoke-'+nonce+'-web'
  docker('run','-d','--name',name,'--read-only','--tmpfs','/tmp:rw,nosuid,nodev','--tmpfs','/run:rw,nosuid,nodev,noexec','--memory','3g','--memory-swap','3g','--security-opt','no-new-privileges',
   '-p','127.0.0.1:'+str(port)+':8080','-v',config+':/config:rw','-v',source+':/storage/test:ro','-v',restore+':/restore:rw',
   '-e','CPH_AGENT=0','-e','CPH_MODE=web','-e','CPH_PUBLIC_ORIGINS='+json.dumps([origin]),'-e','CPH_SOURCE_ROOTS=["/storage/test"]','-e','CPH_SOURCE_SENTINEL=.cph-source-ready','-e','CPH_RESTORE_ROOTS=["/restore"]','-e','CRASHPLAN_SRV_MAX_MEM=1G','-e','CPH_WEB_PASSWORD_FILE=/run/secrets/cph-web-password','-v',str(env)+':/run/secrets/cph-web-password:ro',image);containers.append(name)
  ready=wait(name,lambda x:x['status']=='running' and x['native']['nativeReady'] and x['native']['eventsReady'])
  assert ready['pid']>0 and ready['generation']==1,'JVM generation missing'
  status=json.loads(docker('exec',name,'/usr/local/bin/cphctl','status').stdout)['data']
  pins=dict(line.split('=',1) for line in pathlib.Path('versions.env').read_text().splitlines() if line and not line.startswith('#'))
  assert status['productVersion']==pins['CRASHPLAN_VERSION'] and status['buildNumber']==pins['CRASHPLAN_BUILD'],'vendor version drift'
  assert status['authorized'] is False,'fixture unexpectedly has an account'
  environment=docker('exec',name,'cat','/proc/'+str(ready['pid'])+'/environ').stdout
  assert password not in environment and 'CPH_WEB_PASSWORD=' not in environment and 'CPH_WEB_PASSWORD_FILE=' not in environment,'web password inherited by JVM'
  command=docker('exec',name,'cat','/proc/'+str(ready['pid'])+'/cmdline').stdout
  assert '-javaagent:/usr/local/crashplan/cph/cph-agent.jar' in command,'required runtime agent was disabled'
  record('native-jvm-ready',generation=1,version=status['productVersion'],build=status['buildNumber'],authorized=False,webCredentialAbsentFromJVM=True)
  for program in ['cph-entrypoint','cph-start-gate','cph-healthcheck']:
   docker('exec',name,'sh','-ec','test ! -e /usr/local/bin/'+program)
  record('retired-entrypoints-absent')
  cookies=http.cookiejar.CookieJar();httpclient=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies))
  engine_context=''
  def request(path,method='GET',body=None,headers=None):
   global engine_context
   hdrs={'Origin':origin};hdrs.update(headers or {})
   if method!='GET' and engine_context:hdrs['X-CPH-Engine-Context']=engine_context
   if body is not None:hdrs['Content-Type']='application/json';body=json.dumps(body).encode()
   req=urllib.request.Request(origin+path,data=body,method=method,headers=hdrs)
   try:r=httpclient.open(req,timeout=40)
   except urllib.error.HTTPError as error:r=error
   with r:
    engine_context=engine_context or r.headers.get('X-CPH-Engine-Context','')
    return r.status,dict(r.headers),r.read()
  code,headers,body=request('/',headers={'Accept-Encoding':'gzip'})
  assert code==200 and headers.get('Content-Encoding')=='gzip' and b'<html' in gzip.decompress(body),'embedded SPA unavailable'
  code,_,_=request('/api/v1/health');assert code==401,'web authentication bypass'
  code,_,body=request('/api/v1/session','POST',{'username':'admin','password':password});assert code==200,'web login failed'
  csrf=json.loads(body)['data']['csrf']
  code,_,_=request('/api/v1/health');assert code==200,'authenticated health failed'
  code,_,_=request('/api/v1/engine/restart','POST',{});assert code==403,'missing CSRF accepted'
  code,_,_=request('/api/engine','POST',{}, {'X-CSRF-Token':csrf});assert code==404,'generic native proxy exists'
  scan=docker('exec',name,'/usr/local/bin/cphctl','scan','7',check=False)
  assert scan.returncode==4 and json.loads(scan.stdout)['error']['code']=='NATIVE_AUTHORIZATION_REQUIRED','unsigned scan not refused'
  record('web-and-headless-controls',gzipSPA=True,webLogin=True,csrf=True,noGenericProxy=True,nativeLoginRequired=True)
  roots=json.loads(docker('exec',name,'/usr/local/bin/cphctl','restore','roots').stdout)['data']
  assert len(roots)==1 and roots[0]['id']=='0' and roots[0]['path']=='/restore' and roots[0]['writable'],'packaged restore root missing'
  operations=json.loads(docker('exec',name,'/usr/local/bin/cphctl','restore','operations').stdout)['data']
  assert operations==[],'fresh fixture has restore submissions'
  restore_request={'requestId':'a'*32,'sourceId':'7','targetId':'8','target':{'rootId':'0','relative':''},'existing':'RENAME_ORIGINAL','permissions':'current','includeDeleted':False,'items':[{'path':'/storage/test/file','backupSetId':'1','timestamp':'0','isFolder':False}]}
  code,_,_=request('/api/v1/restore/jobs','POST',restore_request)
  assert code==403,'restore bypassed CSRF'
  code,_,body=request('/api/v1/restore/jobs','POST',restore_request,{'X-CSRF-Token':csrf})
  assert code==409 and json.loads(body)['error']['code']=='NATIVE_AUTHORIZATION_REQUIRED','unsigned restore not refused'
  code,_,body=request('/api/v1/restore/operations')
  assert code==200 and json.loads(body)['data']==[],'unsigned restore created a submission'
  record('packaged-restore-controls',writableRoot=True,emptyJournal=True,csrf=True,nativeLoginRequired=True,noUnsignedSubmission=True)
  # Use the exact vendor hook. It must reach cphd, not the removed PID mailbox.
  docker('exec',name,'/usr/local/crashplan/bin/restartLinux.sh')
  second=wait(name,lambda x:x['generation']==2 and x['status']=='running' and x['native']['nativeReady'] and x['native']['eventsReady'])
  assert second['pid']>0 and second['pid']!=ready['pid'],'vendor hook did not restart the JVM'
  record('vendor-restart-hook',generation=2)
  code,_,body=request('/api/v1/engine/restart','POST',{}, {'X-CSRF-Token':csrf})
  assert code==409 and json.loads(body)['error']['code']=='NATIVE_SESSION_CHANGED','stale web page reached a replacement JVM'
  record('stale-web-context-refused',nativeSessionChanged=True)
  denied=docker('exec','-e','LD_PRELOAD=/usr/local/crashplan/nlib/libwrapper.so',name,'getent','ahosts','download.crashplan.com',check=False)
  local=docker('exec','-e','LD_PRELOAD=/usr/local/crashplan/nlib/libwrapper.so',name,'getent','ahosts','localhost',check=False)
  assert denied.returncode!=0 and not denied.stdout and local.returncode==0 and local.stdout,'update DNS block failed'
  record('update-dns-block',downloadDenied=True,localhostResolves=True)
  logs=docker('logs',name)
  for line in logs.stdout.splitlines():json.loads(line)
  assert password not in logs.stdout and '[cph-agent]' not in logs.stdout,'native console or credential reached product stdout'
  record('private-native-console',structuredProductStdout=True)
  stop(name)
  # A retained heap must come from its complete -Xmx line, even when another
  # size option precedes it. Use only the already stopped fixture config.
  docker('run','--rm','--network','none','--read-only','--entrypoint','sh','-v',config+':/config:rw',image,'-ec',
   'printf -- "-Xms2G\\n-Xmx1536M\\r\\n" > /config/conf/jvm_args')
  name='cph-smoke-'+nonce+'-saved-heap'
  docker('run','-d','--name',name,'--network','none','--read-only','--tmpfs','/tmp:rw,nosuid,nodev','--tmpfs','/run:rw,nosuid,nodev,noexec',
   '--memory','3g','--memory-swap','3g','-v',config+':/config:rw','-v',source+':/storage/test:ro',
   '-e','CPH_SOURCE_ROOTS=["/storage/test"]','-e','CPH_SOURCE_SENTINEL=.cph-source-ready',image);containers.append(name)
  retained=wait(name,lambda x:x['status']=='running' and x['native']['nativeReady'] and x['native']['eventsReady'])
  arguments=docker('exec',name,'cat','/proc/'+str(retained['pid'])+'/cmdline').stdout.split('\0')
  assert [arg for arg in arguments if arg.startswith('-Xmx')]==['-Xmx1536m'],'saved heap was not preserved'
  saved=docker('exec',name,'od','-An','-tx1','/config/conf/jvm_args').stdout
  assert bytes.fromhex(saved)==b'-Xms2G\n-Xmx1536M\r\n','saved heap arguments were rewritten'
  record('retained-heap-launch',heapMiB=1536,otherSizeOptionIgnored=True,crlf=True,savedBytesPreserved=True)
  stop(name)
except BaseException as error:
 report['failure']={'category':type(error).__name__,'message':str(error)}
 raise
finally:
 for name in list(containers):
  try:
   logs=docker('logs',name,check=False)
   if evidence:
    path=evidence/(name+'.log')
    if path.is_symlink():raise RuntimeError('evidence log symlink')
    path.write_text(logs.stdout+logs.stderr);path.chmod(0o600)
   docker('stop','--time','150',name,check=False,timeout=180)
   result=docker('rm',name,check=False)
   if result.returncode:report.setdefault('cleanupFailures',[]).append('container')
  except Exception as error:report.setdefault('cleanupFailures',[]).append(type(error).__name__)
 for name in volumes:
  result=docker('volume','rm',name,check=False)
  if result.returncode:report.setdefault('cleanupFailures',[]).append('volume')
 if evidence:
  path=evidence/'result.json'
  if path.is_symlink():raise RuntimeError('evidence result symlink')
  path.write_text(json.dumps(report,indent=2)+'\n');path.chmod(0o600)
 print(json.dumps(report,indent=2))
 if report.get('cleanupFailures'):raise SystemExit('SMOKE FAIL: fixture cleanup incomplete')
print('SMOKE PASS')
PY
