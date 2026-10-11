import base64
import io
import json
import os
import pathlib
import tempfile
import types
import unittest
from unittest import mock
import cdo_sdk_relay as relay
import cdo_sdk_state as state

class RelayTests(unittest.TestCase):
    def test_frames_are_bounded_and_reject_ambiguous_json(self):
        for data in (b'{"kind":"x","kind":"y"}\n',b'{"x":NaN}\n',b'[]\n',b'x'*(relay.MAX_FRAME+1),b'{}'):
            with self.assertRaises(relay.RelayDenied):relay.read_frame(io.BytesIO(data))
        output=io.BytesIO();relay.Writer(output).send('cli_input',data='YWJj')
        self.assertEqual(relay.read_frame(io.BytesIO(output.getvalue())),{'kind':'cli_input','data':'YWJj'})
    def test_blob_bound(self):
        self.assertEqual(relay.blob('YWJj'),b'abc')
        for value in ('#'*4,base64.b64encode(b'x'*(relay.CHUNK+1)).decode(),None):
            with self.assertRaises(relay.RelayDenied):relay.blob(value)
    def test_closed_endpoint_policy(self):
        self.assertEqual(relay.endpoint('http://127.0.0.1:12345/v1','/v1'),12345)
        for value in ('http://localhost:12345/v1','http://127.0.0.1:12345/v1?url=http://evil','http://127.0.0.1:12345/v1/','https://127.0.0.1:12345/v1','http://user@127.0.0.1:12345/v1','http://127.0.0.1:80/v1','http://127.0.0.1:65536/v1'):
            with self.assertRaises(relay.RelayDenied):relay.endpoint(value,'/v1')
    def test_requests_have_no_generic_destination_or_method(self):
        relay.request_shape('mcp','POST','/','application/json')
        relay.request_shape('provider','POST','/v1/responses','application/json')
        for args in (('docker','POST','/','application/json'),('provider','CONNECT','/v1/responses','application/json'),('mcp','POST','/v1/executions','application/json'),('provider','POST','/v1/responses?x=y','application/json')):
            with self.assertRaises(relay.RelayDenied):relay.request_shape(*args)
    def test_body_limits_preserve_private_state_stress_without_unbounded_input(self):
        self.assertEqual(relay.body_limit('mcp'),2097152)
        self.assertEqual(relay.body_limit('provider'),16777216)
        with self.assertRaises(relay.RelayDenied):relay.body_limit('docker')
    def test_host_config_is_private_regular_and_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            path=pathlib.Path(directory).resolve()/'config.json';token='s'*64
            value={'version':1,'mode':'deterministic-certification','mcp':{'url':'http://127.0.0.1:12345/','token':token},'provider':{'url':'http://127.0.0.1:23456/v1'}}
            path.write_text(json.dumps(value));path.chmod(0o600)
            self.assertEqual(relay.load_config(str(path)),value)
            path.chmod(0o644)
            with self.assertRaises(relay.RelayDenied):relay.load_config(str(path))
            path.chmod(0o600);link=path.with_name('link');link.symlink_to(path)
            with self.assertRaises(relay.RelayDenied):relay.load_config(str(link))
            link.unlink();os.link(path,link)
            with self.assertRaises(relay.RelayDenied):relay.load_config(str(path))
    def test_config_rewrite_does_not_send_host_credential_or_destination(self):
        args=['exec','--config','mcp_servers.cdo.url="http://127.0.0.1:23456/"','--config','mcp_servers.cdo.http_headers.Authorization="Bearer SECRET"','--config','model_providers.cdo_fixture.base_url="http://127.0.0.1:34567/v1"']
        values=relay.rewrite_args(args,{'mcp':1234,'provider':5678})
        self.assertNotIn('SECRET',str(values));self.assertNotIn('23456',str(values));self.assertIn('5678',str(values))
        without_header=args[:3]+args[5:]
        self.assertIn('mcp_servers.cdo.http_headers.Authorization="Bearer local-relay"',relay.rewrite_args(without_header,{'mcp':1234,'provider':5678}))
        with self.assertRaises(relay.RelayDenied):relay.rewrite_args(args[:-2],{'mcp':1234,'provider':5678})
        with self.assertRaises(relay.RelayDenied):relay.rewrite_args(args+args[1:3],{'mcp':1234,'provider':5678})
    def test_host_never_forwards_container_auth_headers(self):
        config={'mcp':{'url':'http://127.0.0.1:12345/','token':'hostsecret'},'provider':{'url':'http://127.0.0.1:23456/v1'}}
        child=mock.Mock(stdin=io.BytesIO(),stdout=io.BytesIO());runtime=relay.HostRelay(config,child,io.BytesIO())
        response=mock.Mock(status=200);response.getheader.return_value='application/json';response.read.return_value=b''
        connection=mock.Mock();connection.getresponse.return_value=response
        with mock.patch.object(relay.http.client,'HTTPConnection',return_value=connection):runtime.fetch(1,{'target':'mcp','path':'/','body':bytearray(b'{}')})
        connection.request.assert_called_once_with('POST','/',body=b'{}',headers={'Content-Type':'application/json','Authorization':'Bearer hostsecret'})
        self.assertIsNone(runtime.failure);runtime.pool.shutdown()
    def test_redirect_is_fatal_and_cannot_change_destination(self):
        child=mock.Mock(stdin=io.BytesIO(),stdout=io.BytesIO());runtime=relay.HostRelay({'provider':{'url':'http://127.0.0.1:23456/v1'}},child,io.BytesIO())
        connection=mock.Mock();connection.getresponse.return_value.status=302
        with mock.patch.object(relay.http.client,'HTTPConnection',return_value=connection):runtime.fetch(1,{'target':'provider','path':'/v1/responses','body':bytearray(b'{}')})
        self.assertEqual(runtime.failure,'SDK_NETWORK_RELAY_HTTP_STATUS_DENIED');child.terminate.assert_called_once();runtime.pool.shutdown()
    def test_host_preserves_cli_prompt_events_and_terminal_exit(self):
        frames=io.BytesIO();writer=relay.Writer(frames);writer.send('cli_output',data='e30K');writer.send('cli_exit',code=0)
        child=mock.Mock(stdin=io.BytesIO(),stdout=io.BytesIO(frames.getvalue()));output=io.BytesIO()
        runtime=relay.HostRelay({},child,output);self.assertEqual(runtime.run(b'prompt'),0);self.assertEqual(output.getvalue(),b'{}\n')
    def test_host_rejects_frame_scope_escape_and_truncated_stream(self):
        for data in (b'{"kind":"request_start","id":1,"target":"docker","method":"POST","path":"/","content_type":"application/json"}\n',b'{"kind":"cli_output","data":"e30K","url":"http://evil"}\n',b''):
            runtime=relay.HostRelay({},mock.Mock(stdin=io.BytesIO(),stdout=io.BytesIO(data)),io.BytesIO())
            with self.assertRaises(relay.RelayDenied):runtime.run(b'prompt')
    def test_container_multiplex_keeps_cli_stdout_separate_from_requests(self):
        def server_factory(runtime,target,handler):
            return mock.Mock(target=target,server_port=1234 if target=='mcp' else 5678)
        frames=io.BytesIO();host_input=io.BytesIO();writer=relay.Writer(host_input)
        writer.send('cli_input',data='cHJvbXB0');writer.send('cli_eof')
        writer.send('response_start',id=1,status=200,content_type='application/json')
        writer.send('response_body',id=1,data='e30=');writer.send('response_end',id=1)
        with mock.patch.object(relay,'_Server',side_effect=server_factory):
            runtime=relay.ContainerRelay(io.BytesIO(host_input.getvalue()),frames)
            ident,results=runtime.request('mcp','/',b'{}')
            child=mock.Mock(stdin=io.BytesIO(),stdout=io.BytesIO(b'{"event":"complete"}\n'))
            child.poll.return_value=0
            runtime.attach(child)
            runtime.threads[-2].join(timeout=2)
            self.assertIsNone(runtime.failure)
            self.assertEqual(results.get(timeout=1)['kind'],'response_start')
            self.assertEqual(relay.blob(results.get(timeout=1)['data']),b'{}')
            self.assertEqual(results.get(timeout=1)['kind'],'response_end')
            runtime.pending.pop(ident)
            runtime.finish(0)
        decoded=[];source=io.BytesIO(frames.getvalue())
        while True:
            value=relay.read_frame(source)
            if value is None:break
            decoded.append(value)
        self.assertEqual([item['kind'] for item in decoded],['request_start','request_body','request_end','cli_output','cli_exit'])
        self.assertEqual(relay.blob(decoded[-2]['data']),b'{"event":"complete"}\n')
    def test_owned_mode_missing_relay_denies_before_docker(self):
        with tempfile.TemporaryDirectory() as directory:
            with mock.patch.dict(os.environ,{'CDO_SDK_STATE_CONTROL':directory,'CDO_SDK_STATE_IMAGE':'image','CDO_RUNTIME_OWNED_CONFIG':'owned-config'},clear=True),mock.patch.object(state.subprocess,'Popen') as spawn:
                with self.assertRaisesRegex(RuntimeError,'SDK_NETWORK_RELAY_CONFIGURATION_REQUIRED'):state.adapter([])
                spawn.assert_not_called()
    def test_deadline_can_interrupt_detached_http_response_socket(self):
        child=mock.Mock(stdin=io.BytesIO(),stdout=io.BytesIO());runtime=relay.HostRelay({},child,io.BytesIO())
        connection=mock.Mock(sock=None);owned_socket=mock.Mock()
        runtime.connections[1]={'connection':connection,'socket':owned_socket}
        runtime.fail('SDK_NETWORK_RELAY_HTTP_TIMEOUT')
        owned_socket.shutdown.assert_called_once_with(relay.socket.SHUT_RDWR);connection.close.assert_called_once()
        runtime.pool.shutdown()
    def test_owned_adapter_never_exposes_host_credentials_or_destinations(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory).resolve();config=root/'relay.json'
            config.write_text(json.dumps({'version':1,'mode':'deterministic-certification','mcp':{'url':'http://127.0.0.1:12345/','token':'host-secret-'+'s'*48},'provider':{'url':'http://127.0.0.1:23456/v1'}}));config.chmod(0o600)
            frame_output=io.BytesIO();relay.Writer(frame_output).send('cli_exit',code=0)
            metrics={'phase':'FINAL','failure':None}
            child=mock.Mock(stdin=io.BytesIO(),stdout=io.BytesIO(frame_output.getvalue()),stderr=io.BytesIO((state.PREFIX+json.dumps(metrics)+'\n').encode()))
            child.wait.return_value=0;child.poll.return_value=0
            runtime=mock.Mock();runtime.popen.return_value=child
            runtime.cleanup_resource.return_value={'resources_absent':True,'status':'ACTIVE'}
            runtime.prove_absent.return_value={'resources_absent':True,'status':'ACTIVE'}
            def execute(argv,**kwargs):
                if argv[0]=='inspect':return types.SimpleNamespace(returncode=1,stdout='[]\n',stderr='Error: No such object: '+argv[1]+'\n')
                return types.SimpleNamespace(returncode=0)
            runtime.run.side_effect=execute
            environment={'CDO_SDK_STATE_CONTROL':str(root),'CDO_SDK_STATE_IMAGE':'sha256:'+'a'*64,'CDO_RUNTIME_OWNED_CONFIG':str(root/'owned.json'),'CDO_RUNTIME_RELAY_CONFIG':str(config),'CDO_DOCKER_EXECUTABLE':str(pathlib.Path('/bin/sh').resolve()),'CDO_DOCKER_HOST':'unix:///private/tmp/fake.sock','CDO_EXECUTION_ID':'11111111-1111-4111-8111-111111111111','CODEX_API_KEY':'NEVER-SEND','CDO_SDK_AUTH_REFERENCE':'NEVER-MOUNT'}
            args=['exec','--config','mcp_servers.cdo.url="http://127.0.0.1:12345/"','--config','model_providers.cdo_fixture.base_url="http://127.0.0.1:23456/v1"']
            with mock.patch.dict(os.environ,environment,clear=True),mock.patch('cdo_runtime_owned.OwnedDocker',return_value=runtime),mock.patch.object(state.signal,'signal'),mock.patch.object(state.sys,'stdin',types.SimpleNamespace(buffer=io.BytesIO(b'prompt'))),mock.patch.object(state.sys,'stdout',types.SimpleNamespace(buffer=io.BytesIO())):
                self.assertEqual(state.adapter(args),0)
            command=runtime.popen.call_args.args[0]
            self.assertEqual(command[command.index('--network')+1],'none')
            for forbidden in ('host-secret','NEVER-SEND','NEVER-MOUNT','12345','23456','host.docker.internal'):
                self.assertNotIn(forbidden,str(command))
            self.assertTrue(json.loads((root/'sdk-metrics.json').read_text())['cleanup'])
            runtime.cleanup_resource.assert_called_once();runtime.prove_absent.assert_called_once()
            # Signal while the owned adapter is still creating/starting OCI.
            handlers={}
            def register(sig,handler):handlers[sig]=handler
            def cancel_during_spawn(*args,**kwargs):
                handlers[state.signal.SIGTERM]()
                return child
            runtime.popen.side_effect=cancel_during_spawn
            with mock.patch.dict(os.environ,environment,clear=True),mock.patch('cdo_runtime_owned.OwnedDocker',return_value=runtime),mock.patch.object(state.signal,'signal',side_effect=register),mock.patch.object(state.sys,'stdin',types.SimpleNamespace(buffer=io.BytesIO(b'prompt'))),mock.patch.object(state.sys,'stdout',types.SimpleNamespace(buffer=io.BytesIO())):
                with self.assertRaisesRegex(RuntimeError,'SDK_PRIVATE_STATE_CANCELLED'):state.adapter(args)
    def test_unknown_ledger_cannot_be_reported_as_sdk_cleanup_success(self):
        runtime=mock.Mock()
        runtime.cleanup_resource.return_value={'resources_absent':True,'status':'UNKNOWN'}
        runtime.prove_absent.return_value={'resources_absent':True,'status':'UNKNOWN'}
        self.assertFalse(state.owned_sdk_cleanup(runtime,'owned-sdk'))
        runtime.cleanup_resource.return_value={'resources_absent':True,'status':'ACTIVE'}
        runtime.prove_absent.return_value={'resources_absent':'true','status':'ACTIVE'}
        self.assertFalse(state.owned_sdk_cleanup(runtime,'owned-sdk'))
    def test_private_state_kernel_profile_is_preserved(self):
        with mock.patch.dict(os.environ,{},clear=True):args=state.oci_command('owned','image',[])
        self.assertIn('seccomp=builtin',args);self.assertIn('fsize=16777216:16777216',args)
        self.assertIn(state.ROOT+':rw,noexec,nosuid,nodev,size=8388608,nr_inodes=256,uid=100,gid=101,mode=0700',args)

if __name__=='__main__':unittest.main()
