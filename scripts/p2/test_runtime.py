import unittest
from runtime import decode_chunked
class ChunkedResponseTests(unittest.TestCase):
 def test_large_json_crosses_arbitrary_chunks(self):
  raw=b'4;extension=test\r\n{"lo\r\n7\r\nng":123\r\n1\r\n}\r\n0\r\nX-Trailer: ignored\r\n\r\n'
  self.assertEqual(decode_chunked(raw),b'{"long":123}')
 def test_truncated_transfer_is_not_silently_accepted(self):
  with self.assertRaises(ValueError):decode_chunked(b'5\r\n123\r\n')
if __name__=='__main__':unittest.main()
