import unittest
from runtime import decode_chunked
class ChunkedResponseTests(unittest.TestCase):
 def test_large_json_crosses_arbitrary_chunks(self):
  raw=b'4;extension=test\r\n{"lo\r\n7\r\nng":123\r\n1\r\n}\r\n0\r\nX-Trailer: ignored\r\n\r\n'
  self.assertEqual(decode_chunked(raw),b'{"long":123}')
 def test_truncated_transfer_is_not_silently_accepted(self):
  with self.assertRaises(ValueError):decode_chunked(b'5\r\n123\r\n')
if __name__=='__main__':unittest.main()


import asyncio
from runtime import Runtime
class KeepaliveTests(unittest.IsolatedAsyncioTestCase):
 async def test_reuses_framed_responses_including_chunk_trailers(self):
  connections=[]
  async def serve(reader,writer):
   connections.append(1)
   try:
    for i in range(3):
     await reader.readuntil(b'\r\n\r\n')
     if i==1:wire=b'HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n7\r\n{"v":2}\r\n0\r\nX-Test: yes\r\n\r\n'
     else:wire=b'HTTP/1.1 200 OK\r\nContent-Length: 7\r\n\r\n{"v":1}'
     writer.write(wire);await writer.drain()
   finally:writer.close();await writer.wait_closed()
  server=await asyncio.start_server(serve,'127.0.0.1',0)
  runtime=Runtime.__new__(Runtime);runtime.run='test';runtime.client={};runtime.http_pool={};runtime.http_connections=0;runtime.http_reuses=0
  try:
   port=server.sockets[0].getsockname()[1]
   self.assertEqual([await runtime.request(port,'GET','/') for _ in range(3)],[{'v':1},{'v':2},{'v':1}])
   self.assertEqual(len(connections),1);self.assertEqual(runtime.http_reuses,2)
  finally:
   for pool in runtime.http_pool.values():
    for _,writer in pool:writer.close();await writer.wait_closed()
   server.close();await server.wait_closed()
 async def test_failed_write_is_never_retried(self):
  connections=[]
  async def serve(reader,writer):
   connections.append(1);await reader.readuntil(b'\r\n\r\n');writer.close();await writer.wait_closed()
  server=await asyncio.start_server(serve,'127.0.0.1',0)
  runtime=Runtime.__new__(Runtime);runtime.run='test';runtime.client={'LOADGEN_AUTHORIZATION':'test-only'};runtime.http_pool={};runtime.http_connections=0;runtime.http_reuses=0
  try:
   with self.assertRaises(asyncio.IncompleteReadError):await runtime.request(server.sockets[0].getsockname()[1],'PUT','/',{'x':1})
   self.assertEqual(len(connections),1);self.assertEqual(runtime.http_pool[server.sockets[0].getsockname()[1]],[])
  finally:server.close();await server.wait_closed()
